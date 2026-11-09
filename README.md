# Financial Ledger API (Go)

A cloud-native REST API in Go simulating a high-throughput financial ledger. Designed to comply to industry-standard software engineering patterns, such as ACID-ity, data-race prevention, and separation of concerns.

## Architecture & Design Decisions

The system tackles typical distributed finance bottlenecks (double-spending, deadlocks, dual-writes) using standard patterns:

- **Concurrency & ACID:** Utilizes PostgreSQL pessimistic row-level locks. Enforces a strict **Lock Hierarchy** (always locking the lowest Account ID first) to avoid deadlocks during concurrent cyclic transfers. Database operations are made via `pgx.Batch` to minimize I/O chatter.
- **Double-Entry Bookkeeping:** Transfers generate delta records in an `entries` table, ensuring the ledger state is always verifiable.
- **Change Data Capture:** Transactional Outbox pattern: domain events are atomically committed to `outbox_events`. A background worker polls the outbox with `FOR UPDATE SKIP LOCKED`, allowing horizontal scaling of the worker pool without double-processing.
- **Idempotency:** Network anomalies and client retries are handled via `idempotency_keys` unique constraints, making duplicate API calls inherently safe at the database level.
- **Middleware Pipeline:** 
  1. **Observability:** Context-aware RED (Rate, Errors, Duration) metrics logging.
  2. **Zero-Trust Auth:** Sender identity is extracted from JWT claims.
  3. **Tenant-Isolation Rate Limiting:** Enforces quotas via `httprate` based on the authenticated user ID.

## Tech Stack

- **Language:** Go 1.26
- **Datastore:** PostgreSQL 15
- **Testing:** `testcontainers-go` (integration), `k6` (load testing)
- **CI/CD:** Multi-stage Scratch Dockerfile, GitHub Actions, `golangci-lint`

## Quick Start

### 1. Run the API

Start the PostgreSQL database and the API server
```bash
docker compose up -d postgres api
```

### 2. Development & Testing
The test suite utilizes Testcontainers to validate ACID properties against a real PostgreSQL instance.

```bash
# Run unit and integration tests (with race detector)
make test

# Run the linter
make lint

# Calculate code coverage
make coverage
```

### 3. Load & Stress Testing
Executes an automated E2E pipeline: tears down infrastructure, generates seeded JWTs, sets up the DB/API, injects seed data, and runs a high-concurrency k6 stress test to validate lock mechanics.

```bash
make stress
```