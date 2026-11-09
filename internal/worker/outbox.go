package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	slog.Info("[CDC Worker] Started dynamic polling...")

	for {
		select {
		case <-ctx.Done():
			slog.Info("[CDC Worker] Shutdown signal received. Halting...")
			return
		default:
			processed := w.processBatch(ctx)

			// backoff to prevent DB thrashing when empty
			if processed == 0 {
				time.Sleep(2 * time.Second)
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
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tx, err := w.db.Begin(txCtx)
	if err != nil {
		slog.ErrorContext(txCtx, "[CDC Worker] Failed to begin transaction", "error", err)
		return 0
	}
	defer tx.Rollback(txCtx)

	events := w.extractEvents(txCtx, tx)

	if len(events) == 0 {
		return 0
	}

	processedIDs := w.publishEvents(txCtx, events)

	if len(processedIDs) == 0 {
		return 0
	}

	_, err = tx.Exec(txCtx, `DELETE FROM outbox_events WHERE id = ANY($1)`, processedIDs)
	if err != nil {
		slog.ErrorContext(txCtx, "[CDC Worker] Failed to delete processed events", "error", err)
		return 0
	}

	if err := tx.Commit(txCtx); err != nil {
		slog.ErrorContext(txCtx, "[CDC Worker] DB commit failed", "error", err)
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
		FROM outbox_events
		FOR UPDATE SKIP LOCKED
		LIMIT 100;
	`
	rows, err := tx.Query(ctx, query)
	if err != nil {
		slog.ErrorContext(ctx, "[CDC Worker] Failed to extract events", "error", err)
		return []outboxEvent{}
	}

	events := make([]outboxEvent, 0, 100)
	for rows.Next() {
		var e outboxEvent
		if err := rows.Scan(&e.id, &e.aggregateID, &e.eventType, &e.payload); err != nil {
			slog.ErrorContext(ctx, "[CDC Worker] DB scan failed", "error", err)
			rows.Close()
			return []outboxEvent{}
		}
		events = append(events, e)
	}
	rows.Close()
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

			err := w.publisher.Publish(pubCtx, event.aggregateID, event.eventType, event.payload)

			// mutex prevents data races on shared slices across goroutines
			mu.Lock()
			if err != nil {
				slog.ErrorContext(ctx, "[CDC Worker] Publish failed, will retry later", "id", event.id, "error", err)
			} else {
				processedIDs = append(processedIDs, event.id)
			}
			mu.Unlock()
		}(e)
	}
	wg.Wait()

	return processedIDs
}
