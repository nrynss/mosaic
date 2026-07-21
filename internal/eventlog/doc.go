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
//     Now: a Postgres table with a monotonic sequence, claimed per partition and
//     checkpointed as work completes. Later: a Kafka/Redpanda topic keyed by
//     partition, consumed by a consumer group. Only this layer is replaced.
//     Package docs speak only claim / order / checkpoint semantics — not a
//     particular lock primitive (SKIP LOCKED, advisory locks, etc.).
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
// claim/checkpoint queue and a Kafka consumer group can honor. No consumer may
// rely on more:
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
// Handlers MUST be idempotent: delivery is at-least-once, so a crash or
// rebalance can redeliver an event after partial side effects.
//
// Backends SHOULD make handle success and position advance atomic when the
// storage model allows it (Postgres can commit projection work and a checkpoint
// in one transaction). That is an implementation strength, not a portable floor.
//
// The portable floor is process-then-advance with at-least-once redelivery: the
// consumer invokes handle, and on nil return the implementation advances the
// consuming position past that event. If the process dies between handle success
// and advance, the event may be redelivered; idempotent handlers absorb that.
//
// NEVER pair append with projection in one product path:
//
//	(append + project)   -- only co-located Postgres can honor this; Kafka cannot
//
// Both backends can express "I processed this event and advanced past it"
// (atomically when possible, process-then-advance otherwise). Only a single
// database can atomically "append a new event AND project it." Choosing the
// boundary both systems can honor is the entire price of keeping the log
// pluggable; see [EventConsumer] for how the contract is expressed in the
// handler signature.
//
// # Partition key: scale and determinism, one decision
//
// The partition key is simultaneously the sharding unit and the ordering unit.
// A backend-defined sequence gives per-partition order; the subsequence for any
// single key is ordered, so per-incident order is free. Workers claim one key at
// a time and process it in sequence order, so different incidents project in
// parallel while each stays strictly ordered — the "1000 events at once" answer
// that does not break determinism. Physical parallelism later (hash partitioning,
// Kafka partitions) is the same decision expressed in infrastructure; how
// logical keys map onto physical partitions is implementation-defined.
//
// # Kafka / distributed-log costs (honest)
//
//   - Append idempotency is not free on Kafka. Postgres enforces a unique
//     IdempotencyKey constraint cheaply; a Kafka producer must use an external
//     store, transactional outbox, or equivalent to give the same first-wins
//     no-op. Callers still see the same Append contract; the cost sits in the
//     adapter.
//   - Parallelism is implementation-defined: logical per-key claim (one worker
//     drains a partition key) versus hashing keys onto physical Kafka partitions
//     with consumer-group assignment. The interface promises per-key order and
//     concurrent keys; it does not prescribe the claim mechanism.
//
// # What must never leak
//
// Nothing backend-specific may appear in this package's API: no SQL, no
// *sql.Tx, no Postgres or Kafka types, and no raw offsets-as-integers callers can
// do arithmetic on. Position tracking is owned entirely by the implementation;
// callers receive opaque [Position] values for diagnostics and system-of-record
// metadata, but never advance a raw offset themselves. If a construct cannot be
// honored by both a claim/checkpoint queue and a Kafka consumer group, it does
// not belong in these interfaces.
package eventlog
