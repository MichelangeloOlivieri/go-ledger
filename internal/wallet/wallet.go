package wallet

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WalletService struct {
	db *pgxpool.Pool
}

func NewWalletService(db *pgxpool.Pool) *WalletService {
	return &WalletService{db: db}
}

var (
	ErrIdempotencyMismatch = errors.New("idempotency_mismatch")
	ErrInsufficientFunds   = errors.New("insufficient_funds")
	ErrAccountNotFound     = errors.New("account_not_found")
)

// implements transfer logic (Pessimistic Lock) with Lock Hierarchy (accounts), Double-Entry Bookkeeping (entries), and Transactional Outbox (events)
func (w *WalletService) Transfer(ctx context.Context, fromId int64, toId int64, amount int64, idempotencyKey string) error {
	if fromId == toId {
		return fmt.Errorf("self transfer not allowed")
	}
	if amount <= 0 {
		return fmt.Errorf("non-positive amount not allowed")
	}
	if idempotencyKey == "" {
		return fmt.Errorf("missing idempotency key")
	}

	// 1. Begin
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 2. Idempotency checks: does nothing if the same sender uses the same idempotency key more than once
	idempotencyQuery := `INSERT INTO idempotency_keys (from_id, to_id, amount, key) VALUES ($1, $2, $3, $4) ON CONFLICT (from_id, key) DO NOTHING;`
	tag, err := tx.Exec(ctx, idempotencyQuery, fromId, toId, amount, idempotencyKey)

	if err != nil {
		return fmt.Errorf("idempotency claim error: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var extractedId int64
		var extractedAmount int64

		if err := tx.QueryRow(ctx, "SELECT to_id, amount FROM idempotency_keys WHERE from_id = $1 AND key = $2;", fromId, idempotencyKey).Scan(&extractedId, &extractedAmount); err != nil {
			return fmt.Errorf("could not read idempotency key: %w", err)
		}

		// preventing bugged client to perform more than one action (sending to a different receiver or sending a different amount) with the same logical identifier (idempotencyKey)
		if toId != extractedId || amount != extractedAmount {
			return ErrIdempotencyMismatch
		}

		// else, it was just a retry: return nil, signaling the transaction was ALREADY processed
		return nil
	}

	// transfers
	var transferId int64
	if err := tx.QueryRow(ctx, `INSERT INTO transfers DEFAULT VALUES RETURNING id;`).Scan(&transferId); err != nil {
		return fmt.Errorf("could not insert transfer_id: %w", err)
	}

	// 3. Batch preparation
	batch := &pgx.Batch{}
	// lock ordering
	first := fromId
	second := toId
	senderFirst := true
	if fromId > toId {
		first = toId
		second = fromId
		senderFirst = false
	}

	// accounts
	if senderFirst {
		batch.Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, first)
		batch.Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, second)
	} else {
		batch.Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, first)
		batch.Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, second)
	}

	// entries
	batch.Queue(`INSERT INTO entries (transfer_id, account_id, amount) VALUES ($1, $2, $3)`, transferId, fromId, -amount)
	batch.Queue(`INSERT INTO entries (transfer_id, account_id, amount) VALUES ($1, $2, $3)`, transferId, toId, amount)

	// events
	outboxPayload := fmt.Sprintf(`{"from": %d, "to": %d, "amount": %d}`, fromId, toId, amount)
	batch.Queue(`INSERT INTO events (aggregate_id, event_type, payload) VALUES ($1, 'TRANSFER_COMPLETED', $2)`, strconv.FormatInt(transferId, 10), outboxPayload)

	// 4. Batch execution
	br := tx.SendBatch(ctx, batch)

	// first two updates
	for range 2 {
		tag, err := br.Exec()
		if err != nil {
			_ = br.Close()
			// postgres code for insufficient funds
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23514" {
				return ErrInsufficientFunds
			}
			return fmt.Errorf("update into accounts table failed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			_ = br.Close()
			return ErrAccountNotFound
		}
	}

	// last three inserts
	for range 2 {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("insert failed in either entries or events table: %w", err)
		}
	}

	if err = br.Close(); err != nil {
		return fmt.Errorf("failed to close batch: %w", err)
	}

	// 5. Commit
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	return nil
}
