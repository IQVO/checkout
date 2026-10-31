---
id: entity-relationship
title: Entity Relationship
sidebar_label: Entity Relationship
description: "The Postgres schema of Checkout as built: every table of its migration on develop, with keys, the design-time tables each one replaces, and the outbox, inbox and idempotency tables."
---

# Entity Relationship

The schema **as built**: [`0001_checkout_schema.up.sql`](https://github.com/IQVO/checkout/blob/develop/internal/adapters/outbound/postgres/migrations/0001_checkout_schema.up.sql) on `develop` (`ab4f0e9`), applied at boot with golang-migrate (which adds its own `schema_migrations` table). Checkout owns its database and shares no table with another context.

3 tables. Key markers: PK primary key, FK foreign key, UK unique. Types are the column types of the migration; constraints (CHECK, defaults, partial indexes) are in the file.

```mermaid
erDiagram
    checkout_sessions {
        text session_id PK
        text state
        text cart_id
        bigint cart_version
        text shopper_id
        text ship_to_address_id
        jsonb lines
        jsonb groups
        text price_quote_id
        text currency
        bigint total_minor
        timestamptz quote_expires_at
        text authorization_id
        text order_id
        jsonb requoted_offers
        text failure_code
        text decline_reason
        integer auth_attempt
        text pending_token
        timestamptz authorizing_since
        bytea placement_body
        text placement_hash
        timestamptz placing_since
        integer placement_attempts
        timestamptz next_attempt_at
        timestamptz alerted_at
        text void_authorization_id
        timestamptz terminal_at
        timestamptz created_at
        timestamptz updated_at
        bigint version
    }
    idempotency_keys {
        text route PK
        text key PK
        text request_hash
        integer status
        text content_type
        bytea body
        timestamptz created_at
    }
    outbox_events {
        bigserial id PK
        text event_id UK
        text topic
        text event_type
        text subject
        bytea key
        text dataschema
        bytea value
        jsonb headers
        timestamptz created_at
        timestamptz published_at
        integer attempts
        text last_error
    }
```

## Tables

| Table | Columns | Primary key | Role |
| --- | --- | --- | --- |
| `checkout_sessions` | 31 | `session_id` | aggregate or read-model state |
| `idempotency_keys` | 7 | `route, key` | stored REST answers per `Idempotency-Key` and route |
| `outbox_events` | 13 | `id` | transactional outbox the relay publishes to Kafka |

## Design-time schema versus as built

The pack was first written with a proposed, fully normalised schema. The service kept the aggregate boundaries and changed the storage shape:

| Design-time table | As built |
| --- | --- |
| `session_lines` | `checkout_sessions.lines` (jsonb column) |
| `session_groups` | `checkout_sessions.groups` (jsonb column) |
| `session_requoted_offers` | `checkout_sessions.requoted_offers` (jsonb column) |

Every other design-time table exists under the same name. The invariants are enforced by the aggregate in `internal/domain` before a row is written; the migration adds CHECK constraints on scalar columns and the indexes listed in the file.