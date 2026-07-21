package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"mosaic.local/mosaic/internal/eventlog"
)

// Compile-time proof that Store satisfies the EventLog transport seam.
// EventConsumer and EventBus are separate types (see consumer.go, eventbus.go).
var _ eventlog.EventLog = (*Store)(nil)

// postgresUniqueViolation is the SQLSTATE for unique_violation (ON CONFLICT /
// duplicate key). Used to turn a re-append of the same IdempotencyKey into a
// successful no-op instead of a hard error — only when the violated constraint
// is the idempotency unique (see isIdempotencyUniqueViolation).
const postgresUniqueViolation = "23505"

// eventLogIdempotencyConstraint is the default Postgres name for the inline
// UNIQUE on event_log.idempotency_key (migration 0002). Also accept any
// constraint name containing "idempotency_key" so renamed uniques still map
// to idempotent success.
const eventLogIdempotencyConstraint = "event_log_idempotency_key_key"

// Append durably records e in the event_log table. It is the Postgres
// implementation of eventlog.EventLog: a plain INSERT, never paired with
// projection work in the same transaction (see the package atomic-boundary rule
// in internal/eventlog).
//
// Validation requires non-empty PartitionKey, IdempotencyKey, and Type after
// trimming whitespace. A nil Payload is stored as empty bytes so producers need
// not distinguish nil from zero-length. Re-appending an already-seen
// IdempotencyKey returns nil without inserting a second row (unique constraint
// + SQLSTATE 23505 on the idempotency unique only). Other unique violations
// (for example a future secondary unique) remain hard errors.
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
	if isIdempotencyUniqueViolation(err) {
		// Producer retry after ambiguous success. First-wins; payload/type on
		// retry ignored.
		return nil
	}
	return fmt.Errorf("append event log: %w", err)
}

// isIdempotencyUniqueViolation reports whether err is SQLSTATE 23505 on the
// event_log idempotency unique. Other unique violations must surface as errors.
func isIdempotencyUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != postgresUniqueViolation {
		return false
	}
	name := strings.ToLower(pgErr.ConstraintName)
	if name == "" {
		// Defensive: some drivers omit ConstraintName; fall back to the known
		// column when the message mentions idempotency.
		return strings.Contains(strings.ToLower(pgErr.Message), "idempotency_key") ||
			strings.Contains(strings.ToLower(pgErr.Detail), "idempotency_key")
	}
	return name == eventLogIdempotencyConstraint || strings.Contains(name, "idempotency_key")
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
