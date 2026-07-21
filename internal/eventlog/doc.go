// Package eventlog defines the backend-agnostic seam for Mosaic's event spine:
// the interfaces, envelope, and delivery contract that let the system slide from
// a Postgres-only backbone to a real log (Kafka/Redpanda/NATS JetStream) by
// re-wiring composition, without any producer or consumer changing a line.
//
// # Three-layer model
//
// Mosaic separates three responsibilities that are all physically Postgres today
// but must never be conflated in code. Keeping them distinct is what makes the
// transport swappable later:
//
//  1. Log (transport). Append events and consume them in order. This package's
//     [EventLog] (append side) and [EventConsumer] (read side) are that seam.
//     Now: a Postgres table with a monotonic sequence claimed via
//     SELECT ... FOR UPDATE SKIP LOCKED. Later: a Kafka/Redpanda topic keyed by
//     partition, consumed by a consumer group. Only this layer is replaced.
//
//  2. System of record / read model. The immutable provenance store and the
//     materialized common operating picture (COP). This is Postgres forever and
//     is NOT part of this package — it lives behind the repository contracts in
//     internal/contracts. The projector reads from the log seam and writes here.
//
//  3. Fan-out. Best-effort notification that the read model changed, so SSE
//     gateways can nudge browsers to re-read. This package's [EventBus] is that
//     seam. Now: Postgres LISTEN/NOTIFY. Later: Redis, NATS, or a compacted
//     topic. Payloads are tiny (a revision or id), never the COP itself.
//
// The log seam (layer 1) carries durable, ordered, at-least-once events that
// drive state. The bus seam (layer 3) carries ephemeral, droppable hints that
// carry no state. They are deliberately different interfaces with different
// guarantees; do not use one where the other is meant.
//
// # The delivery contract
//
// Every guarantee this package promises is the WEAKEST that BOTH a Postgres
// SKIP LOCKED queue and a Kafka consumer group can honor. No consumer may rely
// on more:
//
//   - At-least-once, never exactly-once. An event may be delivered more than
//     once (crash between side effect and position advance, a redelivery after a
//     handler error, a rebalance). Consumers MUST be idempotent. Mosaic's source
//     idempotency ([EventEnvelope.IdempotencyKey]) turns at-least-once into
//     effectively-once at the projection boundary.
//
//   - Ordered per partition key, never globally ordered. Events sharing a
//     [EventEnvelope.PartitionKey] (default: incident id) are delivered in append
//     order. Events in different partitions have NO defined relative order and
//     may be processed in parallel. There is no global total order across
//     partitions — do not assume one exists, and do not build cross-incident
//     causality on delivery order.
//
// Why the weakest contract on purpose: exactly-once and global order are exactly
// the guarantees a distributed log cannot give cheaply. Encoding them into the
// interface would let consumers quietly depend on Postgres-only behavior, and the
// Kafka door would slam shut. Restraint in the interface is the portability.
//
// # The atomic-boundary rule
//
// A Postgres-only design tempts you to append an event and project it in one ACID
// transaction. Kafka cannot do that. So the projector consumes from the log seam
// and commits, in a single transaction:
//
//	(projection update + position advance)   -- both backends honor this
//
// NEVER:
//
//	(append + project)                       -- only Postgres honors this
//
// Both backends can atomically record "I updated the read model AND advanced my
// consuming position to here." Only Postgres can atomically "append a new event
// AND project it." Choosing the boundary both systems honor is the entire price
// of keeping the log pluggable; see [EventConsumer] for how the contract is
// expressed in the handler signature.
//
// # Partition key: scale and determinism, one decision
//
// The partition key is simultaneously the sharding unit and the ordering unit.
// A monotonic sequence gives a total order; the subsequence for any single key
// is still ordered, so per-incident order is free. Workers claim one key at a
// time and process it in sequence order, so different incidents project in
// parallel while each stays strictly ordered — the "1000 events at once" answer
// that does not break determinism. Physical parallelism later (hash partitioning,
// Kafka partitions) is the same decision expressed in infrastructure.
//
// # What must never leak
//
// Nothing backend-specific may appear in this package's API: no SQL, no
// *sql.Tx, no Postgres or Kafka types, and no raw offsets-as-integers callers can
// do arithmetic on. Position tracking is owned entirely by the implementation;
// callers never see or advance a raw offset. If a construct cannot be honored by
// both a Postgres SKIP LOCKED queue and a Kafka consumer group, it does not
// belong in these interfaces.
package eventlog
