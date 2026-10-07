package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type EventPublisher interface {
	Publish(ctx context.Context, aggregateID int, eventType, payload string) error
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

// initiates the polling daemon to process the outbox table asynchronously
func (w *OutboxWorker) Start(ctx context.Context) {
	slog.Info("[CDC Worker] Started dynamic polling...")

	// launches the background reaper cronjob concurrently
	go w.reaperDaemon(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("[CDC Worker] Shutdown signal received. Halting...")
			return
		default:
			processed := w.processBatch(ctx)

			if processed == 0 {
				time.Sleep(2 * time.Second)
			}
		}
	}
}

// background cronjob that rescues zombie IN_FLIGHT messages
func (w *OutboxWorker) reaperDaemon(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reapStaleEvents(ctx)
		}
	}
}

// resets records stuck in IN_FLIGHT state due to worker crashes
func (w *OutboxWorker) reapStaleEvents(ctx context.Context) {
	reapQuery := `
		UPDATE outbox_events 
		SET status = 'PENDING' 
		WHERE status = 'IN_FLIGHT' 
		AND created_at < NOW() - INTERVAL '5 minutes'
	`
	_, err := w.db.Exec(ctx, reapQuery)

	if err != nil {
		slog.ErrorContext(ctx, "[CDC Worker] Reaper failed to rescue zombie records", "error", err)
	}
}

func (w *OutboxWorker) processBatch(ctx context.Context) int {
	extractQuery := `
		UPDATE outbox_events 
		SET status = 'IN_FLIGHT' 
		WHERE id IN (
			SELECT id FROM outbox_events 
			WHERE status = 'PENDING' 
			FOR UPDATE SKIP LOCKED 
			LIMIT 100
		)
		RETURNING id, aggregate_id, event_type, payload;
	`

	// extracting rows
	rows, err := w.db.Query(ctx, extractQuery)
	if err != nil {
		slog.ErrorContext(ctx, "[CDC Worker] Failed to extract outbox events", "error", err)
		return 0
	}

	type outboxEvent struct {
		id          int
		aggregateID int
		eventType   string
		payload     string
	}
	var events []outboxEvent
	var malformedIDs []int

	// reading from extracted rows
	for rows.Next() {
		var e outboxEvent
		err := rows.Scan(&e.id, &e.aggregateID, &e.eventType, &e.payload)

		if err != nil {
			slog.ErrorContext(ctx, "[CDC Worker] Malformed data detected", "error", err)
			malformedIDs = append(malformedIDs, e.id)
			continue
		}

		events = append(events, e)
	}
	// closure to free the db socket before network I/O starts
	rows.Close()

	if len(events) == 0 && len(malformedIDs) == 0 {
		return 0
	}

	var processedIDs []int
	var failedIDs []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	// avoids reading I/O sequentially
	for _, e := range events {

		wg.Add(1)
		go func(event outboxEvent) {
			defer wg.Done()

			err := w.publisher.Publish(ctx, event.aggregateID, event.eventType, event.payload)
			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				slog.ErrorContext(ctx, "[CDC Worker] Failed to publish event", "error", err)
				failedIDs = append(failedIDs, event.id)
			} else {
				processedIDs = append(processedIDs, event.id)
			}
		}(e)
	}

	wg.Wait()
	processedIDs = append(processedIDs, malformedIDs...)

	// cleanup successfully dispatched messages
	if len(processedIDs) > 0 {
		deleteQuery := `
			DELETE 
			FROM outbox_events 
			WHERE id = ANY($1)
		`
		_, err := w.db.Exec(ctx, deleteQuery, processedIDs)
		if err != nil {
			slog.ErrorContext(ctx, "[CDC Worker] Failed to delete processed records", "error", err)
		}
	}

	// revert failed messages back for the next tick
	if len(failedIDs) > 0 {
		revertQuery := `
			UPDATE outbox_events 
			SET status = 'PENDING' 
			WHERE id = ANY($1)
		`
		_, err := w.db.Exec(ctx, revertQuery, failedIDs)
		if err != nil {
			slog.ErrorContext(ctx, "[CDC Worker] Failed to revert failed records", "error", err)
		}
	}

	return len(processedIDs)
}
