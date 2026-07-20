package usage

import (
	"sync"
	"testing"
	"time"
)

func TestNewAccumulatorStartsEmpty(t *testing.T) {
	since := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	accumulator := NewAccumulator(since)
	snapshot := accumulator.Snapshot()
	if snapshot.InputTokens != 0 || snapshot.OutputTokens != 0 || snapshot.EstimatedSpendUSD != 0 || snapshot.LiveRuns != 0 {
		t.Fatalf("fresh accumulator snapshot = %#v, want all zero", snapshot)
	}
	if !snapshot.Since.Equal(since) {
		t.Fatalf("Since = %v, want %v", snapshot.Since, since)
	}
}

func TestRecordAccumulatesKnownModel(t *testing.T) {
	accumulator := NewAccumulator(time.Now())
	accumulator.Record("gpt-5.6", 1_000_000, 0)
	accumulator.Record("gpt-5.6", 0, 1_000_000)

	snapshot := accumulator.Snapshot()
	if snapshot.InputTokens != 1_000_000 {
		t.Fatalf("InputTokens = %d, want 1_000_000", snapshot.InputTokens)
	}
	if snapshot.OutputTokens != 1_000_000 {
		t.Fatalf("OutputTokens = %d, want 1_000_000", snapshot.OutputTokens)
	}
	if snapshot.LiveRuns != 2 {
		t.Fatalf("LiveRuns = %d, want 2", snapshot.LiveRuns)
	}
	// 1M input tokens @ $1.25/1M + 1M output tokens @ $10.00/1M = $11.25
	wantSpend := 11.25
	if diff := snapshot.EstimatedSpendUSD - wantSpend; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("EstimatedSpendUSD = %v, want %v", snapshot.EstimatedSpendUSD, wantSpend)
	}
}

func TestRecordUnknownModelUsesDefaultPricing(t *testing.T) {
	known := NewAccumulator(time.Now())
	known.Record("gpt-5.6", 1000, 1000)

	unknown := NewAccumulator(time.Now())
	unknown.Record("some-future-model", 1000, 1000)

	knownSpend := known.Snapshot().EstimatedSpendUSD
	unknownSpend := unknown.Snapshot().EstimatedSpendUSD
	if knownSpend <= 0 || unknownSpend <= 0 {
		t.Fatalf("expected non-zero spend for both known and unknown models: known=%v unknown=%v", knownSpend, unknownSpend)
	}
	if diff := knownSpend - unknownSpend; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("unknown model spend = %v, want default pricing to match known model spend %v", unknownSpend, knownSpend)
	}
}

func TestRecordClampsNegativeTokens(t *testing.T) {
	accumulator := NewAccumulator(time.Now())
	accumulator.Record("gpt-5.6", -5, -5)
	snapshot := accumulator.Snapshot()
	if snapshot.InputTokens != 0 || snapshot.OutputTokens != 0 || snapshot.EstimatedSpendUSD != 0 {
		t.Fatalf("negative tokens were not clamped: %#v", snapshot)
	}
	if snapshot.LiveRuns != 1 {
		t.Fatalf("LiveRuns = %d, want 1 (call still counts as a run)", snapshot.LiveRuns)
	}
}

func TestSnapshotIsSafeForConcurrentRecordAndRead(t *testing.T) {
	accumulator := NewAccumulator(time.Now())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			accumulator.Record("gpt-5.6", 10, 10)
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = accumulator.Snapshot()
		}()
	}
	wg.Wait()

	snapshot := accumulator.Snapshot()
	if snapshot.LiveRuns != 50 {
		t.Fatalf("LiveRuns = %d, want 50", snapshot.LiveRuns)
	}
	if snapshot.InputTokens != 500 || snapshot.OutputTokens != 500 {
		t.Fatalf("tokens = input:%d output:%d, want 500/500", snapshot.InputTokens, snapshot.OutputTokens)
	}
}

func TestNilAccumulatorIsSafe(t *testing.T) {
	var accumulator *Accumulator
	accumulator.Record("gpt-5.6", 10, 10) // must not panic
	if snapshot := accumulator.Snapshot(); snapshot != (Snapshot{}) {
		t.Fatalf("nil accumulator snapshot = %#v, want zero value", snapshot)
	}
}
