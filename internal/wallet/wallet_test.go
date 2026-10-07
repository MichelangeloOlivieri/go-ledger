package wallet

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var globalPool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
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
		panic(err)
	}
	defer func() { _ = pgContainer.Terminate(ctx) }()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(err)
	}

	mig, err := migrate.New("file://../../migrations", connStr)
	if err != nil {
		panic(err)
	}

	err = mig.Up()
	if err != nil && err != migrate.ErrNoChange {
		panic(err)
	}

	globalPool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		panic(err)
	}
	defer globalPool.Close()

	return m.Run()
}

// tests the atomic increment guaranteed by PostgreSQL row-level locks
func TestWalletService_Concurrency(t *testing.T) {
	ctx := context.Background()
	// cleaning tables for future tests (test idempotency)
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE wallets, idempotency_keys, outbox_events CASCADE;")

	insertQuery := `INSERT INTO wallets (id, balance) VALUES (1, 1000);`
	_, err := globalPool.Exec(ctx, insertQuery)
	if err != nil {
		t.Fatalf("schema setup failed: %v", err)
	}

	// concurrency stress test
	svc := NewWalletService(globalPool)
	var wg sync.WaitGroup
	workers := 50
	creditAmount := int64(10)
	var successCount int32
	var failCount int32

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// bypasses idempotency rejection by generating unique keys for each vu
			idemKey := fmt.Sprintf("stress-test-key-%d", workerID)

			_, err := svc.Credit(ctx, 1, creditAmount, idemKey)
			if err != nil {
				atomic.AddInt32(&failCount, 1)
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// verifying the atomic increment invariant: finalBalance = initialBalance + creditAmount * successCount
	var finalBalance int64
	err = globalPool.QueryRow(ctx, "SELECT balance FROM wallets WHERE id = 1").Scan(&finalBalance)
	if err != nil {
		t.Fatalf("failed to read final balance from 'wallets' table: %v", err)
	}

	expectedBalance := int64(1000) + (int64(successCount) * creditAmount)

	// outputting throughput and contention metrics for observability
	t.Logf("total requests: %d", workers)
	t.Logf("successful: %d | rejected: %d", successCount, failCount)
	t.Logf("expected balance: %d | actual balance: %d", expectedBalance, finalBalance)

	if finalBalance != expectedBalance {
		t.Errorf("datarace detected: expected %d, got %d", expectedBalance, finalBalance)
	}

	// checking that no request failed
	if failCount > 0 {
		t.Errorf("expected 0 failures with PostgreSQL atomic increments, got %d", failCount)
	}
}
