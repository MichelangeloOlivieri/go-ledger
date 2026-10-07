-- seeds 50 wallets to allow k6 virtual users concurrent load testing

INSERT INTO wallets (id, balance)
SELECT generate_series(1, 50), 1000
ON CONFLICT (id) DO NOTHING;

-- aligns postgres counter with manually inserted id
SELECT setval('wallets_id_seq', (SELECT MAX(id) FROM wallets));