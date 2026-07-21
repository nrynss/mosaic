package store

import (
	"context"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
)

func TestMemoryCOPRoundTripByKey(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryCOP()
	_, found, err := m.LoadCOPReadModelKey(ctx, "sim-a")
	if err != nil || found {
		t.Fatalf("empty load found=%v err=%v", found, err)
	}
	want := contracts.ProjectionResult{
		StateRevision: 9,
		ProjectedAt:   time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
		COP:           map[string]any{"rev": float64(9)},
	}
	if err := m.SaveCOPReadModelKey(ctx, "sim-a", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found, err := m.LoadCOPReadModelKey(ctx, "sim-a")
	if err != nil || !found {
		t.Fatalf("load found=%v err=%v", found, err)
	}
	if got.StateRevision != 9 || got.COP["rev"] != float64(9) {
		t.Fatalf("got %#v", got)
	}
	// Mutation of returned COP must not clobber store.
	got.COP["rev"] = "mutated"
	again, _, _ := m.LoadCOPReadModelKey(ctx, "sim-a")
	if again.COP["rev"] != float64(9) {
		t.Fatalf("store mutated via load copy: %#v", again.COP)
	}
	// Default key remains empty when only session keys written.
	if _, found, _ := m.LoadCOPReadModel(ctx); found {
		t.Fatal("default key should be empty")
	}
}

func TestMemoryCOPDefaultKey(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryCOP()
	if err := m.SaveCOPReadModel(ctx, contracts.ProjectionResult{StateRevision: 1, COP: map[string]any{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found, err := m.LoadCOPReadModel(ctx)
	if err != nil || !found || got.StateRevision != 1 {
		t.Fatalf("got %#v found=%v err=%v", got, found, err)
	}
}
