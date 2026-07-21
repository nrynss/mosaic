// Package memory provides an in-process EventLog for local SQLite demos and
// tests. Domain data (canonical events, COP checkpoints) stays on the durable
// SQLite store; this log is only the append transport seam so composition can
// exercise EventLog.Append without Postgres.
//
// The interactive progressive path Appends here, then synchronously processes
// the beat via domain ingest+project (not a multi-worker consumer). Multi-worker
// EventConsumer remains the scale path on Postgres.
package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mosaic.local/mosaic/internal/eventlog"
)

// Compile-time: Log is an EventLog.
var _ eventlog.EventLog = (*Log)(nil)

// Log is a thread-safe, idempotent in-memory EventLog.
type Log struct {
	mu     sync.Mutex
	seen   map[string]struct{}
	events []eventlog.Event
	seq    map[string]uint64 // per-partition sequence
}

// New returns an empty memory EventLog.
func New() *Log {
	return &Log{
		seen: make(map[string]struct{}),
		seq:  make(map[string]uint64),
	}
}

// Append records e. Re-appending the same IdempotencyKey is a successful no-op.
func (l *Log) Append(ctx context.Context, e eventlog.EventEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil {
		return fmt.Errorf("memory event log is nil")
	}
	pk := strings.TrimSpace(e.PartitionKey)
	ik := strings.TrimSpace(e.IdempotencyKey)
	typ := strings.TrimSpace(e.Type)
	if pk == "" {
		return fmt.Errorf("PartitionKey is required")
	}
	if ik == "" {
		return fmt.Errorf("IdempotencyKey is required")
	}
	if typ == "" {
		return fmt.Errorf("Type is required")
	}
	payload := e.Payload
	if payload == nil {
		payload = []byte{}
	} else {
		// Copy so callers cannot mutate stored bytes.
		payload = append([]byte(nil), payload...)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.seen[ik]; exists {
		return nil
	}
	l.seq[pk]++
	seq := l.seq[pk]
	token := fmt.Sprintf("%s:%d", pk, seq)
	l.seen[ik] = struct{}{}
	l.events = append(l.events, eventlog.Event{
		EventEnvelope: eventlog.EventEnvelope{
			PartitionKey:   pk,
			IdempotencyKey: ik,
			Type:           typ,
			Payload:        payload,
		},
		Position:  eventlog.NewPosition(pk, token),
		Sequence:  seq,
		Timestamp: time.Now().UTC(),
	})
	return nil
}

// Len returns how many unique events have been appended (tests/diagnostics).
func (l *Log) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// Events returns a copy of appended events in append order (tests).
func (l *Log) Events() []eventlog.Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]eventlog.Event, len(l.events))
	copy(out, l.events)
	return out
}
