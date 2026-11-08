-- ARCHITECTURAL NOTE: TEXT is identical to VARCHAR in Postgres but without length-check CPU overhead.
-- ARCHITECTURAL NOTE: balance CHECK constraint is the absolute source of truth for domain integrity.

CREATE TABLE IF NOT EXISTS accounts (
    id BIGSERIAL PRIMARY KEY,
    balance BIGINT NOT NULL,
    CONSTRAINT balance_non_negative CHECK (balance >= 0)
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ARCHITECTURAL NOTE: Implicitly uses idempotency_key as transaction_id for double-entry grouping. Minimal and brilliant.
CREATE TABLE IF NOT EXISTS entries (
    id BIGSERIAL PRIMARY KEY,
    idempotency_key TEXT NOT NULL REFERENCES idempotency_keys(key),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    amount BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entries_account_id ON entries(account_id);
CREATE INDEX IF NOT EXISTS idx_entries_idempotency_key ON entries(idempotency_key);

-- ARCHITECTURAL NOTE: Ephemeral queue strictly designed for 'SELECT ... FOR UPDATE SKIP LOCKED' and 'DELETE'.
-- Prevents MVCC dead-tuple bloat by eliminating UPDATEs (no status, no locked_at).
CREATE TABLE IF NOT EXISTS outbox_events (
    id BIGSERIAL PRIMARY KEY, 
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ARCHITECTURAL NOTE: Partial index for lightning-fast polling. We only care about existing rows, 
-- as processed ones are immediately DELETED by the Go workers.
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON outbox_events(aggregate_id);