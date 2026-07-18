package stream

import "testing"

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
