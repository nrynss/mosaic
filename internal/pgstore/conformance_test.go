package pgstore

import (
	"mosaic.local/mosaic/internal/eventlog"
	"mosaic.local/mosaic/internal/eventlog/eventlogtest"
	"os"
	"testing"
)

func TestEventLogConformance(t *testing.T) {
	if os.Getenv("MOSAIC_TEST_PG_DSN") == "" {
		t.Skip("MOSAIC_TEST_PG_DSN not set")
	}
	eventlogtest.RunConformanceTests(t, func() (eventlog.EventLog, eventlog.EventConsumer, func()) {
		s := newTestStore(t)
		consumer := NewEventConsumer(s, ConsumerConfig{})
		return s, consumer, func() {}
	})
}
