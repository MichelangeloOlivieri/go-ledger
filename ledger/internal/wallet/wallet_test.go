package wallet

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestWalletService_Credit_Concurrency(t *testing.T) {
	ctx := context.Background()

	// Spin up an ephemeral PostgreSQL container for isolated integration testing.
	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15-alpine"),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(10*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("Failed to start postgres container: %v", err)
	}
	defer pgContainer.Terminate(ctx)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}
	defer pool.Close()

	// Bootstrap schema and seed initial data.
	_, err = pool.Exec(ctx, `
		CREATE TABLE wallets (
			id SERIAL PRIMARY KEY,
			balance BIGINT NOT NULL,
			version INT NOT NULL DEFAULT 0
		);
		INSERT INTO wallets (id, balance, version) VALUES (1, 1000, 0);

		CREATE TABLE outbox_events (
			id SERIAL PRIMARY KEY,
			aggregate_id INT NOT NULL,
			event_type VARCHAR(50) NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			processed_at TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("Schema setup failed: %v", err)
	}

	// Configure concurrency stress test parameters.
	svc := NewWalletService(pool)
	var wg sync.WaitGroup

	workers := 50
	creditAmount := int64(10)

	var successCount int32
	var failCount int32

	// Spawn concurrent workers to trigger OCC conflicts.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Credit(ctx, creditAmount)
			if err != nil {
				atomic.AddInt32(&failCount, 1)
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// Assert OCC invariant: The final balance must perfectly match the sum of successful transactions.
	var finalBalance int64
	err = pool.QueryRow(ctx, "SELECT balance FROM wallets WHERE id = 1").Scan(&finalBalance)
	if err != nil {
		t.Fatalf("Failed to read final balance: %v", err)
	}

	expectedBalance := int64(1000) + (int64(successCount) * creditAmount)

	t.Logf("Total Requests: %d", workers)
	t.Logf("Successful: %d | Rejected (OCC Conflicts): %d", successCount, failCount)
	t.Logf("Expected Balance: %d | Actual Database Balance: %d", expectedBalance, finalBalance)

	if finalBalance != expectedBalance {
		t.Errorf("FATAL: DATA RACE DETECTED! Expected %d, got %d", expectedBalance, finalBalance)
	}
}
