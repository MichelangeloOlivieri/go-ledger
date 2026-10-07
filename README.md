# Distributed Financial Ledger API (Go)

A cloud-native RESTful API simulating a financial ledger. Designed with a focus on LLD principles (SOLID, ACID, concurrency), low-latency, and fault-tolerance for highly scalable microservices environments.

## Design and Patterns

This project tackles common distributed systems challenges (double-spending, dual-write problem, rate limiting) using standard enterprise patterns:

* **Clean Architecture & Dependency Injection:** Strict separation of concerns (Hexagonal Architecture / DDD principles). Delivery layers (HTTP Handlers), Business Logic (Domain Services), and Persistence (Repositories) are fully decoupled via interfaces, enabling 100% mockability and enforcing SRP and DIP.
* **ACID Transactions & Concurrency Control:** Uses PostgreSQL row-level locks and atomic increments (`FOR UPDATE SKIP LOCKED`) to prevent double-spending and race conditions and minimize I/O chatter.
* **Transactional Outbox & Event-Driven Architecture:** An asynchronous Background Worker polls the outbox table to guarantee **At-Least-Once** dispatching to message brokers (e.g., Kafka/RabbitMQ), elegantly solving the Dual-Write Problem.
* **Self-Healing Systems (Reaper Daemon):** A background cronjob automatically detects and rescues "zombie" `IN_FLIGHT` messages caused by pod crashes or network timeouts, ensuring eventual consistency without manual intervention.
* **Idempotency:** Idempotency keys are enforced at the database level to handle network retries atomically and safely under adversarial conditions.
* **Lock-Sharded GCRA Rate Limiter:** Prevents resource exhaustion with lock-sharding by using a `sync.Map` with per-IP mutexes. Includes a background eviction daemon to remove inactive IPs and prevent OOM (Out-Of-Memory) errors.
* **Decorator Pattern (Middleware):** Cross-cutting concerns like Cryptographic Security (JWT signature validation) and Observability are implemented as HTTP middlewares to keep the core domain immaculate (OCP).
* **Observability & Telemetry:** Implements a context-aware JSON logging strategy and a middleware to intercept and record RED (Rate, Errors, Duration) metrics for seamless integration with Datadog/Grafana.

## Stack

* **Language:** Go 1.22 (leveraging Goroutines and Channels for high-throughput async processing)
* **Datastore:** PostgreSQL 15 (optimized for relational integrity and transactional safety)
* **Testing:** Testcontainers (isolated DB integration tests), k6 (load testing & stress testing), standard `testing` package (TDD & Race Detector).
* **DevOps & CI/CD:** Multi-stage Dockerfile (scratch image for minimal attack surface), GitHub Actions pipeline, `golangci-lint`, Makefile.

## Quick Start

The project is fully containerized and includes a `Makefile` for rapid testing.

```bash
# Run the test suite (includes data-race detection)
make test

# Build and run the Docker image
make docker-build

# Run the stress test (spawns a Postgres container, seeds data, and runs a k6 load test)
make stress
```
