// Package stream provides the bounded in-process event broker used by the
// single-instance demo's server-sent-events endpoint.
package stream

import (
	"sync"
)

// Event is a named server-sent event payload. Data must be JSON-marshalable by
// the HTTP surface that writes it to the wire.
type Event struct {
	Name string
	Data any
}

// Broker fans out best-effort read-model notifications to local subscribers.
// A slow client cannot stall the projector or a request path: it retains at
// most one pending notification and receives a fresh snapshot on reconnect.
type Broker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan Event
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
	return &Broker{subscribers: make(map[uint64]chan Event)}
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
// make reconnects and subsequent updates self-healing for this demo.
func (b *Broker) Publish(event Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
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
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}

func closedEvents() <-chan Event {
	ch := make(chan Event)
	close(ch)
	return ch
}
