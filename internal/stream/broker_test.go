package stream

import (
	"sync"
	"testing"
	"time"
)

func TestSubscriptionCancelRemovesSubscriber(t *testing.T) {
	broker := NewBroker()
	subscription := broker.Subscribe()
	if got := broker.SubscriberCount(); got != 1 {
		t.Fatalf("subscriber count = %d, want 1", got)
	}

	subscription.Cancel()
	subscription.Cancel()
	if got := broker.SubscriberCount(); got != 0 {
		t.Fatalf("subscriber count after cancel = %d, want 0", got)
	}
	if _, open := <-subscription.Events; open {
		t.Fatal("canceled subscription remains open")
	}
}

func TestPublishDoesNotBlockOnSlowSubscriber(t *testing.T) {
	broker := NewBroker()
	subscription := broker.Subscribe()
	defer subscription.Cancel()

	broker.Publish(Event{Name: "cop.snapshot", Data: "first"})
	broker.Publish(Event{Name: "cop.snapshot", Data: "second"})

	event := <-subscription.Events
	if event.Data != "first" {
		t.Fatalf("first buffered event = %#v, want first", event)
	}
}

func TestMetadataTracksOnlyLastEventNameAndTimestamp(t *testing.T) {
	publishedAt := time.Date(2026, 7, 19, 8, 30, 0, 0, time.UTC)
	broker := NewBrokerWithClock(func() time.Time { return publishedAt })
	if metadata := broker.Metadata(); metadata.SubscriberCount != 0 || metadata.LastPublished != nil {
		t.Fatalf("initial metadata = %#v", metadata)
	}

	broker.Publish(Event{Name: "cop.updated", Data: map[string]string{"payload": "not telemetry"}})
	metadata := broker.Metadata()
	if metadata.LastPublished == nil || metadata.LastPublished.Name != "cop.updated" || !metadata.LastPublished.PublishedAt.Equal(publishedAt) {
		t.Fatalf("last publication = %#v", metadata.LastPublished)
	}
	metadata.LastPublished.Name = "mutated-by-caller"
	if got := broker.Metadata().LastPublished.Name; got != "cop.updated" {
		t.Fatalf("caller changed broker metadata: %q", got)
	}
}

func TestMetadataIsSafeDuringPublishAndSubscriptionLifecycle(t *testing.T) {
	broker := NewBroker()
	var group sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			for iteration := 0; iteration < 50; iteration++ {
				subscription := broker.Subscribe()
				broker.Publish(Event{Name: "cop.updated", Data: index})
				_ = broker.Metadata()
				subscription.Cancel()
			}
		}(worker)
	}
	group.Wait()
	if metadata := broker.Metadata(); metadata.SubscriberCount != 0 || metadata.LastPublished == nil {
		t.Fatalf("final metadata = %#v", metadata)
	}
}
