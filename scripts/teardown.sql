-- truncates all tables involved in the stress test

TRUNCATE TABLE wallets, outbox_events, idempotency_keys CASCADE;