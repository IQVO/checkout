-- REST API guideline 2.3: the Idempotency-Key is global to the service, not
-- scoped to a route. The stored fingerprint (method + path + body hash)
-- tells a replay from a reuse, so the key alone is the primary key and the
-- route column goes. A key stored under two routes keeps its newest row.
DELETE FROM idempotency_keys a
    USING idempotency_keys b
    WHERE a.key = b.key
      AND (a.created_at, a.route) < (b.created_at, b.route);
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys DROP COLUMN route;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (key);
