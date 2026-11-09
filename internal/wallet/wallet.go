package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInsufficientFunds = errors.New("insufficient_funds")
	ErrAccountNotFound   = errors.New("account_not_found")
)

type WalletService struct {
	db *pgxpool.Pool
}

func NewWalletService(db *pgxpool.Pool) *WalletService {
	return &WalletService{db: db}
}

// implements transfer logic (Pessimistic Lock) with Lock Hierarchy, Double-Entry Bookkeeping, and Transactional Outbox
func (w *WalletService) Transfer(ctx context.Context, fromID, toID int64, amount int64, idempotencyKey string) error {
	if fromID == toID {
		return fmt.Errorf("self-transfer not allowed")
	}

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Idempotency Check
	idempotencyQuery := `INSERT INTO idempotency_keys (key) VALUES ($1) ON CONFLICT (key) DO NOTHING;`
	tag, err := tx.Exec(ctx, idempotencyQuery, idempotencyKey)
	if err != nil {
		return fmt.Errorf("idempotency claim error: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	// 2. Lock Ordering
	firstID, secondID := fromID, toID
	isFirstSender := true
	if fromID > toID {
		firstID, secondID = toID, fromID
		isFirstSender = false
	}

	// 3. Batch Construction
	batch := &pgx.Batch{}

	// lock ordering
	if isFirstSender {
		batch.Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, firstID)
		batch.Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, secondID)
	} else {
		batch.Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, firstID)
		batch.Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, secondID)
	}

	// Double-Entry Bookkeeping
	batch.Queue(`INSERT INTO entries (idempotency_key, account_id, amount) VALUES ($1, $2, $3)`, idempotencyKey, fromID, -amount)
	batch.Queue(`INSERT INTO entries (idempotency_key, account_id, amount) VALUES ($1, $2, $3)`, idempotencyKey, toID, amount)

	// Transactional Outbox
	outboxPayload := fmt.Sprintf(`{"from": %d, "to": %d, "amount": %d}`, fromID, toID, amount)
	batch.Queue(`INSERT INTO outbox_events (aggregate_id, event_type, payload) VALUES ($1, 'TRANSFER_COMPLETED', $2)`, idempotencyKey, outboxPayload)

	// 4. Atomic Execution
	br := tx.SendBatch(ctx, batch)

	// execute the two updates
	for i := 0; i < 2; i++ {
		tag, err := br.Exec()
		if err != nil {
			_ = br.Close()
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23514" {
				return ErrInsufficientFunds
			}
			return fmt.Errorf("account update failed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			_ = br.Close()
			return ErrAccountNotFound
		}
	}

	// execute the three inserts
	for i := 0; i < 3; i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("audit/outbox insert failed: %w", err)
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
