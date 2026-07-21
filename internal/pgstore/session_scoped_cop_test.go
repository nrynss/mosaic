package pgstore

import (
	"context"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
)

// memoryCOP is an in-memory COPReadModelRepository for unit tests without Postgres.
type memoryCOP struct {
	byKey map[string]contracts.ProjectionResult
}

func newMemoryCOP() *memoryCOP {
	return &memoryCOP{byKey: make(map[string]contracts.ProjectionResult)}
}

func (m *memoryCOP) LoadCOPReadModel(ctx context.Context) (contracts.ProjectionResult, bool, error) {
	return m.LoadCOPReadModelKey(ctx, contracts.DefaultCOPReadModelKey)
}

func (m *memoryCOP) SaveCOPReadModel(ctx context.Context, result contracts.ProjectionResult) error {
	return m.SaveCOPReadModelKey(ctx, contracts.DefaultCOPReadModelKey, result)
}

func (m *memoryCOP) LoadCOPReadModelKey(_ context.Context, key string) (contracts.ProjectionResult, bool, error) {
	got, ok := m.byKey[key]
	return got, ok, nil
}

func (m *memoryCOP) SaveCOPReadModelKey(_ context.Context, key string, result contracts.ProjectionResult) error {
	m.byKey[key] = result
	return nil
}

func TestSessionScopedCOPMemoryTwoSessionsDoNotClobber(t *testing.T) {
	ctx := context.Background()
	inner := newMemoryCOP()
	active := &stubActive{id: "sim-1", ok: true}
	scoped := NewSessionScopedCOP(inner, active)

	if err := scoped.SaveCOPReadModel(ctx, contracts.ProjectionResult{
		StateRevision: 1,
		COP:           map[string]any{"session": "one"},
	}); err != nil {
		t.Fatalf("save sim-1: %v", err)
	}

	active.id = "sim-2"
	if err := scoped.SaveCOPReadModel(ctx, contracts.ProjectionResult{
		StateRevision: 5,
		COP:           map[string]any{"session": "two"},
	}); err != nil {
		t.Fatalf("save sim-2: %v", err)
	}

	// sim-1 row intact under its key.
	got1, found, err := inner.LoadCOPReadModelKey(ctx, "sim-1")
	if err != nil || !found || got1.StateRevision != 1 || got1.COP["session"] != "one" {
		t.Fatalf("sim-1 = %#v found=%v err=%v", got1, found, err)
	}
	got2, found, err := inner.LoadCOPReadModelKey(ctx, "sim-2")
	if err != nil || !found || got2.StateRevision != 5 || got2.COP["session"] != "two" {
		t.Fatalf("sim-2 = %#v found=%v err=%v", got2, found, err)
	}
	if _, found, _ := inner.LoadCOPReadModel(ctx); found {
		t.Fatal("default key must remain empty when using session keys")
	}

	// Switch active back to sim-1 for load path.
	active.id = "sim-1"
	loaded, found, err := scoped.LoadCOPReadModel(ctx)
	if err != nil || !found || loaded.StateRevision != 1 {
		t.Fatalf("scoped load sim-1: %#v found=%v err=%v", loaded, found, err)
	}
}

func TestSessionScopedCOPInactiveIsNoop(t *testing.T) {
	ctx := context.Background()
	inner := newMemoryCOP()
	scoped := NewSessionScopedCOP(inner, &stubActive{ok: false})
	if err := scoped.SaveCOPReadModel(ctx, contracts.ProjectionResult{StateRevision: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(inner.byKey) != 0 {
		t.Fatalf("expected no keys, got %#v", inner.byKey)
	}
	_, found, err := scoped.LoadCOPReadModel(ctx)
	if err != nil || found {
		t.Fatalf("inactive load found=%v err=%v", found, err)
	}
}
