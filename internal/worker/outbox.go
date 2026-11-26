package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
)

type EventPublisher interface {
	Publish(ctx context.Context, aggregateID string, eventType string, payload string) error
}

type OutboxWorker struct {
	db        *pgxpool.Pool
	publisher EventPublisher
}

func NewOutboxWorker(db *pgxpool.Pool, pub EventPublisher) *OutboxWorker {
	return &OutboxWorker{
		db:        db,
		publisher: pub,
	}
}

func (w *OutboxWorker) Start(ctx context.Context) {
	slog.Info("[Polling Worker] Started dynamic polling...")

	for {
		select {
		case <-ctx.Done():
			slog.Info("[Polling Worker] Shutdown signal received. Halting...")
			return
		default:
			processed := w.processBatch(ctx)

			// backoff to prevent DB thrashing when empty
			if processed == 0 {
				select {
				case <-ctx.Done():
					slog.Info("[Polling Worker] Shutdown signal received. Halting...")
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}
}

type outboxEvent struct {
	id          int
	aggregateID string
	eventType   string
	payload     string
}

func (w *OutboxWorker) processBatch(ctx context.Context) int {
	tracer := otel.Tracer("outbox-worker")
	txCtx, span := tracer.Start(ctx, "ProcessOutboxBatch")
	defer span.End()

	// 1. Begin (10 seconds timeout)
	txCtx, cancel := context.WithTimeout(txCtx, 10*time.Second)
	defer cancel()

	tx, err := w.db.Begin(txCtx)
	if err != nil {
		slog.ErrorContext(txCtx, "[Polling Worker] Failed to begin transaction", "error", err)
		return 0
	}
	// fix: rollback throws error if txCtx is already dead (db still rolls back, but it is not a precise log)
	rbCtx, rbCancel := context.WithTimeout(context.WithoutCancel(txCtx), 2*time.Second)
	defer rbCancel()
	defer func() {
		if err := tx.Rollback(rbCtx); err != nil && err != pgx.ErrTxClosed {
			slog.ErrorContext(rbCtx, "[Polling Worker] Failed to rollback transaction", "error", err)
		}
	}()

	// 2. Extract
	events := w.extractEvents(txCtx, tx)

	if len(events) == 0 {
		return 0
	}

	// 3. Publish (at least once delivery)
	processedIDs := w.publishEvents(txCtx, events)

	if len(processedIDs) == 0 {
		return 0
	}

	// 4. Delete
	_, err = tx.Exec(txCtx, `DELETE FROM events WHERE id = ANY($1)`, processedIDs)
	if err != nil {
		slog.ErrorContext(txCtx, "[Polling Worker] Failed to delete processed events", "error", err)
		return 0
	}

	// 5. Commit
	if err := tx.Commit(txCtx); err != nil {
		slog.ErrorContext(txCtx, "[Polling Worker] DB commit failed", "error", err)
		return 0
	}

	return len(processedIDs)
}

// ================================================================================================
// HELPER FUNCTIONS
// ================================================================================================

func (w *OutboxWorker) extractEvents(ctx context.Context, tx pgx.Tx) []outboxEvent {
	query := `
		SELECT id, aggregate_id, event_type, payload
		FROM events
		ORDER BY id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 100;
	`
	rows, err := tx.Query(ctx, query)
	if err != nil {
		slog.ErrorContext(ctx, "[Polling Worker] Failed to extract events", "error", err)
		return []outboxEvent{}
	}
	defer rows.Close()

	events := make([]outboxEvent, 0, 100)

	for rows.Next() {
		var e outboxEvent
		if err := rows.Scan(&e.id, &e.aggregateID, &e.eventType, &e.payload); err != nil {
			slog.ErrorContext(ctx, "[Polling Worker] DB scan failed", "error", err)
			rows.Close()
			return []outboxEvent{}
		}

		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		slog.ErrorContext(ctx, "[Polling Worker] Row iteration failed", "error", err)
		return nil
	}

	return events
}

func (w *OutboxWorker) publishEvents(ctx context.Context, events []outboxEvent) []int {
	processedIDs := make([]int, 0, len(events))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, e := range events {
		wg.Add(1)
		go func(event outboxEvent) {
			defer wg.Done()

			// explicit timeout avoids goroutine leaks if publisher hangs
			pubCtx, cancelPub := context.WithTimeout(ctx, 5*time.Second)
			defer cancelPub()
			// NB: publisher needs to be thread-safe! It would be better to implementa a Lease/Claim pattern but for now we ignore it due to publisher simplicity (it is basically a mock)
			err := w.publisher.Publish(pubCtx, event.aggregateID, event.eventType, event.payload)

			// mutex prevents data races on shared slices across goroutines
			mu.Lock()
			if err != nil {
				slog.ErrorContext(ctx, "[Polling Worker] Publish failed, will retry later", "id", event.id, "error", err)
			} else {
				processedIDs = append(processedIDs, event.id)
			}
			mu.Unlock()
		}(e)
	}
	wg.Wait()

	return processedIDs
}
