package wallet

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
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

// 1. Setup of the test infrastructure

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

// spins up a real ephemeral postgres container with testcontainers (enabling real PostgreSQL logic)
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

// 2. Testing

// validates: concurrency (no data-races), Lock Hierarchy (no deadlocks) and invariants from accounts and entry tables
func TestWalletService_ACID_Transfers(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE accounts, idempotency_keys, transfers, entries, events CASCADE;")

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

	for i := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			from := rand.Intn(5) + 1
			to := rand.Intn(5) + 1
			if from == to {
				to = (to % 5) + 1
			}

			idempotencyKey := fmt.Sprintf("tx-stress-%d", workerID)
			fromId := int64(from)
			toId := int64(to)
			err := svc.Transfer(ctx, fromId, toId, 50, idempotencyKey)
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

	// accounts
	var totalSystemBalance int64
	if err := globalPool.QueryRow(ctx, "SELECT SUM(balance) FROM accounts;").Scan(&totalSystemBalance); err != nil || totalSystemBalance != 5000 {
		t.Fatalf("ACID violation on accounts table: expected 5000, got %d with error %v", totalSystemBalance, err)
	}

	// entries
	var totalEntriesBalance int64
	if err := globalPool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM entries;").Scan(&totalEntriesBalance); err != nil || totalEntriesBalance != 0 {
		t.Fatalf("ACID violation on entry table: expected 0, got %d with error %v", totalEntriesBalance, err)
	}
}

// validates idempotency across: accounts, entries, events
func TestWalletService_Idempotency(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE accounts, idempotency_keys, transfers, entries, events CASCADE;")
	_, _ = globalPool.Exec(ctx, "INSERT INTO accounts (id, balance) VALUES (1, 1000), (2, 1000);")

	svc := NewWalletService(globalPool)
	var wg sync.WaitGroup
	idempotencyKey := "test_idempotecy_key"

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Transfer(ctx, 1, 2, 50, idempotencyKey); err != nil {
				t.Errorf("transfer error during idempotency test: %v", err)
			}
		}()
	}
	wg.Wait()

	// accounts
	var balance1, balance2 int64
	_ = globalPool.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = 1;").Scan(&balance1)
	_ = globalPool.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = 2;").Scan(&balance2)

	if balance1 != 950 || balance2 != 1050 {
		t.Fatalf("idempotency violation on accounts: first_balance=%d (expected 950), second_balance=%d (expected 1050)", balance1, balance2)
	}

	// entries
	var transferId int64
	_ = globalPool.QueryRow(ctx, "SELECT id FROM transfers;").Scan(&transferId)

	var entryCount int
	_ = globalPool.QueryRow(ctx, "SELECT COUNT(*) FROM entries WHERE transfer_id = $1", transferId).Scan(&entryCount)
	if entryCount != 2 {
		t.Fatalf("idempotency violation on ledger: expected 2 entries, got %d", entryCount)
	}

	// events
	var outboxCount int
	_ = globalPool.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE aggregate_id = $1", strconv.FormatInt(transferId, 10)).Scan(&outboxCount)
	if outboxCount != 1 {
		t.Fatalf("idempotency violation on outbox: expected 1 event, got %d", outboxCount)
	}
}
