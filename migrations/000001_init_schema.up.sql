-- setting up the tables in the database when the server is started

-- business domain table
CREATE TABLE IF NOT EXISTS wallets (
    id BIGSERIAL PRIMARY KEY,
    balance BIGINT NOT NULL,
    CONSTRAINT balance_non_negative CHECK (balance >= 0)
);

-- events table (effhemeral queue for CDC)
CREATE TABLE IF NOT EXISTS outbox_events (
    id SERIAL PRIMARY KEY,
    aggregate_id BIGINT NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(20) DEFAULT 'PENDING', 
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- enforces idempotency via unique primary key constraint and stores the response
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key VARCHAR(255) PRIMARY KEY,
    response_balance BIGINT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);