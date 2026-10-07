package worker

import (
	"context"
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
		))
	if err != nil {
		panic(err)
	}
	defer func() { _ = pgContainer.Terminate(ctx) }()

	connString, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(err)
	}

	mig, err := migrate.New("file://../../migrations", connString)
	if err != nil {
		panic(err)
	}

	err = mig.Up()
	if err != nil && err != migrate.ErrNoChange {
		panic(err)
	}

	globalPool, err = pgxpool.New(ctx, connString)
	if err != nil {
		panic(err)
	}
	defer globalPool.Close()

	return m.Run()
}

type mockPublisher struct {
	publishedCount int32
}

func (m *mockPublisher) Publish(ctx context.Context, aggregateID int, eventType, payload string) error {
	atomic.AddInt32(&m.publishedCount, 1)
	return nil
}

// tests the base logic of the worker
func TestOutboxWorker_Math(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE outbox_events CASCADE;")

	insertQuery := `
		INSERT INTO outbox_events (aggregate_id, event_type, payload) VALUES
			(1, 'created', '{"data": "event 1"}'),
			(2, 'updated', '{"data": "event 2"}'),
			(3, 'deleted', '{"data": "event 3"}'),
			(4, 'created', '{"data": "event 4"}'),
			(5, 'updated', '{"data": "event 5"}');
	`
	_, err := globalPool.Exec(ctx, insertQuery)
	if err != nil {
		t.Fatalf("could not insert test data: %v\n", err)
	}

	pub := &mockPublisher{}
	w := NewOutboxWorker(globalPool, pub)
	processedCount := w.processBatch(ctx)

	// testing the function return value
	if processedCount != 5 {
		t.Fatalf("expected function to return 5 processed events, got %d\n", processedCount)
	}

	// validating the broker integration
	if pub.publishedCount != 5 {
		t.Fatalf("expected publisher to receive 5 events, got %d\n", pub.publishedCount)
	}

	// testing the database state
	var remainingCount int
	countQuery := `SELECT COUNT(*) FROM outbox_events;`

	err = globalPool.QueryRow(ctx, countQuery).Scan(&remainingCount)
	if err != nil {
		t.Fatalf("could not count remaining events: %v\n", err)
	}

	if remainingCount != 0 {
		t.Fatalf("expected 0 remaining events (hard delete), got %d\n", remainingCount)
	}
}

// tests isolation of each worker
func TestOutboxWorker_Concurrency(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE outbox_events CASCADE;")

	insertQuery := `
		INSERT INTO outbox_events (aggregate_id, event_type, payload)
		SELECT floor(random() * 1000)::BIGINT,
			   (ARRAY['created', 'updated', 'deleted'])[floor(random() * 3 + 1)::INT],
			   '{"data": "mock"}'::jsonb
		FROM generate_series(1, 100);
	`
	_, err := globalPool.Exec(ctx, insertQuery)
	if err != nil {
		t.Fatalf("could not set up concurrency data: %v\n", err)
	}

	pub := &mockPublisher{}
	w := NewOutboxWorker(globalPool, pub)
	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.processBatch(ctx)
		}()
	}
	wg.Wait()

	// validates that the broker received exactly 100 messages across all threads
	if pub.publishedCount != 100 {
		t.Fatalf("concurrency failure: expected 100 published events, got %d", pub.publishedCount)
	}

	var remainingCount int
	// validates that all rows have been successfully processed and deleted
	countQuery := `SELECT COUNT(*) FROM outbox_events;`

	err = globalPool.QueryRow(ctx, countQuery).Scan(&remainingCount)
	if err != nil {
		t.Fatalf("could not execute count query: %v\n", err)
	}

	if remainingCount != 0 {
		t.Fatalf("concurrency failure: expected 0 remaining rows, got %d remaining", remainingCount)
	}
}

// tests lock contention and latency under heavy concurrent load
func BenchmarkOutboxWorker_SkipLocked(b *testing.B) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE outbox_events CASCADE;")

	insertQuery := `
		INSERT INTO outbox_events (aggregate_id, event_type, payload)
		SELECT floor(random() * 1000)::BIGINT,
			   'benchmark_event',
			   '{"data": "benchmark"}'::jsonb
		FROM generate_series(1, 100000);
	`
	_, err := globalPool.Exec(ctx, insertQuery)
	if err != nil {
		b.Fatalf("could not set up benchmark data: %v\n", err)
	}

	pub := &mockPublisher{}
	w := NewOutboxWorker(globalPool, pub)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.processBatch(ctx)
		}
	})
}

// tests the background reaper logic for zombie messages
func TestOutboxWorker_Reaper(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE outbox_events CASCADE;")

	// simulating a worker crash by inserting a stale IN_FLIGHT record and a fresh one
	insertQuery := `
		INSERT INTO outbox_events (aggregate_id, event_type, payload, status, created_at) VALUES
			(1, 'created', '{"data": "zombie"}', 'IN_FLIGHT', NOW() - INTERVAL '10 minutes'),
			(2, 'updated', '{"data": "fresh"}', 'IN_FLIGHT', NOW());
	`
	_, err := globalPool.Exec(ctx, insertQuery)
	if err != nil {
		t.Fatalf("could not insert reaper test data: %v\n", err)
	}

	pub := &mockPublisher{}
	w := NewOutboxWorker(globalPool, pub)

	// triggering the reaper manually bypassing the 5 minutes ticker
	w.reapStaleEvents(ctx)

	var pendingCount int
	err = globalPool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE status = 'PENDING'").Scan(&pendingCount)
	if err != nil {
		t.Fatalf("could not count pending events: %v\n", err)
	}

	// only the zombie record should have been resurrected
	if pendingCount != 1 {
		t.Fatalf("expected 1 zombie record to be rescued, got %d", pendingCount)
	}
}
