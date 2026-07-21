package pgstore

import (
	"os"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/eventlog"
	"mosaic.local/mosaic/internal/eventlog/eventlogtest"
)

func TestEventLogConformance(t *testing.T) {
	if os.Getenv("MOSAIC_TEST_PG_DSN") == "" {
		t.Skip("MOSAIC_TEST_PG_DSN not set")
	}

	eventlogtest.RunConformanceTests(t, func() (eventlog.EventLog, eventlog.EventConsumer, eventlog.EventBus, func()) {
		s := newTestStore(t)
		consumer := NewEventConsumer(s, ConsumerConfig{
			ConsumerGroup: "conformance",
			IdleInterval:  10 * time.Millisecond,
			ErrorBackoff:  -1,
		})
		bus := NewEventBus(s.Pool())
		return s, consumer, bus, func() {}
	}, eventlogtest.WithSharedConsumers(func() (eventlog.EventLog, eventlog.EventConsumer, eventlog.EventConsumer, eventlog.EventBus, func()) {
		// One store, two consumers, same group — multi-worker partition isolation.
		s := newTestStore(t)
		cfg := ConsumerConfig{
			ConsumerGroup: "conformance-mw",
			IdleInterval:  10 * time.Millisecond,
			ErrorBackoff:  -1,
		}
		c1 := NewEventConsumer(s, cfg)
		c2 := NewEventConsumer(s, cfg)
		bus := NewEventBus(s.Pool())
		return s, c1, c2, bus, func() {}
	}))
}
