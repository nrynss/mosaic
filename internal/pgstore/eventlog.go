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
// Validation requires non-empty PartitionKey, IdempotencyKey, and Type. A nil
// Payload is stored as empty bytes so producers need not distinguish nil from
// zero-length. Re-appending an already-seen IdempotencyKey returns nil without
// inserting a second row (unique constraint + SQLSTATE 23505).
func (s *Store) Append(ctx context.Context, e eventlog.EventEnvelope) error {
	if err := validateEventEnvelope(e); err != nil {
		return err
	}
	payload := e.Payload
	if payload == nil {
		payload = []byte{}
	}

	// Append deliberately uses the pool, not the ambient WithinTransaction
	// executor: the projector must never couple (append + project) into one
	// ACID transaction. Sequence is assigned by BIGSERIAL.
	_, err := s.pool.Exec(ctx, `INSERT INTO event_log
		(partition_key, idempotency_key, event_type, payload)
		VALUES ($1, $2, $3, $4)`,
		e.PartitionKey, e.IdempotencyKey, e.Type, payload,
	)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
		// Unique on idempotency_key: producer retry after ambiguous success.
		return nil
	}
	return fmt.Errorf("append event log: %w", err)
}

func validateEventEnvelope(e eventlog.EventEnvelope) error {
	if strings.TrimSpace(e.IdempotencyKey) == "" {
		return fmt.Errorf("%w: IdempotencyKey is required", ErrInvalidRecord)
	}
	// PartitionKey is required here (not the degenerate empty single-partition
	// form allowed by the interface docs). Empty keys would collapse every
	// producer into one ordering unit and defeat per-incident parallelism.
	if strings.TrimSpace(e.PartitionKey) == "" {
		return fmt.Errorf("%w: PartitionKey is required", ErrInvalidRecord)
	}
	if strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("%w: Type is required", ErrInvalidRecord)
	}
	return nil
}
