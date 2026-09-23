package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TODO: CDC Event Streaming
// The Transactional Outbox pattern paired with FOR UPDATE SKIP LOCKED ensures
// zero dual-write failures. Delivery is currently mocked to stdout.
// Ideally we would inject a message producer (e.g., segmentio/kafka-go) configured
// with RequiredAcks = All. If the network times out, the DB commit must fail,
// allowing the event to be retried on the next tick (At-Least-Once Delivery).

type OutboxWorker struct {
	db *pgxpool.Pool
}

func NewOutboxWorker(db *pgxpool.Pool) *OutboxWorker {
	return &OutboxWorker{db: db}
}

// Start initiates the polling daemon to process the outbox table asynchronously.
func (w *OutboxWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	log.Println("[CDC Worker] Started polling...")

	for {
		select {
		case <-ctx.Done():
			log.Println("[CDC Worker] Shutdown signal received. Halting...")
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *OutboxWorker) processBatch(ctx context.Context) {
	// 1. Open an explicit transaction to hold row locks during processing.
	tx, err := w.db.Begin(ctx)
	if err != nil {
		log.Printf("[CDC Worker] Failed to begin transaction: %v\n", err)
		return
	}
	defer tx.Rollback(ctx)

	// 2. Extract and lock rows without mutating them
	selectQuery := `
		SELECT id, aggregate_id, event_type, payload 
		FROM outbox_events 
		WHERE processed_at IS NULL 
		ORDER BY created_at ASC 
		LIMIT 50 
		FOR UPDATE SKIP LOCKED
	`

	rows, err := tx.Query(ctx, selectQuery)
	if err != nil {
		log.Printf("[CDC Worker] Failed to extract outbox events: %v\n", err)
		return
	}

	processedIDs := make([]int, 0, 50)

	for rows.Next() {
		var id, aggID int
		var evtType, payload string

		if err := rows.Scan(&id, &aggID, &evtType, &payload); err != nil {
			log.Printf("[CDC Worker] Row scan error: %v\n", err)
			continue
		}

		// TODO: replace mock with asynchronous segmentio/kafka-go publisher.
		fmt.Printf("[KAFKA MOCK] Dispatched %s for Aggregate %d: %s\n", evtType, aggID, payload)

		// Record ID only if the broker acknowledgment (real or mocked) is successful
		processedIDs = append(processedIDs, id)
	}
	rows.Close()

	// 3. Update only successfully dispatched messages
	if len(processedIDs) > 0 {
		updateQuery := `
			UPDATE outbox_events 
			SET processed_at = NOW() 
			WHERE id = ANY($1)
		`
		if _, err := tx.Exec(ctx, updateQuery, processedIDs); err != nil {
			log.Printf("[CDC Worker] Failed to update processed records: %v\n", err)
			return
		}
	}

	// 4. Flush process timestamps to disk and release locks for other workers
	if err := tx.Commit(ctx); err != nil {
		log.Printf("[CDC Worker] Failed to commit transaction: %v\n", err)
	}
}
