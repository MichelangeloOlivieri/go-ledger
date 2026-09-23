High-throughput, ACID-compliant financial ledger in Go.

## Architecture & System Design
*   **OCC (Optimistic Concurrency Control):** Prevents double-spending and data-races in high-frequency transactions without pessimistic table locks.
*   **Transactional Outbox:** Ensures dual-write safety. Wallet mutations and domain events commit atomically to PostgreSQL.
*   **CDC Worker:** Async Go routine polling the outbox (`FOR UPDATE SKIP LOCKED`) for At-Least-Once event dispatch.
*   **Lock-Sharded GCRA Rate Limiter:** In-memory O(1) rate limiter. Uses `sync.Map` and per-IP Mutexes to prevent global bottlenecks, with a background eviction daemon to avoid OOM.
*   **Zero-Trust Security:** JWT signature validation middleware.

## Tech Stack
*   **Language:** Go 1.22
*   **Datastore:** PostgreSQL 15 (pgxpool)
*   **Observability:** OpenTelemetry (OTLP), RED Metrics, Structured JSON Logging (`log/slog`)
*   **Testing:** Testcontainers (Isolated DB integration), K6 (Load testing)
*   **Infrastructure:** Multi-stage Dockerfile (Scratch target), GitHub Actions (CI/Linting)

## Trade-offs & Future Scalability
*   **Event Streaming:** Replace polling CDC worker with Debezium (WAL tailing) for real-time Kafka integration.
*   **Distributed Rate Limiting:** Migrate in-memory GCRA to Redis + Lua scripts for multi-pod K8s synchronization.
*   **Idempotency Keys:** Implement header-based idempotency keys via Redis to safely handle client network retries.
