package worker

import (
	"context"
	"os"
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

// in-memory mock
type MockBus struct {
	mu     sync.Mutex
	events []string
}

func NewMockBus() *MockBus {
	return &MockBus{
		events: make([]string, 0),
	}
}

func (m *MockBus) Publish(ctx context.Context, aggregateID string, eventType string, payload string) error {
	// needs to be thread-safe for w.publishEvents() to work
	m.mu.Lock()
	defer m.mu.Unlock()

	m.events = append(m.events, payload)
	return nil
}

// returns a deep copy to mathematically prevent data races during test assertions
func (m *MockBus) GetEvents() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := make([]string, len(m.events))
	copy(cpy, m.events)

	return cpy
}

// validates standard workflow (processBatch): extract, isolate (IN_FLIGHT), publish, and delete
func TestOutboxWorker_Baseline(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE events CASCADE;")

	insertQuery := `
		INSERT INTO events (aggregate_id, event_type, payload) VALUES
			('agg-1', 'created', '{"data": "event 1"}'),
			('agg-2', 'updated', '{"data": "event 2"}'),
			('agg-3', 'deleted', '{"data": "event 3"}');
	`
	if _, err := globalPool.Exec(ctx, insertQuery); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	mockPublisher := NewMockBus()
	w := NewOutboxWorker(globalPool, mockPublisher)

	processedCount := w.processBatch(ctx)
	if processedCount != 3 {
		t.Fatalf("expected 3 processed events, got %d", processedCount)
	}

	events := mockPublisher.GetEvents()
	if len(events) != 3 {
		t.Fatalf("expected exactly 3 events published, got: %d", len(events))
	}

	expected := map[string]bool{
		`{"data": "event 1"}`: true,
		`{"data": "event 2"}`: true,
		`{"data": "event 3"}`: true,
	}

	for _, e := range events {
		if _, exists := expected[e]; !exists {
			t.Fatalf("unexpected or corrupted event payload received: %s", e)
		}
		delete(expected, e)
	}
	// only way to detect duplicates
	if len(expected) != 0 {
		t.Fatalf("events payload mismatch, missing expected events: %v", expected)
	}

	var remainingCount int
	if err := globalPool.QueryRow(ctx, `SELECT COUNT(*) FROM events;`).Scan(&remainingCount); err != nil {
		t.Fatalf("count query failed: %v", err)
	}

	if remainingCount != 0 {
		t.Fatalf("expected 0 remaining in events table, got %d", remainingCount)
	}
}

// validates absence of double processing or deadlocks in db reading
func TestOutboxWorker_ConcurrentSkipLocked(t *testing.T) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE events CASCADE;")

	insertQuery := `
		INSERT INTO events (aggregate_id, event_type, payload)
		SELECT floor(random() * 1000)::TEXT, 'event', '{"data": "mock"}'::jsonb
		FROM generate_series(1, 100);
	`
	if _, err := globalPool.Exec(ctx, insertQuery); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	mockPublisher := NewMockBus()
	w := NewOutboxWorker(globalPool, mockPublisher)
	var wg sync.WaitGroup

	// multiple simultaneous workers try to read the same batch
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.processBatch(ctx)
		}()
	}
	wg.Wait()

	events := mockPublisher.GetEvents()
	if len(events) != 100 {
		t.Fatalf("expected exactly 100 published events, got %d", len(events))
	}

	var remainingCount int
	if err := globalPool.QueryRow(ctx, `SELECT COUNT(*) FROM events;`).Scan(&remainingCount); err != nil {
		t.Fatalf("count query failed: %v", err)
	}

	if remainingCount != 0 {
		t.Fatalf("expected 0 remaining rows, got %d", remainingCount)
	}
}

// measures performance under heavy concurrent load
func BenchmarkOutboxWorker_SkipLocked(b *testing.B) {
	ctx := context.Background()
	_, _ = globalPool.Exec(ctx, "TRUNCATE TABLE events CASCADE;")

	insertQuery := `
		INSERT INTO events (aggregate_id, event_type, payload)
		SELECT floor(random() * 1000)::TEXT, 'benchmark', '{"data": "bench"}'::jsonb
		FROM generate_series(1, 100000);
	`
	if _, err := globalPool.Exec(ctx, insertQuery); err != nil {
		b.Fatalf("setup failed: %v", err)
	}

	publisher := NewMockBus()
	w := NewOutboxWorker(globalPool, publisher)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.processBatch(ctx)
		}
	})
}
