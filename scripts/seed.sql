INSERT INTO accounts (id, balance)
SELECT generate_series(1, 50), 1000000000000
ON CONFLICT (id) DO NOTHING;

SELECT setval('accounts_id_seq', (SELECT MAX(id) FROM accounts));
