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
	ErrInsufficientFunds = errors.New("insufficient_funds")
	ErrAccountNotFound   = errors.New("account_not_found")
)

// implements transfer logic (Pessimistic Lock) with Lock Hierarchy, Double-Entry Bookkeeping, and Transactional Outbox
func (w *WalletService) Transfer(ctx context.Context, fromId int64, toId int64, amount int64, idempotencyKey string) error {
	if fromId == toId {
		return fmt.Errorf("self-transfer not allowed")
	}

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Idempotency Check
	idempotencyQuery := `INSERT INTO idempotency_keys (account_id, key) VALUES ($1, $2) ON CONFLICT (account_id, key) DO NOTHING;`
	tag, err := tx.Exec(ctx, idempotencyQuery, fromId, idempotencyKey)
	if err != nil {
		return fmt.Errorf("idempotency claim error: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	var transferID int64
	insertTransferID := `INSERT INTO transfers DEFAULT VALUES RETURNING id;`
	if err := tx.QueryRow(ctx, insertTransferID).Scan(&transferID); err != nil {
		return fmt.Errorf("could not insert transfer_id: %w", err)
	}

	// 2. Lock Ordering
	firstID, secondID := fromId, toId
	senderFirst := true
	if fromId > toId {
		firstID, secondID = toId, fromId
		senderFirst = false
	}

	// 3. Batch Construction
	batch := &pgx.Batch{}

	// accounts
	if senderFirst {
		batch.Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, firstID)
		batch.Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, secondID)
	} else {
		batch.Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, firstID)
		batch.Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, secondID)
	}

	// entries
	batch.Queue(`INSERT INTO entries (transfer_id, account_id, amount) VALUES ($1, $2, $3)`, transferID, fromId, -amount)
	batch.Queue(`INSERT INTO entries (transfer_id, account_id, amount) VALUES ($1, $2, $3)`, transferID, toId, amount)

	// events
	outboxPayload := fmt.Sprintf(`{"from": %d, "to": %d, "amount": %d}`, fromId, toId, amount)
	batch.Queue(`INSERT INTO events (aggregate_id, event_type, payload) VALUES ($1, 'TRANSFER_COMPLETED', $2)`, strconv.FormatInt(transferID, 10), outboxPayload)

	// 4. Execution
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
	for range 3 {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("insert failed (either entries or outbox table): %w", err)
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
