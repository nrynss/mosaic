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
// Append is the only method required today; EventConsumer arrives in B3.
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

// normalizeEventEnvelope trims identity fields and enforces non-empty keys.
// PartitionKey is required here (not the degenerate empty single-partition form
// allowed by the interface docs): empty keys would collapse every producer into
// one ordering unit and defeat per-incident parallelism.
func normalizeEventEnvelope(e eventlog.EventEnvelope) (eventlog.EventEnvelope, error) {
	out := eventlog.EventEnvelope{
		PartitionKey:   strings.TrimSpace(e.PartitionKey),
		IdempotencyKey: strings.TrimSpace(e.IdempotencyKey),
		Type:           strings.TrimSpace(e.Type),
		Payload:        e.Payload,
	}
	if out.IdempotencyKey == "" {
		return eventlog.EventEnvelope{}, fmt.Errorf("%w: IdempotencyKey is required", ErrInvalidRecord)
	}
	if out.PartitionKey == "" {
		return eventlog.EventEnvelope{}, fmt.Errorf("%w: PartitionKey is required", ErrInvalidRecord)
	}
	if out.Type == "" {
		return eventlog.EventEnvelope{}, fmt.Errorf("%w: Type is required", ErrInvalidRecord)
	}
	return out, nil
}
