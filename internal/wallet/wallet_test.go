package wallet

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
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
	if err = mig.Up(); err != nil && err != migrate.ErrNoChange {
		panic(err)
	}

	globalPool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		panic(err)
	}
	defer globalPool.Close()

	return m.Run()
}

// validates under concurrency: concurrency (no data-races), Lock Hierarchy (no deadlocks) and check invariants from accounts and entry tables
func TestWalletService_ACID_Transfers(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE accounts, idempotency_keys, entries, outbox_events CASCADE;")

	for i := 1; i <= 5; i++ {
		_, err := globalPool.Exec(ctx, "INSERT INTO accounts (id, balance) VALUES ($1, 1000);", i)
		if err != nil {
			t.Fatalf("schema setup failed: %v", err)
		}
	}

	svc := NewWalletService(globalPool)
	var wg sync.WaitGroup
	workers := 2000
	errChan := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			from := rand.Intn(5) + 1
			to := rand.Intn(5) + 1
			if from == to {
				to = (to % 5) + 1
			}

			idemKey := fmt.Sprintf("tx-stress-%d", workerID)
			err := svc.Transfer(ctx, int64(from), int64(to), 1, idemKey)
			if err != nil {
				errChan <- err
			}
		}(i)
	}
	wg.Wait()
	close(errChan)

	// deadlock detection: check constraint errors (balance < 0) are expected, whereas deadlocks are architectural failures
	for err := range errChan {
		if errors.Is(err, ErrInsufficientFunds) {
			continue
		}
		if strings.Contains(strings.ToLower(err.Error()), "deadlock detected") {
			t.Fatalf("lock hierarchy failed: deadlock detected - %v", err)
		}
	}

	var totalSystemBalance int64
	if err := globalPool.QueryRow(ctx, "SELECT SUM(balance) FROM accounts;").Scan(&totalSystemBalance); err != nil || totalSystemBalance != 5000 {
		t.Fatalf("ACID violation (balances): expected 5000, got %d (err: %v)", totalSystemBalance, err)
	}

	var totalEntriesBalance int64
	if err := globalPool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM entries;").Scan(&totalEntriesBalance); err != nil || totalEntriesBalance != 0 {
		t.Fatalf("ACID violation (double-entry): expected ledger sum 0, got %d (err: %v)", totalEntriesBalance, err)
	}
}

// validates idempotency across: accounts, entries, outbox_events
func TestWalletService_Idempotency(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE accounts, idempotency_keys, entries, outbox_events CASCADE;")
	_, _ = globalPool.Exec(ctx, "INSERT INTO accounts (id, balance) VALUES (1, 1000), (2, 1000);")

	svc := NewWalletService(globalPool)
	var wg sync.WaitGroup
	idemKey := "same-idempotency-key"

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Transfer(ctx, 1, 2, 50, idemKey); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	var balance1, balance2 int64
	_ = globalPool.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = 1;").Scan(&balance1)
	_ = globalPool.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = 2;").Scan(&balance2)

	if balance1 != 950 || balance2 != 1050 {
		t.Fatalf("idempotency violation on accounts: first_balance=%d, second_balance=%d", balance1, balance2)
	}

	var entryCount int
	_ = globalPool.QueryRow(ctx, "SELECT COUNT(*) FROM entries WHERE idempotency_key = $1", idemKey).Scan(&entryCount)
	if entryCount != 2 {
		t.Fatalf("idempotency violation on ledger: expected 2 entries, got %d", entryCount)
	}

	var outboxCount int
	_ = globalPool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1", idemKey).Scan(&outboxCount)
	if outboxCount != 1 {
		t.Fatalf("idempotency violation on outbox: expected 1 event, got %d", outboxCount)
	}
}
