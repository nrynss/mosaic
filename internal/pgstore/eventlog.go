package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"mosaic.local/mosaic/internal/eventlog"
)

// Compile-time proof that Store satisfies the EventLog transport seam.
// EventConsumer and EventBus are separate types (see consumer.go, eventbus.go).
var _ eventlog.EventLog = (*Store)(nil)

// postgresUniqueViolation is the SQLSTATE for unique_violation (ON CONFLICT /
// duplicate key). Used to turn a re-append of the same IdempotencyKey into a
// successful no-op instead of a hard error.
const postgresUniqueViolation = "23505"

// Append durably records e in the event_log table. It is the Postgres
// implementation of eventlog.EventLog: a plain INSERT, never paired with
// projection work in the same transaction (see the package atomic-boundary rule
// in internal/eventlog).
//
// Validation requires non-empty PartitionKey, IdempotencyKey, and Type after
// trimming whitespace. A nil Payload is stored as empty bytes so producers need
// not distinguish nil from zero-length. Re-appending an already-seen
// IdempotencyKey returns nil without inserting a second row (unique constraint
// + SQLSTATE 23505).
func (s *Store) Append(ctx context.Context, e eventlog.EventEnvelope) error {
	env, err := normalizeEventEnvelope(e)
	if err != nil {
		return err
	}
	payload := env.Payload
	if payload == nil {
		payload = []byte{}
	}

	// Append deliberately uses the pool, not the ambient WithinTransaction
	// executor: the projector must never couple (append + project) into one
	// ACID transaction. Sequence is assigned by BIGSERIAL.
	_, err = s.pool.Exec(ctx, `INSERT INTO `+EventLogTable+`
		(`+EventLogColPartitionKey+`, `+EventLogColIdempotencyKey+`, `+EventLogColEventType+`, `+EventLogColPayload+`)
		VALUES ($1, $2, $3, $4)`,
		env.PartitionKey, env.IdempotencyKey, env.Type, payload,
	)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
		// Unique on idempotency_key (only non-PK unique on this table): producer
		// retry after ambiguous success. First-wins; payload/type on retry ignored.
		return nil
	}
	return fmt.Errorf("append event log: %w", err)
}

// normalizeEventEnvelope applies the shared [eventlog.ValidateEnvelope] contract
// and wraps failures with ErrInvalidRecord so callers keep the pgstore error type.
func normalizeEventEnvelope(e eventlog.EventEnvelope) (eventlog.EventEnvelope, error) {
	out, err := eventlog.ValidateEnvelope(e)
	if err != nil {
		return eventlog.EventEnvelope{}, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	return out, nil
}
