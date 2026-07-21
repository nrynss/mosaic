# Kafka / Redpanda introduction guide

This runbook describes how Mosaic keeps a **pluggable event-spine transport** so
a real log (Kafka or Redpanda) can replace the Postgres log implementation
later **without changing producers or consumers**. It matches the locked A1
interfaces in [`internal/eventlog`](../../internal/eventlog) and the Postgres
backend in [`internal/pgstore`](../../internal/pgstore).

## 1. Three layers (only the transport is swapped)

| Layer | Responsibility | Today | Later (scale) |
|-------|----------------|-------|---------------|
| **Log (transport)** | Append + ordered consume | Postgres `event_log` + consumer group | Kafka / Redpanda topic + consumer group |
| **System of record / read model** | Immutable provenance + materialized COP | **Postgres forever** | Postgres (unchanged) |
| **Fan-out** | Best-effort “read model changed” hints | Postgres `LISTEN/NOTIFY` | Redis / NATS / compacted topic |

Introducing Kafka replaces **only the transport layer**. Postgres remains the
queryable system of record and COP materialization. That is ordinary CQRS:
append to a log, project into relational tables, read from those tables.

## 2. Real A1 interfaces (`internal/eventlog`)

Producers and consumers depend on these seams only. Nothing in application code
imports Kafka or speaks SQL for the spine.

### EventLog (append side)

```go
// internal/eventlog/eventlog.go
type EventLog interface {
    // Append is at-least-once safe: re-appending an already-seen
    // IdempotencyKey is a no-op that returns nil (first-wins payload).
    Append(ctx context.Context, e EventEnvelope) error
}

type EventEnvelope struct {
    PartitionKey   string // routing + per-key ordering (e.g. incident id)
    IdempotencyKey string // source dedup token
    Type           string // application label, opaque to transport
    Payload        []byte // opaque body; transport never interprets it
}
```

### EventConsumer (read side)

```go
type EventConsumer interface {
    // Run delivers events ordered per PartitionKey, at-least-once.
    // handle nil  => ack / advance position
    // handle err  => no ack; event will be redelivered
    Run(ctx context.Context, handle func(context.Context, Event) error) error
}

type Event struct {
    EventEnvelope
    Position  Position  // opaque per-partition cursor
    Sequence  uint64    // diagnostic only; not a global offset
    Timestamp time.Time // descriptive metadata only
}
```

### EventBus (fan-out, not the durable log)

```go
type EventBus interface {
    Publish(ctx context.Context, topic string, note []byte) error
    // Subscribe: implementation closes the channel when ctx is cancelled.
    // Best-effort; bounded buffer; slow readers drop notes.
    Subscribe(ctx context.Context, topic string) (<-chan []byte, error)
}
```

### Position

`Position` is an opaque per-partition cursor (`PartitionKey()` + `Token()`).
Callers **must not** do arithmetic on tokens. The zero position means “before
the first event.” Equality is only meaningful within the same partition.

### Delivery contract (weakest common semantics)

Every backend must honor **only**:

- **At-least-once**, never exactly-once.
- **Ordered per partition key**, never globally ordered.

Mosaic’s source **IdempotencyKey** turns at-least-once into effectively-once at
the projection boundary. Do not claim exactly-once delivery from the transport.

## 3. Postgres implementation today (`internal/pgstore`)

| Seam | Type | Behaviour |
|------|------|-----------|
| `EventLog` | `*pgstore.Store` | `Append` → `INSERT` into `event_log`; unique on `idempotency_key` → first-wins no-op |
| `EventConsumer` | `pgstore.EventConsumer` | Per-partition advisory locks; process in sequence; checkpoint in same TX as handle |
| `EventBus` | `pgstore.EventBus` | `LISTEN/NOTIFY` via dedicated connections; bounded drop-new buffers |

Composition constructs these next to the durable record store:

```go
pg, err := pgstore.Open(ctx, dsn)
// ...
log := eventlog.EventLog(pg)                    // Store.Append
consumer := pgstore.NewEventConsumer(pg, pgstore.ConsumerConfig{})
bus := pgstore.NewEventBus(pg.Pool())
```

## 4. Wiring swap at composition only

Domain services, the simulation, and projectors take `eventlog.EventLog` /
`EventConsumer` / `EventBus`. They do **not** change when the transport changes.
Only the composition root (e.g. `cmd/mosaicdemo`) swaps constructors.

**Before (Postgres log):**

```go
pg, _ := pgstore.Open(ctx, dsn)
var log eventlog.EventLog = pg
consumer := pgstore.NewEventConsumer(pg, pgstore.ConsumerConfig{
    ConsumerGroup: "mosaic-projector",
})
bus := pgstore.NewEventBus(pg.Pool())

// producers (including simulation) call log.Append
// projector worker: consumer.Run(ctx, handle)
// SSE gateways: bus.Subscribe(ctx, "cop.updated")
```

**After (Kafka/Redpanda log — illustrative):**

```go
pg, _ := pgstore.Open(ctx, dsn) // still system of record + COP
klog := kafkalog.NewEventLog(client, "mosaic-events")           // future package
kconsumer := kafkalog.NewEventConsumer(client, "mosaic-events", "mosaic-projector")
// Fan-out may stay Postgres LISTEN/NOTIFY, or move to Redis/NATS:
bus := pgstore.NewEventBus(pg.Pool())

// Same producer / consumer call sites — only the concrete types differ.
var log eventlog.EventLog = klog
// consumer.Run(ctx, handle) unchanged signature
```

There is no `pglog` package today; the Postgres log is `pgstore`. A future
Kafka adapter is a new package that implements the same three interfaces.

## 5. Atomic project + position (never append + project)

The portability rule:

> The projector **consumes from the log interface** and commits
> **(projection update + position advance)** atomically —
> **never (append + project)**.

Why: Postgres *could* append and project in one ACID transaction; Kafka cannot.
Both backends can commit “I updated the read model **and** advanced my
consuming position.” Encoding append+project into the product path would nail
the spine to Postgres.

On the Postgres consumer path, `EventConsumer.Run` invokes `handle` inside
`Store.WithinTransaction`. A nil return UPSERTs the consumer checkpoint in the
same transaction. On error the transaction rolls back and the event is
redelivered (at-least-once). Handlers that also materialize the COP (via
`pgstore.MaterializingProjector`) should save the read model on that same TX
context so project+position+materialize stay atomic.

## 6. Conformance gate for new backends (E2)

Any new transport implementation **must** pass:

```go
// internal/eventlog/eventlogtest
eventlogtest.RunConformanceTests(t, func() (eventlog.EventLog, eventlog.EventConsumer, eventlog.EventBus, func()) {
    // construct an isolated backend instance
    return log, consumer, bus, cleanup
})
```

The suite covers:

- Append + consume payload integrity
- Idempotent append (including **first-wins** when a retry carries a different payload)
- Per-partition-key ordering
- At-least-once redelivery after handler error
- Position token integrity
- Multi-worker same consumer group: different partitions, no double-processing
- EventBus: Publish/Subscribe, cancel closes channel, topic isolation, backpressure does not hang Publish

Postgres wires the suite in `internal/pgstore/conformance_test.go` (skipped unless
`MOSAIC_TEST_PG_DSN` is set). A Kafka/Redpanda package should register the same
suite against its own factory.

## 7. What stays in Postgres after a Kafka swap

Even with Kafka as the log:

- Immutable records (raw/canonical events, insights, recommendations, model runs, audits)
- Checkpoints and projection receipts used by deterministic recovery
- Materialized COP read model (`GET /cop` cheap path)
- Optional fan-out until a separate bus backend is chosen

The UI and public API continue to read Postgres. Kafka is not a query store.

## 8. Checklist before introducing Kafka/Redpanda

1. Implement `eventlog.EventLog`, `EventConsumer`, and (if used) `EventBus`.
2. Pass `eventlogtest.RunConformanceTests` in CI.
3. Wire the new types only in the composition root; leave producers/consumers alone.
4. Keep project+position atomic in the consumer handle; never append+project.
5. Keep partition key = incident (or domain-scoped key) so per-key order holds.
6. Treat delivery as at-least-once; rely on IdempotencyKey for effect-once.
7. Leave Postgres as system of record; do not put COP snapshots on the bus.

See also:

- [`internal/eventlog/doc.go`](../../internal/eventlog/doc.go) — package contract
- [`docs/HANDOFF-v0.4-pluggable-event-spine.md`](../HANDOFF-v0.4-pluggable-event-spine.md) — increment design
- [`docs/runbook/local-docker-demo.md`](local-docker-demo.md) — two-container Postgres topology today
