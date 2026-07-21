package api

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
)

func TestPreferMaterializedRecoveryUsesMaterializationWithoutFallback(t *testing.T) {
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 99, COP: map[string]any{"from": "fallback"}},
	}
	mat := &stubMaterialization{
		result: contracts.ProjectionResult{
			StateRevision: 7,
			ProjectedAt:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			COP:           map[string]any{"from": "materialized"},
		},
		found: true,
	}
	reader := PreferMaterializedRecovery{Materialized: mat, Fallback: fallback}

	got, err := reader.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.StateRevision != 7 || got.COP["from"] != "materialized" {
		t.Fatalf("got %#v, want materialization", got)
	}
	if fallback.calls.Load() != 0 {
		t.Fatalf("fallback Recover called %d times; materialization must short-circuit full recover", fallback.calls.Load())
	}
}

func TestPreferMaterializedRecoveryFallsBackWhenMissing(t *testing.T) {
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 1, COP: map[string]any{"from": "fallback"}},
	}
	reader := PreferMaterializedRecovery{
		Materialized: &stubMaterialization{found: false},
		Fallback:     fallback,
	}

	got, err := reader.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.COP["from"] != "fallback" {
		t.Fatalf("got %#v, want fallback", got)
	}
	if fallback.calls.Load() != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls.Load())
	}
}

func TestPreferMaterializedRecoveryNilMaterializationUsesFallback(t *testing.T) {
	// SQLite / non-Postgres compositions leave Materialized nil.
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 2, COP: map[string]any{"from": "fallback"}},
	}
	reader := PreferMaterializedRecovery{Fallback: fallback}

	got, err := reader.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.StateRevision != 2 {
		t.Fatalf("revision = %d, want 2", got.StateRevision)
	}
}

func TestPreferMaterializedRecoveryLoadErrorFailsClosed(t *testing.T) {
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 1, COP: map[string]any{}},
	}
	reader := PreferMaterializedRecovery{
		Materialized: &stubMaterialization{err: errors.New("db down")},
		Fallback:     fallback,
	}
	if _, err := reader.Recover(context.Background()); err == nil {
		t.Fatal("expected load error")
	}
	if fallback.calls.Load() != 0 {
		t.Fatal("fallback must not run when materialization load fails")
	}
}

func TestHandleCOPUsesMaterializedRecoveryWithoutFullRecover(t *testing.T) {
	fixture := newFixture(t)
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 99, COP: map[string]any{"from": "fallback"}},
	}
	mat := &stubMaterialization{
		result: contracts.ProjectionResult{
			StateRevision: 4,
			ProjectedAt:   time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
			COP:           map[string]any{"incidents": []any{}},
		},
		found: true,
	}
	server, err := New(Config{
		Recovery: PreferMaterializedRecovery{Materialized: mat, Fallback: fallback},
		Records:  fixture.store,
		Evidence: fixture.server.evidence,
		Stream:   fixture.broker,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/cop", "", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if fallback.calls.Load() != 0 {
		t.Fatalf("GET /cop invoked full recover %d times; expected materialization only", fallback.calls.Load())
	}
	if mat.loads.Load() != 1 {
		t.Fatalf("materialization loads = %d, want 1", mat.loads.Load())
	}
	data := responseData(t, resp)
	if revision, ok := data["state_revision"].(float64); !ok || revision != 4 {
		t.Fatalf("state_revision = %#v, want 4", data["state_revision"])
	}
}

type countingRecovery struct {
	result contracts.ProjectionResult
	err    error
	calls  atomic.Int64
}

func (c *countingRecovery) Recover(context.Context) (contracts.ProjectionResult, error) {
	c.calls.Add(1)
	return c.result, c.err
}

type stubMaterialization struct {
	result contracts.ProjectionResult
	found  bool
	err    error
	loads  atomic.Int64
}

func (s *stubMaterialization) LoadCOPReadModel(context.Context) (contracts.ProjectionResult, bool, error) {
	s.loads.Add(1)
	return s.result, s.found, s.err
}
