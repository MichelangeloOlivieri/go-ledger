package wallet

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type WalletService struct {
	db *pgxpool.Pool
}

func NewWalletService(db *pgxpool.Pool) *WalletService {
	return &WalletService{db: db}
}

// Credit implements OCC and the Transactional Outbox pattern.
func (w *WalletService) Credit(ctx context.Context, amount int64) (int64, error) {
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		// 1. Initiate Atomic SQL Transaction
		tx, err := w.db.Begin(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to begin transaction: %v", err)
		}

		defer tx.Rollback(ctx)

		var currentBalance int64
		var currentVersion int

		// 2. Read state bound to the transaction
		err = tx.QueryRow(ctx, "SELECT balance, version FROM wallets WHERE id = 1").Scan(&currentBalance, &currentVersion)
		if err != nil {
			return 0, fmt.Errorf("failed to read wallet state: %v", err)
		}

		newBalance := currentBalance + amount
		newVersion := currentVersion + 1

		// 3. Conditional Write
		tag, err := tx.Exec(ctx,
			"UPDATE wallets SET balance = $1, version = $2 WHERE id = 1 AND version = $3",
			newBalance, newVersion, currentVersion)
		if err != nil {
			return 0, fmt.Errorf("failed to execute conditional update: %v", err)
		}

		// 4. Collision Handling (Data-Race detected)
		if tag.RowsAffected() == 0 {
			tx.Rollback(ctx)

			jitter := time.Duration(rand.Intn(50*(i+1))) * time.Millisecond
			log.Printf("[OCC] Collision on attempt %d. Jitter Backoff: %v...", i+1, jitter)

			select {
			case <-time.After(jitter):
				continue
			case <-ctx.Done():
				return 0, fmt.Errorf("client timeout during retry: %v", ctx.Err())
			}
		}

		// 5. Transactional Outbox
		payload := fmt.Sprintf(`{"amount_credited": %d, "new_balance": %d}`, amount, newBalance)
		_, err = tx.Exec(ctx,
			"INSERT INTO outbox_events (aggregate_id, event_type, payload) VALUES ($1, $2, $3)",
			1, "CREDIT_ACCEPTED", payload)
		if err != nil {
			return 0, fmt.Errorf("failed to persist outbox event: %v", err)
		}

		// 6. Atomic Commit
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("fatal commit error: %v", err)
		}

		return newBalance, nil
	}

	return 0, fmt.Errorf("transaction failed: excessive concurrency on target row")
}
