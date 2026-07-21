package eventlog

import (
	"context"
	"time"
)

// EventLog is the append side of the log transport. Backends: a Postgres INSERT
// with a unique constraint on IdempotencyKey, or a Kafka/Redpanda produce keyed
// by PartitionKey. It carries no read, offset, or transaction surface — appending
// is all a producer (including the simulation) may do.
type EventLog interface {
	// Append durably records e. It is at-least-once safe: re-appending an
	// envelope with an already-seen IdempotencyKey must be a no-op that returns
	// nil, so a producer that retries after an ambiguous failure never creates a
	// duplicate. Append does not project; a separate EventConsumer does.
	Append(ctx context.Context, e EventEnvelope) error
}

// EventEnvelope is what a producer appends. It is deliberately minimal: only
// fields both a relational row and a Kafka record carry natively belong here.
type EventEnvelope struct {
	// PartitionKey is the routing and per-key ordering unit (default: incident
	// id). Events sharing a key are delivered in append order; events in
	// different keys have no defined relative order and may project in parallel.
	// An empty key is a degenerate single-partition stream, not a wildcard.
	PartitionKey string

	// IdempotencyKey is the source-assigned dedup token that makes at-least-once
	// safe. Appending the same key twice records the event once. It is the
	// producer's stable identity for "this exact event," not a per-attempt nonce.
	IdempotencyKey string

	// Type names the event kind for consumer dispatch. It is an application-level
	// label, opaque to the transport.
	Type string

	// Payload is the opaque event body, serialized by the producer. The transport
	// never interprets it. Keep the full state in the payload; the fan-out
	// EventBus is for hints, not bodies.
	Payload []byte
}

// EventConsumer is the read side of the log transport. It delivers events
// ordered per partition key, at-least-once, and owns all position/offset
// tracking internally — callers never see or advance a raw offset.
//
// Backends: a Postgres SKIP LOCKED claim grouped by PartitionKey, processed in
// sequence order with a checkpointed cursor; or a Kafka consumer group with
// committed offsets.
//
// Atomic-boundary rule (see package doc): the handler MUST commit its projection
// update and the advance of this event's Position in ONE transaction —
// (projection update + position advance), never (append + project). Both
// backends honor project-plus-position; only Postgres could honor
// append-plus-project, and depending on it would nail the transport to Postgres.
type EventConsumer interface {
	// Run consumes until ctx is cancelled or a fatal transport error occurs.
	//
	// For each delivered Event, handle is invoked:
	//   - returns nil  => the event is acknowledged and the consuming position
	//     advances past it. The handler is expected to have committed its
	//     projection update together with the position advance atomically.
	//   - returns error => the event is NOT acknowledged and will be redelivered.
	//     Because delivery is at-least-once, a handler that partially succeeded
	//     before erroring must be safe to re-run; use IdempotencyKey to dedup.
	//
	// Ordering is guaranteed only within a PartitionKey. Run may process
	// different partitions concurrently, so handle must be safe for concurrent
	// invocation across distinct keys.
	Run(ctx context.Context, handle func(context.Context, Event) error) error
}

// Event is a delivered log entry: the producer's envelope plus backend-neutral
// delivery metadata. The embedded EventEnvelope exposes PartitionKey,
// IdempotencyKey, Type, and Payload as they were appended.
type Event struct {
	EventEnvelope

	// Position is the opaque cursor for this event within its partition. The
	// handler persists it (via its atomic project+position commit) so consumption
	// can resume exactly here. It is not an offset callers may do arithmetic on;
	// see Position.
	Position Position

	// Sequence is a per-partition monotonic ordering signal for logs and
	// diagnostics. It is monotonically increasing within a PartitionKey but MAY
	// be sparse (gaps from other partitions' events, quarantines, or redelivery)
	// and is meaningless across partitions. It is NOT a global offset and MUST
	// NOT be used for arithmetic, gap detection, or resumption — Position owns
	// resumption. It exists so operators can read "incident X, event 5," nothing
	// more.
	Sequence uint64

	// Timestamp is when the event was appended/produced, as recorded by the
	// backend. Clocks may skew and it is not an ordering source (Position and
	// per-partition delivery order are); treat it as descriptive metadata only.
	Timestamp time.Time
}

// Position is an opaque, per-partition consuming cursor. It marks "up to here"
// within one PartitionKey and is what the atomic project+position commit
// persists so consumption can resume without reprocessing acknowledged events.
//
// Deliberate non-capabilities:
//   - It is NOT a dense global integer. Callers cannot add to it, diff it, or
//     derive the "next" position; only the backend understands its token.
//   - Equality (==) is meaningful ONLY between positions in the same partition.
//     Comparing positions from different PartitionKeys is undefined.
//   - There is no exported cross-partition ordering; global order does not exist
//     under the delivery contract, so none is offered.
//
// The zero Position means "before the first event of the partition" — a valid
// starting cursor. Position is comparable, so it may be used as a map key or
// compared with ==; that comparison only carries meaning within a partition.
type Position struct {
	// partitionKey scopes the cursor; comparisons are only meaningful when two
	// positions share it.
	partitionKey string
	// token is the backend-defined opaque cursor (e.g. a Postgres sequence
	// encoding or a Kafka offset encoding). It is a string, not an int, so no
	// caller can do offset arithmetic on it. Its contents are meaningless outside
	// the backend that produced it.
	token string
}

// NewPosition constructs a Position from a backend-defined opaque token scoped to
// partitionKey. Only EventConsumer/EventLog implementations (in other packages)
// should call this; application code receives positions, it does not mint them.
func NewPosition(partitionKey, token string) Position {
	return Position{partitionKey: partitionKey, token: token}
}

// PartitionKey returns the partition this position is scoped to.
func (p Position) PartitionKey() string { return p.partitionKey }

// Token returns the backend-defined opaque cursor. It is exposed so the handler
// can serialize the position into its atomic project+position commit; it carries
// no meaning to application code and must not be parsed.
func (p Position) Token() string { return p.token }

// IsZero reports whether p is the zero cursor, i.e. "before the first event."
func (p Position) IsZero() bool { return p.partitionKey == "" && p.token == "" }

// EventBus is the fan-out seam (layer 3): best-effort, ephemeral notification
// that the read model changed. Backends: Postgres LISTEN/NOTIFY now; Redis, NATS,
// or a compacted topic later.
//
// It is NOT the durable log. Notes may be dropped, coalesced, or arrive
// out of order; a subscriber that misses one recovers by re-reading the read
// model, never by replaying the bus. Never route state through it — payloads are
// tiny hints (a revision number or id), never a COP snapshot.
type EventBus interface {
	// Publish sends a small note to subscribers of topic. Delivery is
	// best-effort: with no subscribers, or a slow one, the note may simply be
	// dropped. A nil error means the note was accepted for fan-out, not that any
	// subscriber received it.
	Publish(ctx context.Context, topic string, note []byte) error

	// Subscribe returns a channel of notes for topic. The implementation owns the
	// channel and closes it when ctx is cancelled; the caller must drain it and
	// must not assume every published note appears (see the best-effort
	// contract). Buffering is bounded, so a subscriber that stops reading loses
	// notes rather than stalling publishers.
	Subscribe(ctx context.Context, topic string) (<-chan []byte, error)
}
