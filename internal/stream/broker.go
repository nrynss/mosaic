// Package stream provides the bounded in-process event broker used by the
// single-instance demo's server-sent-events endpoint.
package stream

import (
	"sync"
	"time"
)

// Event is a named server-sent event payload. Data must be JSON-marshalable by
// the HTTP surface that writes it to the wire.
type Event struct {
	Name string
	Data any
}

// Publication is the bounded metadata retained for the last locally published
// event. It intentionally excludes Event.Data, which can contain a read-model
// payload that does not belong in operations telemetry.
type Publication struct {
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
}

// Metadata is a point-in-time, process-local broker observation. The local
// broker is deliberately not a shared multi-instance notification mechanism.
type Metadata struct {
	SubscriberCount int          `json:"subscriber_count"`
	LastPublished   *Publication `json:"last_published,omitempty"`
}

// Broker fans out best-effort read-model notifications to local subscribers.
// A slow client cannot stall the projector or a request path: it retains at
// most one pending notification and receives a fresh snapshot on reconnect.
type Broker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan Event
	clock       func() time.Time
	last        *Publication
}

// Subscription is a caller-owned stream registration. Call Cancel when the
// HTTP request ends to remove the subscriber without a background goroutine.
type Subscription struct {
	Events <-chan Event

	once   sync.Once
	cancel func()
}

// NewBroker returns an empty local broker.
func NewBroker() *Broker {
	return NewBrokerWithClock(time.Now)
}

// NewBrokerWithClock creates a local broker with a caller-supplied clock. It
// exists for deterministic diagnostics tests; composition should normally use
// NewBroker.
func NewBrokerWithClock(clock func() time.Time) *Broker {
	if clock == nil {
		clock = time.Now
	}
	return &Broker{subscribers: make(map[uint64]chan Event), clock: clock}
}

// Subscribe registers a bounded local subscriber. The caller must call
// Cancel; this deliberately keeps cancellation lifecycle at the HTTP boundary.
func (b *Broker) Subscribe() *Subscription {
	if b == nil {
		return &Subscription{Events: closedEvents()}
	}

	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan Event, 1)
	b.subscribers[id] = ch
	b.mu.Unlock()

	return &Subscription{
		Events: ch,
		cancel: func() {
			b.mu.Lock()
			if existing, ok := b.subscribers[id]; ok {
				delete(b.subscribers, id)
				close(existing)
			}
			b.mu.Unlock()
		},
	}
}

// Cancel removes the subscriber. It is safe to call more than once.
func (s *Subscription) Cancel() {
	if s == nil || s.cancel == nil {
		return
	}
	s.once.Do(s.cancel)
}

// Publish offers an event to every current subscriber without blocking. A
// subscriber with an unread pending event is intentionally skipped; snapshots
// give reconnects and subsequent updates a fresh deterministic baseline.
func (b *Broker) Publish(event Event) {
	if b == nil || event.Name == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last = &Publication{Name: event.Name, PublishedAt: b.clock().UTC()}
	for _, subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

// SubscriberCount is useful for deterministic lifecycle tests and local
// diagnostics. It does not expose subscriber identities.
func (b *Broker) SubscriberCount() int {
	return b.Metadata().SubscriberCount
}

// Metadata returns only bounded local-stream telemetry. The returned
// Publication is copied so callers cannot mutate broker state.
func (b *Broker) Metadata() Metadata {
	if b == nil {
		return Metadata{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	metadata := Metadata{SubscriberCount: len(b.subscribers)}
	if b.last != nil {
		last := *b.last
		metadata.LastPublished = &last
	}
	return metadata
}

func closedEvents() <-chan Event {
	ch := make(chan Event)
	close(ch)
	return ch
}
