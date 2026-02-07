-- +goose Up
-- +goose StatementBegin
-- Orders table.
CREATE TABLE IF NOT EXISTS orders
(
    id         TEXT PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    items      JSONB       NOT NULL DEFAULT '[]',
    amount     BIGINT      NOT NULL, -- cents
    status     TEXT        NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_user_id ON orders (user_id);
CREATE INDEX idx_orders_status ON orders (status);

-- Outbox table (transactional outbox pattern).
CREATE TABLE IF NOT EXISTS outbox
(
    id           BIGSERIAL PRIMARY KEY,
    topic        TEXT        NOT NULL,
    key          TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    payload      BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    retry_count  INT         NOT NULL DEFAULT 0,
    last_error   TEXT
);

-- Partial index: only covers unpublished rows → stays small.
CREATE INDEX idx_outbox_unpublished
    ON outbox (created_at)
    WHERE published_at IS NULL;

-- Processed events table (idempotent consumer).
CREATE TABLE IF NOT EXISTS processed_events
(
    idempotency_key TEXT PRIMARY KEY,
    processed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_processed_events_cleanup
    ON processed_events (processed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS orders;
-- +goose StatementEnd
