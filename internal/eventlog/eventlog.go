package eventlog

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// EventLog is the append side of the log transport. Backends: a Postgres INSERT
// with a unique constraint on IdempotencyKey, or a Kafka/Redpanda produce keyed
// by PartitionKey. It carries no read, offset, or transaction surface — appending
// is all a producer (including the simulation) may do.
type EventLog interface {
	// Append durably records e. It is at-least-once safe: re-appending an
	// envelope with an already-seen IdempotencyKey must be a no-op that returns
	// nil (first-wins: payload and type from the first successful append are
	// retained; later conflict payloads/types are ignored). IdempotencyKey scope
	// is global to the log, not per PartitionKey — the same key recorded under
	// two partitions still yields a single stored event. Append does not project;
	// a separate EventConsumer does.
	//
	// PartitionKey, IdempotencyKey, and Type must be non-empty after trimming
	// whitespace; empty or whitespace-only values are rejected. Implementations
	// should use [ValidateEnvelope] (or equivalent) before storing.
	//
	// On Kafka/Redpanda, Append dedup is not free: adapters pay for an external
	// store, transactional outbox, or similar. Callers still see this contract.
	Append(ctx context.Context, e EventEnvelope) error
}

// EventEnvelope is what a producer appends. It is deliberately minimal: only
// fields both a relational row and a Kafka record carry natively belong here.
type EventEnvelope struct {
	// PartitionKey is the routing and per-key ordering unit (default: incident
	// id). Events sharing a key are delivered in append order; events in
	// different keys have no defined relative order and may project in parallel.
	// Must be non-empty after trim; there is no empty-key "degenerate stream."
	PartitionKey string

	// IdempotencyKey is the source-assigned dedup token that makes at-least-once
	// safe. Appending the same key twice records the event once (global scope
	// across partitions; first-wins payload and type). It is the producer's
	// stable identity for "this exact event," not a per-attempt nonce.
	IdempotencyKey string

	// Type names the event kind for consumer dispatch. It is an application-level
	// label, opaque to the transport. Must be non-empty after trim.
	Type string

	// Payload is the opaque event body, serialized by the producer. The transport
	// never interprets it. Keep the full state in the payload; the fan-out
	// EventBus is for hints, not bodies. Nil and empty are both valid; backends
	// may normalize nil to zero-length bytes. Callers must not mutate Payload
	// after Append returns (implementations copy on store).
	Payload []byte
}

// ValidateEnvelope trims PartitionKey, IdempotencyKey, and Type and rejects any
// that are empty after trim. Payload is passed through unchanged (including nil).
// Callers should use the returned envelope for storage so stored identity fields
// match the validated form.
func ValidateEnvelope(e EventEnvelope) (EventEnvelope, error) {
	out := EventEnvelope{
		PartitionKey:   strings.TrimSpace(e.PartitionKey),
		IdempotencyKey: strings.TrimSpace(e.IdempotencyKey),
		Type:           strings.TrimSpace(e.Type),
		Payload:        e.Payload,
	}
	if out.PartitionKey == "" {
		return EventEnvelope{}, fmt.Errorf("PartitionKey is required")
	}
	if out.IdempotencyKey == "" {
		return EventEnvelope{}, fmt.Errorf("IdempotencyKey is required")
	}
	if out.Type == "" {
		return EventEnvelope{}, fmt.Errorf("Type is required")
	}
	return out, nil
}

// EventConsumer is the read side of the log transport. It delivers events
// ordered per partition key, at-least-once, and owns all position/offset
// tracking internally — callers never see or advance a raw offset.
//
// Backends: a Postgres claim/checkpoint consumer grouped by PartitionKey,
// processed in sequence order with a checkpointed cursor; or a Kafka consumer
// group with committed offsets. How logical keys map to physical partitions or
// claim slots is implementation-defined.
//
// Atomic-boundary rule (see package doc): handlers MUST be idempotent. Backends
// SHOULD make a successful handle and position advance atomic when they can
// (e.g. one Postgres transaction for projection + checkpoint). The portable
// floor is process-then-advance with at-least-once redelivery — never require
// a single shared transaction model across Postgres and Kafka. NEVER
// (append + project) as the product path; only co-located Postgres could honor
// that, and depending on it would nail the transport to Postgres.
type EventConsumer interface {
	// Run consumes until ctx is cancelled or a fatal transport error occurs.
	// On ctx cancel, Run returns promptly (typically ctx.Err() or nil after
	// draining in-flight work); it must not hang.
	//
	// For each delivered Event, handle is invoked:
	//   - returns nil  => the implementation acknowledges the event and advances
	//     the consuming position past it ("consumed through" this event). The
	//     handler does not advance Position itself. When the backend can, it
	//     commits handler side effects together with that advance; otherwise
	//     process-then-advance with at-least-once redelivery applies.
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

	// Position is the opaque cursor for this event within its partition. It is
	// diagnostic and system-of-record metadata the handler may persist alongside
	// projection work. The consumer implementation advances the checkpoint after
	// a successful handle ("consumed through" this position), not "resume
	// exactly at this token as a seek API." Callers do not advance Position
	// themselves; see Position.
	Position Position

	// Sequence is a backend-defined per-partition ordering signal for logs and
	// diagnostics. It is monotonic (non-decreasing) within a PartitionKey but
	// MAY be sparse — gaps are normal (global serials, quarantines, or
	// implementation choice) and MUST NOT be treated as dense "incident X,
	// event 5" counters. Sequence is meaningless across partitions and MUST NOT
	// be used for arithmetic, gap detection, resumption, or cross-partition
	// comparison. Position owns resumption metadata; Sequence is descriptive.
	Sequence uint64

	// Timestamp is when the event was appended/produced, as recorded by the
	// backend. Clocks may skew and it is not an ordering source (Position and
	// per-partition delivery order are); treat it as descriptive metadata only.
	Timestamp time.Time
}

// Position is an opaque, per-partition consuming cursor. It marks "consumed
// through here" within one PartitionKey and is what backends persist (often
// with projection work) so acknowledged events are not reprocessed under normal
// operation. The type is exported so handlers and system-of-record code can
// store diagnostic cursor metadata; it is not a seek or arithmetic API.
//
// Deliberate non-capabilities:
//   - It is NOT a dense global integer. Callers cannot add to it, diff it, or
//     derive the "next" position; only the backend understands its token.
//   - Equality (==) is meaningful ONLY between positions in the same partition.
//     Comparing positions from different PartitionKeys is undefined.
//   - There is no exported cross-partition ordering; global order does not exist
//     under the delivery contract, so none is offered.
//
// The zero Position (both partitionKey and token empty) is the only portable
// "before the first event" marker. An empty token with a non-empty partition
// key may be a backend-specific start marker and is not portable across
// implementations. Position is comparable, so it may be used as a map key or
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
// can serialize the position into system-of-record / diagnostic metadata; it
// carries no meaning to application code and must not be parsed or used for
// arithmetic.
func (p Position) Token() string { return p.token }

// IsZero reports whether p is the portable zero cursor: both partition key and
// token empty ("before the first event" in the only cross-backend sense). An
// empty token with a non-empty partition key is not IsZero and may be a
// backend-specific start marker — do not treat it as portable "before first."
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
