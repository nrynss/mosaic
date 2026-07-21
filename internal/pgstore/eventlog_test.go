package pgstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"mosaic.local/mosaic/internal/eventlog"
)

func TestEventLogAppendInsertsAndAssignsSequence(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.Append(ctx, eventlog.EventEnvelope{
		PartitionKey:   "incident-1",
		IdempotencyKey: "evt-1",
		Type:           "raw.ingested",
		Payload:        []byte(`{"hello":"world"}`),
	}); err != nil {
		t.Fatal(err)
	}

	var (
		sequence     int64
		partitionKey string
		idempotency  string
		eventType    string
		payload      []byte
	)
	err := s.Pool().QueryRow(ctx, `SELECT sequence, partition_key, idempotency_key, event_type, payload
		FROM event_log WHERE idempotency_key = $1`, "evt-1").Scan(
		&sequence, &partitionKey, &idempotency, &eventType, &payload,
	)
	if err != nil {
		t.Fatalf("read appended row: %v", err)
	}
	if sequence < 1 {
		t.Fatalf("sequence = %d, want >= 1", sequence)
	}
	if partitionKey != "incident-1" || idempotency != "evt-1" || eventType != "raw.ingested" {
		t.Fatalf("row fields = %q %q %q", partitionKey, idempotency, eventType)
	}
	if string(payload) != `{"hello":"world"}` {
		t.Fatalf("payload = %q", payload)
	}
}

func TestEventLogAppendIdempotentReappendIsNoOp(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	env := eventlog.EventEnvelope{
		PartitionKey:   "incident-1",
		IdempotencyKey: "same-key",
		Type:           "raw.ingested",
		Payload:        []byte("first"),
	}
	if err := s.Append(ctx, env); err != nil {
		t.Fatal(err)
	}
	// Different payload/type must still be a no-op: the key is the identity.
	retry := env
	retry.Type = "raw.other"
	retry.Payload = []byte("second")
	if err := s.Append(ctx, retry); err != nil {
		t.Fatalf("re-append returned error: %v", err)
	}

	var count int
	if err := s.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM event_log WHERE idempotency_key = $1`, "same-key").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count for same-key = %d, want 1", count)
	}

	var eventType string
	var payload []byte
	if err := s.Pool().QueryRow(ctx, `SELECT event_type, payload FROM event_log WHERE idempotency_key = $1`, "same-key").Scan(&eventType, &payload); err != nil {
		t.Fatal(err)
	}
	if eventType != "raw.ingested" || string(payload) != "first" {
		t.Fatalf("first-wins lost: type=%q payload=%q", eventType, payload)
	}
}

func TestEventLogAppendConcurrentSameIdempotencyKey(t *testing.T) {
	// P2: N goroutines same IdempotencyKey → one row, all Append return nil.
	ctx := context.Background()
	s := newTestStore(t)

	const n = 32
	const key = "stress-same-key"
	var (
		wg       sync.WaitGroup
		errCount atomic.Int64
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			err := s.Append(ctx, eventlog.EventEnvelope{
				PartitionKey:   "incident-1",
				IdempotencyKey: key,
				Type:           "raw.ingested",
				Payload:        []byte(fmt.Sprintf("payload-%d", i)),
			})
			if err != nil {
				errCount.Add(1)
				t.Errorf("Append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if errCount.Load() != 0 {
		t.Fatalf("%d concurrent Append calls failed", errCount.Load())
	}

	var count int
	if err := s.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM event_log WHERE idempotency_key = $1`, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1 after concurrent Append", count)
	}
}

func TestIsIdempotencyUniqueViolation(t *testing.T) {
	t.Parallel()

	if isIdempotencyUniqueViolation(errors.New("not a pg error")) {
		t.Fatal("plain error should not match")
	}
	if isIdempotencyUniqueViolation(&pgconn.PgError{Code: "23503", ConstraintName: eventLogIdempotencyConstraint}) {
		t.Fatal("non-23505 should not match")
	}
	if !isIdempotencyUniqueViolation(&pgconn.PgError{
		Code:           postgresUniqueViolation,
		ConstraintName: eventLogIdempotencyConstraint,
	}) {
		t.Fatal("exact idempotency constraint should match")
	}
	if !isIdempotencyUniqueViolation(&pgconn.PgError{
		Code:           postgresUniqueViolation,
		ConstraintName: "custom_idempotency_key_uidx",
	}) {
		t.Fatal("constraint name containing idempotency_key should match")
	}
	if isIdempotencyUniqueViolation(&pgconn.PgError{
		Code:           postgresUniqueViolation,
		ConstraintName: "event_log_other_unique",
	}) {
		t.Fatal("unrelated unique constraint must not be treated as idempotent success")
	}
	if !isIdempotencyUniqueViolation(&pgconn.PgError{
		Code:    postgresUniqueViolation,
		Message: `duplicate key value violates unique constraint`,
		Detail:  `Key (idempotency_key)=(x) already exists.`,
	}) {
		t.Fatal("empty ConstraintName with idempotency_key in Detail should match")
	}
}

func TestEventLogAppendMultiPartitionSequences(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Interleave two partitions. Global BIGSERIAL is monotonic, but consumers
	// order only within a partition_key — sequences within each key must still
	// increase with append order.
	envelopes := []eventlog.EventEnvelope{
		{PartitionKey: "p-a", IdempotencyKey: "a1", Type: "t", Payload: []byte("a1")},
		{PartitionKey: "p-b", IdempotencyKey: "b1", Type: "t", Payload: []byte("b1")},
		{PartitionKey: "p-a", IdempotencyKey: "a2", Type: "t", Payload: []byte("a2")},
		{PartitionKey: "p-b", IdempotencyKey: "b2", Type: "t", Payload: []byte("b2")},
	}
	for _, env := range envelopes {
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %q: %v", env.IdempotencyKey, err)
		}
	}

	type row struct {
		idempotency string
		sequence    int64
	}
	readPartition := func(partition string) []row {
		t.Helper()
		rows, err := s.Pool().Query(ctx, `SELECT idempotency_key, sequence FROM event_log
			WHERE partition_key = $1 ORDER BY sequence ASC`, partition)
		if err != nil {
			t.Fatalf("query partition %s: %v", partition, err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.idempotency, &r.sequence); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	partA := readPartition("p-a")
	partB := readPartition("p-b")
	if len(partA) != 2 || partA[0].idempotency != "a1" || partA[1].idempotency != "a2" {
		t.Fatalf("partition a = %#v", partA)
	}
	if len(partB) != 2 || partB[0].idempotency != "b1" || partB[1].idempotency != "b2" {
		t.Fatalf("partition b = %#v", partB)
	}
	if partA[0].sequence >= partA[1].sequence {
		t.Fatalf("partition a sequences not ordered: %d, %d", partA[0].sequence, partA[1].sequence)
	}
	if partB[0].sequence >= partB[1].sequence {
		t.Fatalf("partition b sequences not ordered: %d, %d", partB[0].sequence, partB[1].sequence)
	}
	// Global sequence is shared; interleaved appends place a1 before b1 before a2.
	if !(partA[0].sequence < partB[0].sequence && partB[0].sequence < partA[1].sequence) {
		t.Fatalf("global interleave unexpected: a=%#v b=%#v", partA, partB)
	}
}

func TestEventLogAppendNilPayloadStoredAsEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.Append(ctx, eventlog.EventEnvelope{
		PartitionKey:   "incident-1",
		IdempotencyKey: "nil-payload",
		Type:           "raw.ingested",
		Payload:        nil,
	}); err != nil {
		t.Fatal(err)
	}

	var payload []byte
	if err := s.Pool().QueryRow(ctx, `SELECT payload FROM event_log WHERE idempotency_key = $1`, "nil-payload").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload == nil {
		// pgx may return empty slice or nil for zero-length BYTEA; both are fine
		// as long as the row exists and we did not reject nil input.
		return
	}
	if len(payload) != 0 {
		t.Fatalf("payload len = %d, want 0", len(payload))
	}
}

func TestEventLogAppendValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cases := []struct {
		name string
		env  eventlog.EventEnvelope
	}{
		{
			name: "missing idempotency key",
			env:  eventlog.EventEnvelope{PartitionKey: "p", Type: "t", Payload: []byte("x")},
		},
		{
			name: "whitespace idempotency key",
			env:  eventlog.EventEnvelope{PartitionKey: "p", IdempotencyKey: "  ", Type: "t"},
		},
		{
			name: "missing partition key",
			env:  eventlog.EventEnvelope{IdempotencyKey: "k", Type: "t"},
		},
		{
			name: "missing type",
			env:  eventlog.EventEnvelope{PartitionKey: "p", IdempotencyKey: "k"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Append(ctx, tc.env)
			if !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("error = %v, want ErrInvalidRecord", err)
			}
			var count int
			if err := s.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM event_log`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid append wrote %d rows", count)
			}
		})
	}
}

func TestEventLogMigrationCreatesTables(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for _, table := range []string{"event_log", "event_consumer_checkpoints"} {
		var regclass string
		err := s.Pool().QueryRow(ctx, "SELECT to_regclass($1)::text", table).Scan(&regclass)
		if err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if regclass == "" {
			t.Fatalf("migration did not create %s", table)
		}
	}
}
