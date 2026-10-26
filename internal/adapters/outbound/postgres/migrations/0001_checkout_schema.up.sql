-- checkout schema (ADR 0002). A checkout session holds no PII and no card
-- data: shopper and shipping address are opaque references, the stored
-- placement request is PII-free, the payment method is a PSP token that is
-- kept only while the session is AUTHORIZING.

CREATE TABLE checkout_sessions (
    session_id          text        PRIMARY KEY,
    state               text        NOT NULL CHECK (state IN ('OPEN','AUTHORIZING','DECLINED','PLACING','REQUOTE_REQUIRED','COMPLETED','EXPIRED','FAILED')),
    cart_id             text        NOT NULL,
    cart_version        bigint      NOT NULL CHECK (cart_version >= 1),
    shopper_id          text        NOT NULL,
    ship_to_address_id  text        NOT NULL,
    lines               jsonb       NOT NULL,
    groups              jsonb       NOT NULL,
    price_quote_id      text        NOT NULL,
    currency            text        NOT NULL CHECK (char_length(currency) = 3),
    total_minor         bigint      NOT NULL CHECK (total_minor >= 0),
    quote_expires_at    timestamptz NOT NULL,
    authorization_id    text,
    order_id            text,
    requoted_offers     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    failure_code        text,
    decline_reason      text,
    auth_attempt        integer     NOT NULL DEFAULT 0 CHECK (auth_attempt >= 0),
    pending_token       text,
    authorizing_since   timestamptz,
    placement_body      bytea,
    placement_hash      text,
    placing_since       timestamptz,
    placement_attempts  integer     NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz,
    alerted_at          timestamptz,
    void_authorization_id text,
    terminal_at         timestamptz,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    version             bigint      NOT NULL CHECK (version >= 1)
);
CREATE INDEX checkout_sessions_authorizing_idx ON checkout_sessions (authorizing_since) WHERE state = 'AUTHORIZING';
CREATE INDEX checkout_sessions_placing_idx ON checkout_sessions (next_attempt_at) WHERE state = 'PLACING';
CREATE INDEX checkout_sessions_expiry_idx ON checkout_sessions (quote_expires_at) WHERE state IN ('OPEN','DECLINED');
CREATE INDEX checkout_sessions_void_idx ON checkout_sessions (updated_at) WHERE void_authorization_id IS NOT NULL;
CREATE INDEX checkout_sessions_purge_idx ON checkout_sessions (terminal_at) WHERE placement_body IS NOT NULL AND terminal_at IS NOT NULL;

-- Infrastructure tables: idempotency keys and the transactional outbox.
CREATE TABLE idempotency_keys (
    route        text        NOT NULL,
    key          text        NOT NULL,
    request_hash text        NOT NULL,
    status       integer     NOT NULL,
    content_type text        NOT NULL,
    body         bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (route, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

CREATE TABLE outbox_events (
    id           bigserial   PRIMARY KEY,
    event_id     text        NOT NULL UNIQUE,
    topic        text        NOT NULL,
    event_type   text        NOT NULL,
    subject      text        NOT NULL,
    key          bytea       NOT NULL,
    dataschema   text        NOT NULL,
    value        bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);
CREATE INDEX outbox_events_unpublished_idx ON outbox_events (id) WHERE published_at IS NULL;
