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
    PartitionKey   string // routing + per-key ordering; non-empty after trim
    IdempotencyKey string // global source dedup token (first-wins payload/type)
    Type           string // application label, opaque to transport; required
    Payload        []byte // opaque body; transport never interprets it
}
```

`Append` rejects empty/whitespace `PartitionKey`, `IdempotencyKey`, and `Type`
(shared `ValidateEnvelope`). IdempotencyKey scope is **global** to the log, not
per partition.

### EventConsumer (read side)

```go
type EventConsumer interface {
    // Run delivers events ordered per PartitionKey, at-least-once.
    // handle nil  => implementation advances position ("consumed through")
    // handle err  => no ack; event will be redelivered
    Run(ctx context.Context, handle func(context.Context, Event) error) error
}

type Event struct {
    EventEnvelope
    Position  Position  // opaque per-partition cursor (exported diagnostics/SoR)
    Sequence  uint64    // backend-defined; monotonic within partition; may be sparse
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

`Position` is an opaque per-partition cursor (`PartitionKey()` + `Token()`),
exported for diagnostics and system-of-record metadata — not a seek API.
Callers **must not** do arithmetic on tokens. Only the all-zero position is the
portable “before the first event” marker; an empty token with a non-empty
partition may be a backend start marker. Equality is only meaningful within the
same partition.

### Delivery contract (weakest common semantics)

Every backend must honor **only**:

- **At-least-once**, never exactly-once.
- **Ordered per partition key**, never globally ordered.

Mosaic’s source **IdempotencyKey** turns at-least-once into effectively-once at
the projection boundary. Do not claim exactly-once delivery from the transport.

## 3. Postgres implementation today (`internal/pgstore`)

| Seam | Type | Behaviour |
|------|------|-----------|
| `EventLog` | `*pgstore.Store` | `Append` → `INSERT` into `event_log`; unique on `idempotency_key` (global) → first-wins no-op |
| `EventConsumer` | `pgstore.EventConsumer` | Per-partition **advisory locks** (not row `SKIP LOCKED`); process in sequence; checkpoint in same TX as handle when possible |
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

## 5. Atomicity boundary (never append + project)

Hard rule:

> **Never (append + project)** as the product path.

Handlers **must** be idempotent. Backends **should** make successful handle +
position advance atomic when storage allows it. The **portable floor** is
process-then-advance with at-least-once redelivery — not “one shared transaction”
required of both Postgres and Kafka.

Why: Postgres *could* append and project in one ACID transaction; Kafka cannot.
Both backends can express “I processed this event and advanced past it”
(atomically when possible). Encoding append+project into the product path would
nail the spine to Postgres.

On the Postgres consumer path, `EventConsumer.Run` invokes `handle` inside
`Store.WithinTransaction`. A nil return UPSERTs the consumer checkpoint in the
same transaction. On error the transaction rolls back and the event is
redelivered (at-least-once). Handlers that also materialize the COP (via
`pgstore.MaterializingProjector`) should save the read model on that same TX
context so project+position+materialize stay atomic **where the backend can**.

### Kafka Append dedup cost

Postgres enforces global `IdempotencyKey` uniqueness cheaply. A Kafka adapter
must pay for the same first-wins contract via an external store, transactional
outbox, or equivalent — **Append dedup is not free**. Parallelism is also
implementation-defined (logical key claim vs hashing onto physical partitions).

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
- Global idempotency across partitions (same key, two PKs → one row wins)
- Reject empty/whitespace PartitionKey, IdempotencyKey, Type
- Per-partition-key ordering; Sequence non-decreasing within partition (sparse OK)
- Multi-partition both complete (no global order claim)
- Payload isolation after Append mutation
- At-least-once redelivery after handler error
- Position token integrity; context cancel ends Run cleanly
- Multi-worker same consumer group: no double-process under successful handlers
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
4. Never append+project; prefer atomic handle+advance when possible; portable floor is process-then-advance + idempotent handlers.
5. Keep partition key = incident (or domain-scoped key) so per-key order holds; non-empty after trim.
6. Treat delivery as at-least-once; rely on global IdempotencyKey for effect-once (budget for Append dedup cost on Kafka).
7. Leave Postgres as system of record; do not put COP snapshots on the bus.

See also:

- [`internal/eventlog/doc.go`](../../internal/eventlog/doc.go) — package contract
- [`docs/HANDOFF-v0.4-pluggable-event-spine.md`](../HANDOFF-v0.4-pluggable-event-spine.md) — increment design
- [`docs/runbook/local-docker-demo.md`](local-docker-demo.md) — two-container Postgres topology today
