package wallet

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const EventTypeCreditAccepted = "CREDIT_ACCEPTED"

// NOTE: despite the theoretical violation of DIP, the core business logic (ACID consistency, idempotency, CDC outbox) relies entirely on PostgreSQL's engine via a CTE and row-level locks
type WalletService struct {
	db *pgxpool.Pool
}

func NewWalletService(db *pgxpool.Pool) *WalletService {
	return &WalletService{db: db}
}

// implements business logic with atomic increment and the Transactional Outbox pattern
func (w *WalletService) Credit(ctx context.Context, walletID int, amount int64, idempotencyKey string) (int64, error) {

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var savedBalance *int64

	// lock-safe idempotency check
	idempotencyQuery := `
		INSERT INTO idempotency_keys (key) VALUES ($1)
		ON CONFLICT (key) DO UPDATE SET key = EXCLUDED.key
		RETURNING response_balance
	`
	err = tx.QueryRow(ctx, idempotencyQuery, idempotencyKey).Scan(&savedBalance)
	if err != nil {
		return 0, fmt.Errorf("idempotency claim error: %v", err)
	}

	if savedBalance != nil {
		return *savedBalance, nil
	}

	// wallet update, outbox insert, and idempotency persistence (implicitly transactional)
	businessQuery := `
		WITH updated_wallet AS (
			UPDATE wallets 
			SET balance = balance + $1 
			WHERE id = $2
			RETURNING id, balance
		),
		inserted_outbox AS (
			INSERT INTO outbox_events (aggregate_id, event_type, payload)
			SELECT 
				id, 
				$4, 
				jsonb_build_object('wallet_id', id, 'amount_credited', $1::bigint, 'new_balance', balance)
			FROM updated_wallet
		)
		UPDATE idempotency_keys 
		SET response_balance = w.balance 
		FROM updated_wallet AS w
		WHERE key = $3
		RETURNING response_balance;
	`

	var newBalance int64

	// executing the query
	err = tx.QueryRow(ctx, businessQuery, amount, walletID, idempotencyKey, EventTypeCreditAccepted).Scan(&newBalance)
	if err != nil {
		return 0, fmt.Errorf("failed to execute business transaction: %v", err)
	}

	// committing the transaction (saving to disk)
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit failed: %v", err)
	}

	return newBalance, nil
}
