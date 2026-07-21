package pgstore

import (
	"fmt"
	"strconv"
	"strings"

	"mosaic.local/mosaic/internal/eventlog"
)

// Schema names for the event-spine transport tables created by migration
// 0002_event_spine.sql. B2 (EventLog.Append) and B3 (EventConsumer) must use
// these identifiers so the log and checkpoint surfaces stay aligned.
const (
	// EventLogTable is the durable append log (partition_key + monotonic sequence).
	EventLogTable = "event_log"

	// EventConsumerCheckpointsTable scopes a consumer group + partition to an
	// opaque position token. B3 advances it atomically with projection.
	EventConsumerCheckpointsTable = "event_consumer_checkpoints"

	// EventLogPartitionSequenceIndex supports ordered per-partition consume:
	// WHERE partition_key = $1 AND sequence > $2 ORDER BY sequence.
	EventLogPartitionSequenceIndex = "event_log_partition_sequence_idx"
)

// Event-log column names (agreed with B2).
const (
	EventLogColSequence       = "sequence"
	EventLogColPartitionKey   = "partition_key"
	EventLogColIdempotencyKey = "idempotency_key"
	EventLogColEventType      = "event_type"
	EventLogColPayload        = "payload"
	EventLogColAppendedAt     = "appended_at"
)

// Consumer-checkpoint column names (agreed with B3).
const (
	CheckpointColConsumerGroup = "consumer_group"
	CheckpointColPartitionKey  = "partition_key"
	CheckpointColPositionToken = "position_token"
	CheckpointColUpdatedAt     = "updated_at"
)

// EncodeSequenceToken encodes a global event_log.sequence value as an opaque
// eventlog.Position token. The Postgres backend uses the decimal representation
// of the sequence (no leading zeros, base 10). Callers outside this backend
// must treat the token as opaque and never do arithmetic on it.
//
// Convention:
//   - After successfully handling the row with sequence N, the consumer
//     persists position_token = EncodeSequenceToken(N), meaning "consumed
//     through sequence N."
//   - The next fetch is WHERE partition_key = $1 AND sequence > N.
//   - A missing checkpoint (or a zero Position) means start from the beginning
//     of that partition — there is no token for "before the first event."
func EncodeSequenceToken(sequence int64) string {
	return strconv.FormatInt(sequence, 10)
}

// DecodeSequenceToken parses a token produced by EncodeSequenceToken.
// Empty tokens and non-decimal strings are rejected so a corrupt checkpoint
// fails closed rather than silently rewinding the consumer.
func DecodeSequenceToken(token string) (int64, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, fmt.Errorf("empty event-spine position token")
	}
	sequence, err := strconv.ParseInt(token, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("decode event-spine position token %q: %w", token, err)
	}
	if sequence < 1 {
		return 0, fmt.Errorf("event-spine position token %q is not a positive sequence", token)
	}
	return sequence, nil
}

// PositionForSequence builds the eventlog.Position a Postgres EventConsumer
// should attach to a delivered Event for the given partition and global
// sequence. Application code receives positions; only the backend mints them.
func PositionForSequence(partitionKey string, sequence int64) eventlog.Position {
	return eventlog.NewPosition(partitionKey, EncodeSequenceToken(sequence))
}

// SequenceFromPosition extracts the global sequence from a Postgres-minted
// position. It rejects positions whose token is not a valid sequence encoding.
// The zero Position (before the first event) returns (0, nil) so callers can
// treat it as "start of partition" without a special case on the token.
func SequenceFromPosition(p eventlog.Position) (int64, error) {
	if p.IsZero() || strings.TrimSpace(p.Token()) == "" {
		return 0, nil
	}
	return DecodeSequenceToken(p.Token())
}
