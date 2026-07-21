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
	"sync"
	"time"

	"mosaic.local/mosaic/internal/eventlog"
)

// Compile-time: Log is an EventLog.
var _ eventlog.EventLog = (*Log)(nil)

// Log is a thread-safe, idempotent in-memory EventLog.
// IdempotencyKey is global to the log (first-wins across partitions).
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

// Append records e. Re-appending the same IdempotencyKey is a successful no-op
// (first-wins payload/type; key scope is global). Identity fields are validated
// via [eventlog.ValidateEnvelope].
func (l *Log) Append(ctx context.Context, e eventlog.EventEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil {
		return fmt.Errorf("memory event log is nil")
	}
	env, err := eventlog.ValidateEnvelope(e)
	if err != nil {
		return err
	}
	payload := env.Payload
	if payload == nil {
		payload = []byte{}
	} else {
		// Copy so callers cannot mutate stored bytes.
		payload = append([]byte(nil), payload...)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.seen[env.IdempotencyKey]; exists {
		return nil
	}
	l.seq[env.PartitionKey]++
	seq := l.seq[env.PartitionKey]
	token := fmt.Sprintf("%s:%d", env.PartitionKey, seq)
	l.seen[env.IdempotencyKey] = struct{}{}
	l.events = append(l.events, eventlog.Event{
		EventEnvelope: eventlog.EventEnvelope{
			PartitionKey:   env.PartitionKey,
			IdempotencyKey: env.IdempotencyKey,
			Type:           env.Type,
			Payload:        payload,
		},
		Position:  eventlog.NewPosition(env.PartitionKey, token),
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
// Each event's Payload is a defensive copy so callers cannot mutate storage.
func (l *Log) Events() []eventlog.Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]eventlog.Event, len(l.events))
	for i, e := range l.events {
		out[i] = e
		if e.Payload != nil {
			// append to a non-nil empty base so zero-length payloads stay non-nil.
			out[i].Payload = append([]byte{}, e.Payload...)
		}
	}
	return out
}
