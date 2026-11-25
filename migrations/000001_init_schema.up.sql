CREATE TABLE accounts (
    id BIGSERIAL PRIMARY KEY,
    balance BIGINT NOT NULL,
    CONSTRAINT balance_non_negative CHECK (balance >= 0)
);

CREATE TABLE idempotency_keys (
    from_id BIGINT NOT NULL,
    to_id BIGINT NOT NULL,
    amount BIGINT NOT NULL,
    key TEXT NOT NULL,
    PRIMARY KEY (from_id, key),
    CONSTRAINT idempotency_key_length CHECK (length(key) BETWEEN 1 AND 128)
);

CREATE TABLE transfers (
    id BIGSERIAL PRIMARY KEY
);

CREATE TABLE entries (
    id BIGSERIAL PRIMARY KEY,
    transfer_id BIGINT NOT NULL REFERENCES transfers(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    amount BIGINT NOT NULL,
    CONSTRAINT amount_non_zero CHECK (amount <> 0)
);

CREATE INDEX idx_entries_transfer_id ON entries(transfer_id);
CREATE INDEX idx_entries_account_id ON entries(account_id);

CREATE TABLE events (
    id BIGSERIAL PRIMARY KEY,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL
);