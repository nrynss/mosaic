package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

type stubActiveSession struct {
	id     string
	active bool
}

func (s *stubActiveSession) ActiveSessionID() (string, bool) {
	if s == nil {
		return "", false
	}
	return s.id, s.active
}

func TestPreferMaterializedRecoveryEmptyWhenNoActiveSession(t *testing.T) {
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 99, COP: map[string]any{"from": "fallback"}},
	}
	mat := &stubMaterialization{
		result: contracts.ProjectionResult{StateRevision: 7, COP: map[string]any{"from": "mat"}},
		found:  true,
	}
	reader := PreferMaterializedRecovery{
		Materialized: mat,
		Fallback:     fallback,
		Active:       &stubActiveSession{active: false},
	}
	got, err := reader.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.StateRevision != 0 || len(got.COP) != 0 {
		t.Fatalf("want empty board, got %#v", got)
	}
	if fallback.calls.Load() != 0 || mat.loads.Load() != 0 {
		t.Fatal("inactive session must not load materialization or fallback")
	}
}

func TestPreferMaterializedRecoveryUsesSessionMaterialization(t *testing.T) {
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 99, COP: map[string]any{"from": "fallback"}},
	}
	mat := &stubMaterialization{
		result: contracts.ProjectionResult{
			StateRevision: 3,
			ProjectedAt:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			COP:           map[string]any{"from": "session"},
		},
		found: true,
	}
	reader := PreferMaterializedRecovery{
		Materialized: mat,
		Fallback:     fallback,
		Active:       &stubActiveSession{id: "sim-1", active: true},
	}
	got, err := reader.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.StateRevision != 3 || got.COP["from"] != "session" {
		t.Fatalf("got %#v", got)
	}
	if fallback.calls.Load() != 0 {
		t.Fatal("session mode must not fall back to unscoped recovery")
	}
}

func TestPreferMaterializedRecoveryEmptyWhenSessionMaterializationMissing(t *testing.T) {
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 5, COP: map[string]any{"from": "fallback"}},
	}
	reader := PreferMaterializedRecovery{
		Materialized: &stubMaterialization{found: false},
		Fallback:     fallback,
		Active:       &stubActiveSession{id: "sim-1", active: true},
	}
	got, err := reader.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.StateRevision != 0 {
		t.Fatalf("revision = %d, want 0 empty board", got.StateRevision)
	}
	if fallback.calls.Load() != 0 {
		t.Fatal("must not fall back when materialization missing in session mode")
	}
}

func TestHandleCOPEmptyWhenNoActiveSession(t *testing.T) {
	fixture := newFixture(t)
	active := &stubActiveSession{active: false}
	fallback := &countingRecovery{
		result: contracts.ProjectionResult{StateRevision: 9, COP: map[string]any{"seeded": true}},
	}
	server, err := New(Config{
		Recovery: PreferMaterializedRecovery{
			Materialized: &stubMaterialization{found: true, result: contracts.ProjectionResult{StateRevision: 4}},
			Fallback:     fallback,
			Active:       active,
		},
		Records:       fixture.store,
		Evidence:      fixture.server.evidence,
		Stream:        fixture.broker,
		ActiveSession: active,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/cop", "", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if rev, _ := data["state_revision"].(float64); rev != 0 {
		t.Fatalf("state_revision = %#v, want 0", data["state_revision"])
	}
	if fallback.calls.Load() != 0 {
		t.Fatal("fallback should not run for empty board")
	}
}

func TestHandleCOPAfterActiveSessionStart(t *testing.T) {
	fixture := newFixture(t)
	active := &stubActiveSession{id: "sim-live", active: true}
	mat := &stubMaterialization{
		result: contracts.ProjectionResult{
			StateRevision: 2,
			ProjectedAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			COP:           map[string]any{"incidents": []any{}},
		},
		found: true,
	}
	server, err := New(Config{
		Recovery: PreferMaterializedRecovery{
			Materialized: mat,
			Fallback:     &countingRecovery{},
			Active:       active,
		},
		Records:       fixture.store,
		Evidence:      fixture.server.evidence,
		Stream:        fixture.broker,
		ActiveSession: active,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/cop", "", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if rev, _ := data["state_revision"].(float64); rev != 2 {
		t.Fatalf("state_revision = %#v, want 2", data["state_revision"])
	}
	if mat.loads.Load() != 1 {
		t.Fatalf("materialization loads = %d", mat.loads.Load())
	}
}

func TestHandleAdvisoriesEmptyWhenNoActiveSession(t *testing.T) {
	fixture := newFixture(t)
	active := &stubActiveSession{active: false}
	server, err := New(Config{
		Recovery:        stubRecovery{result: contracts.ProjectionResult{StateRevision: 7, COP: map[string]any{}}},
		Records:         fixture.store,
		Evidence:        fixture.server.evidence,
		Stream:          fixture.broker,
		AdvisoryHistory: fixedAdvisoryHistory{},
		ActiveSession:   active,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if insights, _ := data["insights"].([]any); len(insights) != 0 {
		t.Fatalf("insights = %#v, want empty", insights)
	}
	if recs, _ := data["recommendations"].([]any); len(recs) != 0 {
		t.Fatalf("recommendations = %#v, want empty", recs)
	}
}

func TestSessionAdvisoryViewFiltersBySession(t *testing.T) {
	view := NewSessionAdvisoryView()
	view.Record("sim-a", "insight", "ins-a")
	view.Record("sim-b", "insight", "ins-b")
	view.Record("sim-a", "recommendation", "rec-a")

	history := contracts.AdvisoryHistory{
		Insights: []gen.Insight{
			{InsightID: "ins-a", StateRevision: 1},
			{InsightID: "ins-b", StateRevision: 1},
		},
		Recommendations: []gen.Recommendation{
			{RecommendationID: "rec-a", StateRevision: 1},
			{RecommendationID: "rec-b", StateRevision: 1},
		},
	}
	filtered := view.Filter("sim-a", history)
	if len(filtered.Insights) != 1 || filtered.Insights[0].InsightID != "ins-a" {
		t.Fatalf("insights = %#v", filtered.Insights)
	}
	if len(filtered.Recommendations) != 1 || filtered.Recommendations[0].RecommendationID != "rec-a" {
		t.Fatalf("recommendations = %#v", filtered.Recommendations)
	}
	empty := view.Filter("sim-missing", history)
	if len(empty.Insights) != 0 || len(empty.Recommendations) != 0 {
		t.Fatalf("unknown session should be empty, got %#v", empty)
	}
}

func TestHandleAdvisoriesFiltersWithSessionView(t *testing.T) {
	fixture := newFixture(t)
	active := &stubActiveSession{id: "sim-a", active: true}
	view := NewSessionAdvisoryView()
	view.Record("sim-a", "insight", "ins-keep")

	server, err := New(Config{
		Recovery: PreferMaterializedRecovery{
			Materialized: &stubMaterialization{
				found: true,
				result: contracts.ProjectionResult{
					StateRevision: 3,
					COP:           map[string]any{},
				},
			},
			Active: active,
		},
		Records:           fixture.store,
		Evidence:          fixture.server.evidence,
		Stream:            fixture.broker,
		AdvisoryHistory:   fixedAdvisoryHistory{},
		ActiveSession:     active,
		SessionAdvisories: view,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	insights, _ := data["insights"].([]any)
	if len(insights) != 1 {
		t.Fatalf("insights = %#v, want 1 (ins-keep)", insights)
	}
	ins, _ := insights[0].(map[string]any)
	if ins["insight_id"] != "ins-keep" {
		t.Fatalf("insight_id = %#v", ins["insight_id"])
	}
}

func TestHandleAdvisoriesEmptyAfterActiveSessionCleared(t *testing.T) {
	// Progressive Play indexes advisories for the session; explicit End clears
	// Active → GET /advisories returns the empty-board policy.
	fixture := newFixture(t)
	active := &stubActiveSession{id: "sim-live", active: true}
	view := NewSessionAdvisoryView()
	view.Record("sim-live", "insight", "ins-keep")

	server, err := New(Config{
		Recovery: PreferMaterializedRecovery{
			Materialized: &stubMaterialization{
				found: true,
				result: contracts.ProjectionResult{
					StateRevision: 9,
					COP:           map[string]any{"seeded": true},
				},
			},
			Active: active,
		},
		Records:           fixture.store,
		Evidence:          fixture.server.evidence,
		Stream:            fixture.broker,
		AdvisoryHistory:   fixedAdvisoryHistory{},
		ActiveSession:     active,
		SessionAdvisories: view,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Active → session-filtered advisories visible.
	whileActive := request(t, server.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if whileActive.Code != http.StatusOK {
		t.Fatalf("active status = %d", whileActive.Code)
	}
	activeData := responseData(t, whileActive)
	if insights, _ := activeData["insights"].([]any); len(insights) != 1 {
		t.Fatalf("active insights = %#v, want 1", activeData["insights"])
	}

	// End clears Active (empty board policy).
	active.active = false
	active.id = ""
	afterEnd := request(t, server.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if afterEnd.Code != http.StatusOK {
		t.Fatalf("after end status = %d", afterEnd.Code)
	}
	endData := responseData(t, afterEnd)
	if insights, _ := endData["insights"].([]any); len(insights) != 0 {
		t.Fatalf("after end insights = %#v, want empty", insights)
	}
	if recs, _ := endData["recommendations"].([]any); len(recs) != 0 {
		t.Fatalf("after end recommendations = %#v, want empty", recs)
	}
}

// fixedAdvisoryHistory returns two insights so filter tests can distinguish sessions.
type fixedAdvisoryHistory struct{}

func (fixedAdvisoryHistory) ReadAdvisoryHistory(context.Context) (contracts.AdvisoryHistory, error) {
	return contracts.AdvisoryHistory{
		Insights: []gen.Insight{
			{InsightID: "ins-keep", SchemaVersion: "1.0.0", StateRevision: 1, LifecycleStatus: "active"},
			{InsightID: "ins-other", SchemaVersion: "1.0.0", StateRevision: 1, LifecycleStatus: "active"},
		},
		Recommendations: []gen.Recommendation{},
		ModelRuns:       []gen.ModelRun{},
		AuditRecords:    []gen.AuditRecord{},
	}, nil
}
