CREATE TABLE IF NOT EXISTS wallets (
    id SERIAL PRIMARY KEY,
    balance BIGINT NOT NULL,
    version INT NOT NULL DEFAULT 0
);

INSERT INTO wallets (id, balance, version) VALUES (1, 1000, 0) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS outbox_events (
    id SERIAL PRIMARY KEY,
    aggregate_id INT NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    processed_at TIMESTAMP
);