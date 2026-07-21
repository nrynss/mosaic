package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mosaic.local/mosaic/internal/eventlog"
)

// DefaultConsumerGroup is the projector's logical consumer group. Distinct
// groups keep independent cursors on the same event_log partitions.
const DefaultConsumerGroup = "mosaic-projector"

// DefaultConsumerIdleInterval is how long Run sleeps when no partition can be
// claimed or no work is pending. Short enough for interactive demos, long
// enough to avoid a tight poll loop.
const DefaultConsumerIdleInterval = 50 * time.Millisecond

// DefaultConsumerErrorBackoff is a brief pause after a handler error so a
// stuck event does not burn CPU while still redelivering promptly.
const DefaultConsumerErrorBackoff = 100 * time.Millisecond

// ConsumerConfig configures a Postgres EventConsumer.
type ConsumerConfig struct {
	// ConsumerGroup scopes checkpoints. Empty becomes DefaultConsumerGroup.
	ConsumerGroup string

	// IdleInterval is the sleep when no work is available or every pending
	// partition is locked by another worker. Zero becomes DefaultConsumerIdleInterval.
	IdleInterval time.Duration

	// ErrorBackoff is the sleep after a handler error before trying more work.
	// Zero becomes DefaultConsumerErrorBackoff. A negative value disables it.
	ErrorBackoff time.Duration
}

// EventConsumer is the Postgres implementation of eventlog.EventConsumer.
//
// # Lock strategy (session advisory locks)
//
// Multi-worker safety uses session-level Postgres advisory locks, not
// row-level SKIP LOCKED on individual event_log rows. Claiming events would
// allow two workers to interleave the same partition_key and break per-key
// order. Instead:
//
//  1. Discover partition keys that still have sequences beyond the group's
//     checkpoint.
//  2. For each candidate, try pg_try_advisory_lock(hashtext(group),
//     hashtext(partition_key)) on a pooled connection held for the drain.
//  3. The lock holder processes that partition's pending events in sequence
//     order, one event per atomic project+position transaction.
//  4. pg_advisory_unlock releases the key so another worker may continue.
//
// Different partitions lock independently and may run on concurrent workers.
// The same partition never has two active handlers in one consumer group.
// hashtext collisions only over-serialize (two keys share a lock slot); they
// never under-serialize.
//
// # Atomic project+position
//
// handle is invoked inside Store.WithinTransaction. On nil, the checkpoint is
// UPSERTed in the same transaction (consumed through that event's sequence).
// On error the transaction rolls back and the checkpoint does not advance, so
// the event is redelivered later. Nested WithinTransaction calls join, so a
// handler that projects via the TX context gets true atomicity with the
// cursor advance.
type EventConsumer struct {
	store        *Store
	group        string
	idle         time.Duration
	errorBackoff time.Duration
}

// Compile-time proof that EventConsumer satisfies the transport seam.
var _ eventlog.EventConsumer = (*EventConsumer)(nil)

// NewEventConsumer constructs a consumer bound to store. store must outlive
// the consumer; the consumer does not close the store or its pool.
func NewEventConsumer(store *Store, cfg ConsumerConfig) *EventConsumer {
	if store == nil {
		panic("pgstore: NewEventConsumer requires a non-nil Store")
	}
	group := strings.TrimSpace(cfg.ConsumerGroup)
	if group == "" {
		group = DefaultConsumerGroup
	}
	idle := cfg.IdleInterval
	if idle <= 0 {
		idle = DefaultConsumerIdleInterval
	}
	backoff := cfg.ErrorBackoff
	if backoff == 0 {
		backoff = DefaultConsumerErrorBackoff
	}
	if backoff < 0 {
		backoff = 0
	}
	return &EventConsumer{
		store:        store,
		group:        group,
		idle:         idle,
		errorBackoff: backoff,
	}
}

// ConsumerGroup returns the configured group name (never empty).
func (c *EventConsumer) ConsumerGroup() string { return c.group }

// Run consumes until ctx is cancelled or a fatal transport error occurs.
// See eventlog.EventConsumer for delivery and ack semantics.
func (c *EventConsumer) Run(ctx context.Context, handle func(context.Context, eventlog.Event) error) error {
	if handle == nil {
		return errors.New("event consumer handle is nil")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		partitions, err := c.listPartitionsWithWork(ctx)
		if err != nil {
			return err
		}
		if len(partitions) == 0 {
			if err := sleepContext(ctx, c.idle); err != nil {
				return err
			}
			continue
		}

		progressed := false
		handlerFailed := false
		for _, partitionKey := range partitions {
			if err := ctx.Err(); err != nil {
				return err
			}
			outcome, err := c.tryProcessPartition(ctx, partitionKey, handle)
			if err != nil {
				return err
			}
			switch outcome {
			case partitionOutcomeProgressed:
				progressed = true
			case partitionOutcomeHandlerError:
				handlerFailed = true
				progressed = true // work was attempted; avoid pure idle spin
			case partitionOutcomeLocked, partitionOutcomeIdle:
				// try next partition
			}
		}

		if !progressed {
			if err := sleepContext(ctx, c.idle); err != nil {
				return err
			}
			continue
		}
		if handlerFailed && c.errorBackoff > 0 {
			if err := sleepContext(ctx, c.errorBackoff); err != nil {
				return err
			}
		}
	}
}

type partitionOutcome int

const (
	partitionOutcomeIdle partitionOutcome = iota
	partitionOutcomeLocked
	partitionOutcomeProgressed
	partitionOutcomeHandlerError
)

// tryProcessPartition attempts an exclusive advisory lock for the partition
// and, if acquired, drains pending events in sequence order.
func (c *EventConsumer) tryProcessPartition(
	ctx context.Context,
	partitionKey string,
	handle func(context.Context, eventlog.Event) error,
) (partitionOutcome, error) {
	conn, err := c.store.pool.Acquire(ctx)
	if err != nil {
		return partitionOutcomeIdle, fmt.Errorf("acquire connection for partition lock: %w", err)
	}
	defer conn.Release()

	locked, err := tryPartitionAdvisoryLock(ctx, conn, c.group, partitionKey)
	if err != nil {
		return partitionOutcomeIdle, err
	}
	if !locked {
		return partitionOutcomeLocked, nil
	}
	defer func() {
		// Best-effort unlock; connection return also drops session locks if the
		// pool discards the conn, but unlock keeps the conn reusable cleanly.
		_ = unlockPartitionAdvisoryLock(context.Background(), conn, c.group, partitionKey)
	}()

	handledAny := false
	for {
		if err := ctx.Err(); err != nil {
			if handledAny {
				return partitionOutcomeProgressed, err
			}
			return partitionOutcomeIdle, err
		}

		afterSeq, err := c.loadCheckpointSequence(ctx, partitionKey)
		if err != nil {
			return partitionOutcomeIdle, err
		}
		event, ok, err := c.loadNextEvent(ctx, partitionKey, afterSeq)
		if err != nil {
			return partitionOutcomeIdle, err
		}
		if !ok {
			if handledAny {
				return partitionOutcomeProgressed, nil
			}
			return partitionOutcomeIdle, nil
		}

		handleErr := c.deliverOne(ctx, event, handle)
		handledAny = true
		if handleErr != nil {
			// Transport/fatal errors end Run; handler errors redeliver later.
			if isHandlerError(handleErr) {
				return partitionOutcomeHandlerError, nil
			}
			return partitionOutcomeIdle, handleErr
		}
	}
}

// deliverOne runs handle and, on success, UPSERTs the checkpoint in one
// serializable transaction. Handler errors are wrapped so Run can distinguish
// redelivery from fatal transport failures.
func (c *EventConsumer) deliverOne(
	ctx context.Context,
	event eventlog.Event,
	handle func(context.Context, eventlog.Event) error,
) error {
	seq, err := SequenceFromPosition(event.Position)
	if err != nil {
		return fmt.Errorf("event consumer position: %w", err)
	}
	txErr := c.store.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := handle(txCtx, event); err != nil {
			return &handlerError{err: err}
		}
		return c.upsertCheckpoint(txCtx, event.PartitionKey, seq)
	})
	if txErr == nil {
		return nil
	}
	var he *handlerError
	if errors.As(txErr, &he) {
		return txErr
	}
	// Commit/begin failures are transport-level.
	return fmt.Errorf("event consumer deliver: %w", txErr)
}

type handlerError struct {
	err error
}

func (e *handlerError) Error() string { return e.err.Error() }
func (e *handlerError) Unwrap() error { return e.err }

func isHandlerError(err error) bool {
	var he *handlerError
	return errors.As(err, &he)
}

func (c *EventConsumer) listPartitionsWithWork(ctx context.Context) ([]string, error) {
	// position_token is the decimal encoding of the last consumed global
	// sequence. Rows without a checkpoint start at sequence 0 (all events).
	// Invalid tokens are treated as 0 so a corrupt cursor fails open to
	// redelivery rather than skipping; DecodeSequenceToken still guards the
	// load path used before each event.
	const q = `
		SELECT e.` + EventLogColPartitionKey + `
		FROM ` + EventLogTable + ` e
		LEFT JOIN ` + EventConsumerCheckpointsTable + ` c
		  ON c.` + CheckpointColPartitionKey + ` = e.` + EventLogColPartitionKey + `
		 AND c.` + CheckpointColConsumerGroup + ` = $1
		WHERE e.` + EventLogColSequence + ` > COALESCE(
			CASE
				WHEN c.` + CheckpointColPositionToken + ` ~ '^[1-9][0-9]*$'
				THEN c.` + CheckpointColPositionToken + `::bigint
			END,
			0
		)
		GROUP BY e.` + EventLogColPartitionKey + `
		ORDER BY MIN(e.` + EventLogColSequence + `) ASC`

	rows, err := c.store.pool.Query(ctx, q, c.group)
	if err != nil {
		return nil, fmt.Errorf("list partitions with work: %w", err)
	}
	defer rows.Close()

	var partitions []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan partition with work: %w", err)
		}
		partitions = append(partitions, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate partitions with work: %w", err)
	}
	return partitions, nil
}

func (c *EventConsumer) loadCheckpointSequence(ctx context.Context, partitionKey string) (int64, error) {
	var token *string
	err := c.store.pool.QueryRow(ctx, `
		SELECT `+CheckpointColPositionToken+`
		FROM `+EventConsumerCheckpointsTable+`
		WHERE `+CheckpointColConsumerGroup+` = $1
		  AND `+CheckpointColPartitionKey+` = $2`,
		c.group, partitionKey,
	).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load consumer checkpoint for %q: %w", partitionKey, err)
	}
	if token == nil || strings.TrimSpace(*token) == "" {
		return 0, nil
	}
	seq, err := DecodeSequenceToken(*token)
	if err != nil {
		return 0, fmt.Errorf("consumer checkpoint for group %q partition %q: %w", c.group, partitionKey, err)
	}
	return seq, nil
}

func (c *EventConsumer) loadNextEvent(ctx context.Context, partitionKey string, afterSequence int64) (eventlog.Event, bool, error) {
	var (
		sequence    int64
		partKey     string
		idempotency string
		eventType   string
		payload     []byte
		appendedAt  time.Time
	)
	err := c.store.pool.QueryRow(ctx, `
		SELECT `+EventLogColSequence+`, `+EventLogColPartitionKey+`, `+EventLogColIdempotencyKey+`,
		       `+EventLogColEventType+`, `+EventLogColPayload+`, `+EventLogColAppendedAt+`
		FROM `+EventLogTable+`
		WHERE `+EventLogColPartitionKey+` = $1
		  AND `+EventLogColSequence+` > $2
		ORDER BY `+EventLogColSequence+` ASC
		LIMIT 1`,
		partitionKey, afterSequence,
	).Scan(&sequence, &partKey, &idempotency, &eventType, &payload, &appendedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventlog.Event{}, false, nil
	}
	if err != nil {
		return eventlog.Event{}, false, fmt.Errorf("load next event for %q after %d: %w", partitionKey, afterSequence, err)
	}
	if payload == nil {
		payload = []byte{}
	}
	ev := eventlog.Event{
		EventEnvelope: eventlog.EventEnvelope{
			PartitionKey:   partKey,
			IdempotencyKey: idempotency,
			Type:           eventType,
			Payload:        payload,
		},
		Position:  PositionForSequence(partKey, sequence),
		Sequence:  uint64(sequence),
		Timestamp: appendedAt.UTC(),
	}
	return ev, true, nil
}

func (c *EventConsumer) upsertCheckpoint(ctx context.Context, partitionKey string, sequence int64) error {
	if sequence < 1 {
		return fmt.Errorf("checkpoint sequence must be positive, got %d", sequence)
	}
	exec, err := c.store.executor(ctx)
	if err != nil {
		return err
	}
	token := EncodeSequenceToken(sequence)
	_, err = exec.Exec(ctx, `
		INSERT INTO `+EventConsumerCheckpointsTable+`
			(`+CheckpointColConsumerGroup+`, `+CheckpointColPartitionKey+`, `+CheckpointColPositionToken+`, `+CheckpointColUpdatedAt+`)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (`+CheckpointColConsumerGroup+`, `+CheckpointColPartitionKey+`) DO UPDATE
		SET `+CheckpointColPositionToken+` = EXCLUDED.`+CheckpointColPositionToken+`,
		    `+CheckpointColUpdatedAt+` = EXCLUDED.`+CheckpointColUpdatedAt+``,
		c.group, partitionKey, token,
	)
	if err != nil {
		return fmt.Errorf("upsert consumer checkpoint: %w", err)
	}
	return nil
}

func tryPartitionAdvisoryLock(ctx context.Context, conn *pgxpool.Conn, group, partitionKey string) (bool, error) {
	var locked bool
	// Two-int form namespaces by consumer group so independent groups never
	// block each other on the same partition_key.
	err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtext($1), hashtext($2))`,
		group, partitionKey,
	).Scan(&locked)
	if err != nil {
		return false, fmt.Errorf("try advisory lock for %q/%q: %w", group, partitionKey, err)
	}
	return locked, nil
}

func unlockPartitionAdvisoryLock(ctx context.Context, conn *pgxpool.Conn, group, partitionKey string) error {
	var unlocked bool
	err := conn.QueryRow(ctx,
		`SELECT pg_advisory_unlock(hashtext($1), hashtext($2))`,
		group, partitionKey,
	).Scan(&unlocked)
	if err != nil {
		return fmt.Errorf("advisory unlock for %q/%q: %w", group, partitionKey, err)
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
