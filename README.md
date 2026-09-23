# Ledger in Golang (Go)

Cloud-native RESTful API optimized for distributed systems, ACID-ity, low-latency, and fault-tolerance.

## Architecture
*   **SOLID principles:** Dependency Inversion to decouple business logic from infrastructure and ensure testability.
*   **OCC:** Prevents double-spending and deadlocks without pessimistic table locks.
*   **Event-Driven Architecture:** Ensures dual-write safety and eventual consistency (wallet mutations and domain events commit atomically).
*   **CDC Worker:** Async polling for At-Least-Once event dispatch.
*   **Lock-Sharded GCRA Rate Limiter:** Implemented via `sync.Map` and per-IP Mutexes. Includes an eviction daemon to prevent OOM.
*   **Zero-Trust Security:** JWT signature validation middleware.

## Stack
*   **Language:** Golang (Go 1.22).
*   **Datastore:** PostgreSQL 15.
*   **Observability:** OpenTelemetry, RED Metrics, Structured JSON Logging, Prometheus & Grafana ready.
*   **Testing:** Testcontainers (isolated DB integration), K6 (load testing & benchmarking).
*   **DevOps & CI/CD:** Multi-stage Dockerfile, Kubernetes readiness, GitHub Actions CI, `golangci-lint`.

## Future Scalability
*   **Distributed Rate Limiting:** GCRA designed to scale via Redis for multi-pod synchronization.
*   **Idempotency Keys:** ready for header-based idempotency (via Redis).
