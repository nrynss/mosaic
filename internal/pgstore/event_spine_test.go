package pgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"mosaic.local/mosaic/internal/eventlog"
)

func TestEncodeDecodeSequenceToken(t *testing.T) {
	t.Parallel()

	cases := []int64{1, 2, 42, 1_000_000_000_000}
	for _, seq := range cases {
		token := EncodeSequenceToken(seq)
		got, err := DecodeSequenceToken(token)
		if err != nil {
			t.Fatalf("DecodeSequenceToken(%q) for %d: %v", token, seq, err)
		}
		if got != seq {
			t.Fatalf("round-trip sequence = %d, want %d (token %q)", got, seq, token)
		}
	}

	if _, err := DecodeSequenceToken(""); err == nil {
		t.Fatal("DecodeSequenceToken empty token succeeded")
	}
	if _, err := DecodeSequenceToken("  "); err == nil {
		t.Fatal("DecodeSequenceToken blank token succeeded")
	}
	if _, err := DecodeSequenceToken("not-a-number"); err == nil {
		t.Fatal("DecodeSequenceToken non-decimal token succeeded")
	}
	if _, err := DecodeSequenceToken("0"); err == nil {
		t.Fatal("DecodeSequenceToken(0) succeeded; sequences start at 1")
	}
	if _, err := DecodeSequenceToken("-3"); err == nil {
		t.Fatal("DecodeSequenceToken negative token succeeded")
	}
}

func TestPositionForSequenceAndSequenceFromPosition(t *testing.T) {
	t.Parallel()

	pos := PositionForSequence("incident-7", 5)
	if pos.PartitionKey() != "incident-7" {
		t.Fatalf("PartitionKey = %q, want incident-7", pos.PartitionKey())
	}
	if pos.Token() != "5" {
		t.Fatalf("Token = %q, want 5", pos.Token())
	}
	seq, err := SequenceFromPosition(pos)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 5 {
		t.Fatalf("SequenceFromPosition = %d, want 5", seq)
	}

	zero, err := SequenceFromPosition(eventlog.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if zero != 0 {
		t.Fatalf("zero Position sequence = %d, want 0", zero)
	}

	// Partition scoped but empty token is also "start of partition."
	start, err := SequenceFromPosition(eventlog.NewPosition("incident-7", ""))
	if err != nil {
		t.Fatal(err)
	}
	if start != 0 {
		t.Fatalf("empty-token Position sequence = %d, want 0", start)
	}
}

func TestEventSpineMigrationCreatesTablesAndConstraints(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Tables exist after Open/NewFromPool applies 0002.
	for _, table := range []string{EventLogTable, EventConsumerCheckpointsTable} {
		var regclass string
		err := s.Pool().QueryRow(ctx, "SELECT to_regclass($1)::text", table).Scan(&regclass)
		if err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if regclass == "" {
			t.Fatalf("migration did not create %s", table)
		}
	}

	// Index supporting per-partition ordered consume exists.
	var indexName string
	err := s.Pool().QueryRow(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = ANY (current_schemas(false))
		  AND tablename = $1
		  AND indexname = $2`,
		EventLogTable, EventLogPartitionSequenceIndex,
	).Scan(&indexName)
	if err != nil {
		t.Fatalf("look up %s: %v", EventLogPartitionSequenceIndex, err)
	}
	if indexName != EventLogPartitionSequenceIndex {
		t.Fatalf("index name = %q, want %s", indexName, EventLogPartitionSequenceIndex)
	}

	// Insert two events on different partitions; sequences are global and monotonic.
	var seq1, seq2, seq3 int64
	err = s.Pool().QueryRow(ctx, `
		INSERT INTO event_log (partition_key, idempotency_key, event_type, payload)
		VALUES ('incident-a', 'idemp-1', 'raw.received', '\x01')
		RETURNING sequence`).Scan(&seq1)
	if err != nil {
		t.Fatalf("insert event 1: %v", err)
	}
	err = s.Pool().QueryRow(ctx, `
		INSERT INTO event_log (partition_key, idempotency_key, event_type, payload)
		VALUES ('incident-b', 'idemp-2', 'raw.received', '\x02')
		RETURNING sequence`).Scan(&seq2)
	if err != nil {
		t.Fatalf("insert event 2: %v", err)
	}
	err = s.Pool().QueryRow(ctx, `
		INSERT INTO event_log (partition_key, idempotency_key, event_type, payload)
		VALUES ('incident-a', 'idemp-3', 'raw.received', '\x03')
		RETURNING sequence`).Scan(&seq3)
	if err != nil {
		t.Fatalf("insert event 3: %v", err)
	}
	if !(seq1 < seq2 && seq2 < seq3) {
		t.Fatalf("sequences not strictly monotonic: %d, %d, %d", seq1, seq2, seq3)
	}

	// Per-partition ordered consume sees only that key's subsequence.
	rows, err := s.Pool().Query(ctx, `
		SELECT sequence FROM event_log
		WHERE partition_key = $1 AND sequence > $2
		ORDER BY sequence ASC`, "incident-a", int64(0))
	if err != nil {
		t.Fatalf("per-partition query: %v", err)
	}
	defer rows.Close()
	var partitionSeqs []int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			t.Fatalf("scan partition sequence: %v", err)
		}
		partitionSeqs = append(partitionSeqs, seq)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(partitionSeqs) != 2 || partitionSeqs[0] != seq1 || partitionSeqs[1] != seq3 {
		t.Fatalf("incident-a sequences = %v, want [%d %d]", partitionSeqs, seq1, seq3)
	}

	// Unique idempotency_key rejects duplicates (B2 turns this into a no-op).
	_, err = s.Pool().Exec(ctx, `
		INSERT INTO event_log (partition_key, idempotency_key, event_type, payload)
		VALUES ('incident-a', 'idemp-1', 'raw.received', '\x99')`)
	if err == nil {
		t.Fatal("duplicate idempotency_key was accepted")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("duplicate idempotency_key error = %v, want unique_violation", err)
	}

	// Append-only: UPDATE and DELETE on event_log are refused.
	if _, err := s.Pool().Exec(ctx, `UPDATE event_log SET event_type = 'x' WHERE sequence = $1`, seq1); err == nil {
		t.Fatal("event_log UPDATE was accepted")
	}
	if _, err := s.Pool().Exec(ctx, `DELETE FROM event_log WHERE sequence = $1`, seq1); err == nil {
		t.Fatal("event_log DELETE was accepted")
	}

	// Checkpoint PK and mutability: UPSERT advances position_token.
	token1 := EncodeSequenceToken(seq1)
	if _, err := s.Pool().Exec(ctx, `
		INSERT INTO event_consumer_checkpoints (consumer_group, partition_key, position_token)
		VALUES ('projector', 'incident-a', $1)`, token1); err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}
	token3 := EncodeSequenceToken(seq3)
	if _, err := s.Pool().Exec(ctx, `
		INSERT INTO event_consumer_checkpoints (consumer_group, partition_key, position_token, updated_at)
		VALUES ('projector', 'incident-a', $1, now())
		ON CONFLICT (consumer_group, partition_key) DO UPDATE
		SET position_token = EXCLUDED.position_token,
		    updated_at = EXCLUDED.updated_at`, token3); err != nil {
		t.Fatalf("upsert checkpoint: %v", err)
	}
	var storedToken string
	err = s.Pool().QueryRow(ctx, `
		SELECT position_token FROM event_consumer_checkpoints
		WHERE consumer_group = 'projector' AND partition_key = 'incident-a'`).Scan(&storedToken)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if storedToken != token3 {
		t.Fatalf("checkpoint token = %q, want %q", storedToken, token3)
	}

	// Primary key rejects a second row for the same group+partition without UPSERT.
	_, err = s.Pool().Exec(ctx, `
		INSERT INTO event_consumer_checkpoints (consumer_group, partition_key, position_token)
		VALUES ('projector', 'incident-a', '1')`)
	if err == nil {
		t.Fatal("duplicate checkpoint primary key was accepted")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("duplicate checkpoint error = %v, want unique_violation", err)
	}

	// Distinct consumer groups may track the same partition independently.
	if _, err := s.Pool().Exec(ctx, `
		INSERT INTO event_consumer_checkpoints (consumer_group, partition_key, position_token)
		VALUES ('audit-sink', 'incident-a', $1)`, token1); err != nil {
		t.Fatalf("second consumer group checkpoint: %v", err)
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
