# Order Processor

A production-grade Go microservice for order processing with **guaranteed event delivery** via Kafka. Built around the **Transactional Outbox Pattern** to solve the dual-write problem between PostgreSQL and Kafka — ensuring that no event is ever lost, even during partial failures.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Order Processor                             │
│                                                                     │
│  ┌──────────┐    ┌─────────┐    ┌────────┐    ┌──────────────────┐  │
│  │ HTTP API │───▶│ Service │───▶│ Domain │───▶│ PostgreSQL (pgx) │  │
│  │ (chi/v5) │    │  Layer  │    │ Models │    │ Order + Outbox   │  │
│  └──────────┘    └─────────┘    └────────┘    └────────┬─────────┘  │
│                                                        │            │
│  ┌──────────────┐    ┌────────────────┐    ┌───────────▼─────────┐  │
│  │    Kafka     │◀───│  Outbox Relay  │◀───│  outbox table       │  │
│  │   Producer   │    │  (polling)     │    │  (FOR UPDATE SKIP   │  │
│  │ (idempotent) │    └────────────────┘    │       LOCKED)       │  │
│  └──────┬───────┘                          └─────────────────────┘  │
│         │                                                           │
│  ┌──────▼───────┐    ┌────────────────┐    ┌─────────────────────┐  │
│  │    Kafka     │───▶│ Handler Chain  │───▶│   Saga Orchestrator │  │
│  │   Consumer   │    │ Poison Pill →  │    │   Payment + Stock   │  │
│  │ (per-part.)  │    │ Idempotency →  │    │   (circuit-broken)  │  │
│  └──────────────┘    │ Business Logic │    └─────────────────────┘  │
│                      └────────────────┘                             │
└─────────────────────────────────────────────────────────────────────┘
```

**Design:** DDD + Hexagonal (Ports & Adapters). Dependencies always point inward — handlers depend on services, services depend on interfaces, adapters implement interfaces.

## Highlights

### Transactional Outbox — Zero Lost Events

The classic dual-write problem: you need to save an order to the database **and** publish an event to Kafka. If either fails, the system becomes inconsistent. This service solves it:

1. A single PostgreSQL transaction writes both the `orders` row and an `outbox` message
2. An independent **Outbox Relay** polls unpublished messages (`SELECT ... FOR UPDATE SKIP LOCKED`) and publishes them to Kafka
3. Failed publishes are retried with backoff (up to 5 attempts), relay never crashes on transient errors
4. Result: **at-least-once delivery** with zero message loss

### Lock-Free Circuit Breaker

A custom circuit breaker (`pkg/breaker`) protects downstream service calls (payment, warehouse) from cascading failures:

- **Fully lock-free** — uses `atomic.Pointer[snapshot]` with CAS loops instead of mutexes, achieving high throughput under contention
- **State machine:** Closed → Open (on failure threshold) → HalfOpen (after cooldown) → Closed (on successful probes)
- **Single-probe in HalfOpen** — only one goroutine executes the real call, others get `ErrOpen` immediately (no thundering herd)
- **Slow call detection** — calls exceeding a configurable threshold count as failures even when they return `nil`
- **Panic-safe** — panics in the wrapped function are recorded as failures and re-propagated
- **Context-aware** — `context.Canceled` is neutral (caller gave up), `DeadlineExceeded` is treated as failure
- **29 unit tests** covering concurrency, state transitions, panic recovery, and full lifecycle

### Per-Partition Kafka Consumer

A custom consumer (`pkg/kafka/consumer.go`) that maximizes throughput while preserving ordering guarantees:

- **One goroutine per partition** — messages within a partition are processed sequentially (ordering guarantee), while different partitions run in parallel
- **Manual offset commits** — workers send processed offsets to a commit channel; the poll goroutine batches and deduplicates them (max offset per topic-partition)
- **Backpressure** — when a worker's channel is full, `dispatch()` blocks, which pauses `Poll()`, naturally slowing consumption
- **Deadlock-free shutdown** — `awaitWorkers()` drains the commit channel while waiting for workers to finish, preventing buffer-full deadlocks
- **Rebalance-safe** — revoked partitions trigger targeted worker shutdown with offset drain

### Handler Chain (Middleware Pattern)

Kafka message handling is composed as a decorator chain, cleanly separating cross-cutting concerns:

```
withPoisonPill → withIdempotency → business handler
```

- **Poison pill handler** — non-retriable errors go straight to DLQ; retriable errors are re-published with `x-retry-count` incremented; exhausted retries go to DLQ
- **Idempotency guard** — `topic:partition:offset` is inserted into `processed_events` inside a transaction; duplicates are silently skipped
- **DLQ enrichment** — dead-lettered messages carry `x-original-topic`, `x-original-partition`, `x-original-offset`, `x-error`, `x-failed-at` headers for forensics

### Saga Orchestrator

A synchronous saga (`pkg/saga`) coordinates multi-step operations (e.g., reserve payment → reserve stock) with automatic compensation:

- Steps execute sequentially; on failure, completed steps are compensated **in reverse order**
- **Panic-safe** — `recover()` in both `Execute` and `Compensate` prevents panics from breaking the saga
- **Context-safe** — compensation runs with `context.WithoutCancel(ctx)`, ensuring rollback completes even if the caller has timed out
- **`IsPoisoned()`** — signals that both the saga AND its compensation failed, requiring manual intervention
- **21 unit tests** covering happy path, compensation ordering, panic recovery, and context cancellation

### Context-Carried Transactions

A clean transaction pattern that avoids passing `pgx.Tx` through method signatures:

```go
// RunInTx stores the transaction in context
func (s *Storage) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
    tx, _ := s.pool.Begin(ctx)
    return fn(context.WithValue(ctx, txKey{}, tx))
}

// conn() transparently returns tx (if inside RunInTx) or pool (if outside)
func (s *Storage) conn(ctx context.Context) executor {
    if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
        return tx
    }
    return s.pool
}
```

Every storage method calls `s.conn(ctx)` — works both inside and outside transactions with no signature changes.

### Graceful Shutdown

Orchestrated via `errgroup` with proper resource ordering:

1. Signal received → context cancelled
2. Consumer and relay see cancellation → drain in-flight work → finish
3. HTTP server shuts down (30s timeout for in-flight requests)
4. `errgroup.Wait()` returns → Kafka producer closed → PostgreSQL pool closed
5. Resources are released **after** all goroutines finish — no use-after-close races

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.25 |
| HTTP Router | chi/v5 |
| Database | PostgreSQL 17 (pgx/v5 pool) |
| Messaging | Apache Kafka (confluent-kafka-go) |
| Migrations | Goose |
| Config | cleanenv (YAML + env override) |
| Logging | slog (structured JSON) |
| Concurrency | errgroup, atomic, CAS |
| Linting | golangci-lint v2 (strict) |
| Task Runner | Taskfile |

## Project Structure

```
cmd/app/                          Entry point
internal/
├── app/                          Bootstrap, dependency wiring, graceful shutdown
├── config/                       Configuration (YAML + env vars)
├── domain/
│   ├── model/                    Order, OrderItem, OutboxMessage, CreateOrderCommand
│   └── errors/                   RetriableError, NonRetriableError
├── service/order/                Business logic (create, get, process)
├── http/
│   ├── handlers/                 HTTP handlers (create order, get order)
│   ├── lib/api/                  JSON decode (strict), response helpers
│   └── middlewares/              Recovery middleware (panic → 500)
├── kafka/handler/                Handler chain (poison pill, idempotency, business logic)
└── storage/pg/                   PostgreSQL: pool, transactions, CRUD
pkg/
├── breaker/                      Lock-free circuit breaker (atomic.Pointer + CAS)
├── kafka/                        Kafka producer + per-partition consumer
├── outbox/                       Outbox relay (polling, publish, retry)
└── saga/                         Saga orchestrator (execute, compensate, panic-safe)
migrations/                       Goose SQL migrations
config/                           YAML configuration
```

## API

### Create Order

```
POST /api/v1/orders/
Content-Type: application/json

{
  "user_id": "user-123",
  "items": [
    { "product_id": "prod-1", "quantity": 2, "price": 1999 }
  ]
}
```

```json
201 Created
{ "order_id": "550e8400-...", "status": "PENDING", "message": "order created" }
```

### Get Order

```
GET /api/v1/orders/{id}
```

```json
200 OK
{
  "id": "550e8400-...",
  "user_id": "user-123",
  "items": [{ "product_id": "prod-1", "quantity": 2, "price": 1999 }],
  "amount": 3998,
  "status": "PENDING"
}
```

## Database Schema

```sql
-- Orders (amount in cents, JSONB items)
CREATE TABLE orders (
    id TEXT PRIMARY KEY, user_id TEXT NOT NULL,
    items JSONB NOT NULL, amount BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMPTZ DEFAULT now(), updated_at TIMESTAMPTZ DEFAULT now()
);

-- Transactional Outbox (partial index on unpublished rows)
CREATE TABLE outbox (
    id BIGSERIAL PRIMARY KEY, topic TEXT NOT NULL, key TEXT NOT NULL,
    event_type TEXT NOT NULL, payload BYTEA NOT NULL, headers JSONB DEFAULT '{}',
    published_at TIMESTAMPTZ, retry_count INT DEFAULT 0, last_error TEXT
);
CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- Idempotent consumer deduplication
CREATE TABLE processed_events (
    idempotency_key TEXT PRIMARY KEY, processed_at TIMESTAMPTZ DEFAULT now()
);
```

## Quick Start

```bash
# Install tools
task install:tools

# Start infrastructure (PostgreSQL + Kafka + Kafka UI)
docker compose up -d

# Apply migrations
task migrate:up

# Run the service
go run cmd/app/main.go
```

Kafka UI is available at `http://localhost:8088`.

## Configuration

The service loads configuration from `config/config.yaml` with environment variable overrides. A `.env` file is loaded automatically.

Key sections: `app`, `http`, `kafka`, `postgres`, `outbox`, `payment`, `warehouse`.

## License

MIT
