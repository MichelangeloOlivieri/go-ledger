# Financial Ledger API (Go)

A cloud-native RESTful API in Go simulating a financial ledger. Designed with a focus on LLD principles (SOLID, ACID, concurrency), low-latency, and fault-tolerance.

## Design and Patterns

This project tackles common distributed systems challenges (double-spending, dual-write problem, rate limiting) using standard patterns:

* **Clean Architecture & Dependency Injection:** separation of concerns (Hexagonal Architecture / DDD principles). Delivery layers (HTTP Handlers), business logic (Domain Services), and persistence (Repositories) are fully decoupled via interfaces, enabling 100% mockability and enforcing SRP and DIP.
* **ACID Transactions & Concurrency Control:** uses PostgreSQL row-level locks and atomic increments to prevent double-spending and race conditions and minimize I/O chatter.
* **Transactional Outbox & Event-Driven Architecture:** an asynchronous Background Worker polls the outbox table to guarantee **At-Least-Once** dispatching to message brokers (e.g., Kafka/RabbitMQ, to be added), tackling the Dual-Write Problem.
* **Self-Healing Mechanics:** other background workers ensure system resilience automatically (e.g. a cronjob rescues stuck messages, and a background eviction worker clears inactive IPs from the rate limiter).
* **Idempotency:** idempotency keys are enforced at the database level to handle network retries atomically.
* **Lock-Sharded GCRA Rate Limiter:** prevents resource exhaustion by using a `sync.Map` with per-IP mutexes (lock-sharding).
* **Decorator Pattern:** Functionalities like cryptographic security  and observability are implemented as HTTP middlewares to keep the core domain intact.
* **Observability & Telemetry:** implements a context-aware JSON logging strategy and a middleware to intercept and record RED (Rate, Errors, Duration) metrics (for future integration with Datadog/Grafana).

## Stack

* **Language:** Go 1.22
* **Datastore:** PostgreSQL 15
* **Testing:** Testcontainers, k6, standard `testing` package (TDD & Race Detector).
* **DevOps & CI/CD:** Multi-stage Dockerfile, `golangci-lint`, Makefile.

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
