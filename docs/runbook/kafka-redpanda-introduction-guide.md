# Kafka / Redpanda Introduction Guide

This runbook outlines the architectural role of Kafka (or Redpanda) in the Mosaic ecosystem and how to introduce it as the primary event spine transport layer without disrupting the existing application topology.

## 1. Architectural Role: The Event Spine Transport Layer

In the Mosaic architecture, the event log serves as the immutable spine of the system. All operational facts (raw events, model insights, handoff decisions) are published as discrete events. 

By introducing Kafka or Redpanda, we transition from a polling or listen/notify-based local transport (like Postgres) to a high-throughput, distributed log. Kafka acts purely as the **transport layer**:
- **Producers** append events to topics.
- **Consumers** tail topics and project those events into the relational database.
- The transport layer provides durable retention, partitioning, and strict ordering within partitions.

This enables Mosaic to scale horizontally, allowing multiple consumer groups to project the same event stream into different models or external systems independently.

## 2. Pluggable Transport: ventlog.EventLog and ventlog.EventConsumer

Mosaic's core domains do not know about Kafka or Postgres directly. They depend on small, focused interfaces in the ventlog package (or equivalent internal contracts):

`go
// eventlog.EventLog represents the write-side of the spine.
type EventLog interface {
    Append(ctx context.Context, streamID string, events ...Event) error
}

// eventlog.EventConsumer represents the read-side of the spine.
type EventConsumer interface {
    Consume(ctx context.Context, handler func(Event) error) error
}
`

Because these interfaces define the contract for the event spine, the underlying implementation is completely pluggable. The domain logic simply calls Append(), while the projector logic receives events via Consume(). Switching from a PostgreSQL-backed log to a Kafka-backed log only requires providing a different implementation of these interfaces at startup.

## 3. The "Wiring Swap" at the Composition Root

To introduce Kafka, the domain logic and projectors remain unchanged. The only file that needs modification is the composition root (e.g., cmd/mosaicdemo/main.go), where dependencies are injected.

**Before (Postgres-only):**
`go
// cmd/mosaicdemo/main.go
func main() {
    db := connectPostgres()
    
    // Postgres-backed event log and consumer
    pgLog := pglog.NewEventLog(db)
    pgConsumer := pglog.NewEventConsumer(db)

    // Domain services publish via pgLog
    service := domain.NewService(pgLog)
    
    // Projectors read from pgConsumer
    projector := projection.NewProjector(db)
    go pgConsumer.Consume(ctx, projector.HandleEvent)
    
    // ...
}
`

**After (Kafka swap):**
`go
// cmd/mosaicdemo/main.go
func main() {
    db := connectPostgres()
    kafkaClient := connectKafka()
    
    // Kafka-backed event log and consumer
    kafkaLog := kafkalog.NewEventLog(kafkaClient, "mosaic-events")
    kafkaConsumer := kafkalog.NewEventConsumer(kafkaClient, "mosaic-events", "consumer-group-1")

    // Domain services now publish via kafkaLog
    service := domain.NewService(kafkaLog)
    
    // Projectors read from kafkaConsumer
    projector := projection.NewProjector(db)
    go kafkaConsumer.Consume(ctx, projector.HandleEvent)
    
    // ...
}
`

This "wiring swap" demonstrates how the transport layer can be upgraded with zero changes to the core application logic.

## 4. Postgres as the Relational Read Model (CQRS)

Even after introducing Kafka as the event spine, **PostgreSQL remains a critical component**: it serves as the relational read model (the system of record for queries).

Mosaic follows a Command Query Responsibility Segregation (CQRS) topology:
1. **Write Path:** Domain commands result in events being published (produced) to Kafka.
2. **Projection:** Consumers tail Kafka topics and project the events into structured relational tables in Postgres.
3. **Read Path:** The UI queries the Postgres read-model tables to populate dashboards and views.

Postgres is optimized for complex queries, filtering, and joining data, making it the perfect companion to Kafka's append-only log.

## 5. Delivery Contracts and Checkpoint Guarantees

When projecting events from Kafka to Postgres, we must ensure consistency.

**Delivery Contract:**
- **At-Least-Once Delivery:** Kafka ensures that events are delivered to consumers at least once. Consumers must be idempotent or manage offsets carefully to prevent duplicate processing.
- **Ordered Per Partition-Key:** Kafka guarantees strict ordering of events within a single partition. By using the aggregate ID (e.g., Incident ID) as the partition key, we guarantee that all events for a specific incident are processed in the exact order they occurred.

**Atomic Projection and Position Checkpoints:**
To achieve exactly-once projection semantics (idempotency) and crash resilience, the consumer's position (the Kafka offset) must be saved atomically with the projected data.

In the consumer handler, we open a single Postgres transaction to:
1. Apply the event to the read-model tables.
2. Update the consumer_offsets table with the new Kafka offset.
3. Commit the transaction.

If the process crashes before the commit, the transaction rolls back, and the consumer will re-read the event from the last saved offset. If it succeeds, both the data and the offset move forward atomically. This guarantees that the read model is always consistent with the event stream, avoiding partial updates or skipped events.
