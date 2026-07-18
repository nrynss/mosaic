package terra_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/terra"
)

var terraTime = time.Date(2026, time.July, 18, 10, 5, 32, 0, time.UTC)

type fixtureClient struct {
	response terra.Response
	err      error
	calls    int
	request  terra.Request
	mutate   func(*terra.Request)
}

func (c *fixtureClient) Assess(_ context.Context, request terra.Request) (terra.Response, error) {
	c.calls++
	c.request = cloneRequest(request)
	if c.mutate != nil {
		c.mutate(&request)
	}
	return c.response, c.err
}

type fixtureResolver struct {
	err      error
	calls    int
	revision int64
	evidence []gen.Evidence
}

func (r *fixtureResolver) ResolveEvidence(_ context.Context, revision int64, evidence []gen.Evidence) error {
	r.calls++
	r.revision = revision
	r.evidence = append([]gen.Evidence(nil), evidence...)
	return r.err
}

type harness struct {
	store    *store.Store
	service  *terra.Service
	client   *fixtureClient
	resolver *fixtureResolver
}

func newHarness(t *testing.T, client *fixtureClient, resolver *fixtureResolver, existing ...gen.Insight) harness {
	t.Helper()
	ctx := context.Background()
	database, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	validator, err := terra.LoadSchemaValidator(filepath.Join("..", "..", "ontology"))
	if err != nil {
		t.Fatalf("load Terra schema validator: %v", err)
	}
	sequence := 0
	service, err := terra.New(terra.Config{
		Client:           client,
		EvidenceResolver: resolver,
		Records:          database,
		Validator:        validator,
		PromptVersion:    "1.0.0",
		Provider:         "fixture",
		Model:            "fixture-terra",
		Clock:            func() time.Time { return terraTime },
		NewModelRunID: func() string {
			sequence++
			return fmt.Sprintf("terra-run-%03d", sequence)
		},
		ExistingInsights: existing,
	})
	if err != nil {
		t.Fatalf("new Terra service: %v", err)
	}
	return harness{store: database, service: service, client: client, resolver: resolver}
}

func TestAssessPersistsValidAssessmentWithoutMutatingCOP(t *testing.T) {
	input := accessConstraintInput(7)
	client := &fixtureClient{response: terra.Response{
		InsightJSON: insightJSON(t, activeInsight("insight-access-001", input.StateRevision, input.Evidence)),
		ResponseID:  "response-valid-001",
	}}
	client.mutate = func(request *terra.Request) {
		request.Evidence[0].TargetID = "attempted-client-mutation"
		request.SerializedCOP[0] = '['
	}
	h := newHarness(t, client, &fixtureResolver{})
	beforeCOP := canonicalJSON(t, input.COP)
	beforeEvidence := input.Evidence[0]

	output, err := h.service.Assess(context.Background(), input)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if output.Insight.InsightID != "insight-access-001" || output.ModelRun.ValidationStatus != "valid" {
		t.Fatalf("output = %#v", output)
	}
	if output.ModelRun.StateRevision != 7 || output.ModelRun.Agent != "terra" || output.ModelRun.ResponseID != "response-valid-001" {
		t.Fatalf("model run provenance = %#v", output.ModelRun)
	}
	if got := canonicalJSON(t, input.COP); string(got) != string(beforeCOP) {
		t.Fatalf("COP mutated by Terra: %s != %s", got, beforeCOP)
	}
	if input.Evidence[0] != beforeEvidence {
		t.Fatalf("permitted evidence mutated: %#v != %#v", input.Evidence[0], beforeEvidence)
	}
	if client.calls != 1 || h.resolver.calls != 1 || h.resolver.revision != 7 {
		t.Fatalf("client/resolver calls = %d/%d at revision %d", client.calls, h.resolver.calls, h.resolver.revision)
	}
	if !json.Valid(client.request.SerializedCOP) {
		t.Fatal("fixture client did not receive serialized COP JSON")
	}
	assertCount(t, h.store, "model_runs", 1)
	assertCount(t, h.store, "insights", 1)
	if status := storedRunStatus(t, h.store, output.ModelRun.ModelRunID); status != "valid" {
		t.Fatalf("stored model run status = %q, want valid", status)
	}
}

func TestAssessInvalidOutputPersistsOnlyInvalidModelRun(t *testing.T) {
	input := accessConstraintInput(7)
	invalidSchema := activeInsight("insight-invalid-schema", 7, input.Evidence)
	invalidSchema.Confidence = nil
	tests := []struct {
		name     string
		response terra.Response
	}{
		{name: "malformed JSON", response: terra.Response{InsightJSON: json.RawMessage(`{"insight_id":`)}},
		{name: "schema invalid", response: terra.Response{InsightJSON: insightJSON(t, invalidSchema)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, &fixtureClient{response: test.response}, &fixtureResolver{})
			output, err := h.service.Assess(context.Background(), input)
			if !errors.Is(err, terra.ErrInvalidAssessment) {
				t.Fatalf("Assess error = %v, want invalid assessment", err)
			}
			if output.Insight.InsightID != "" || output.ModelRun.ValidationStatus != "invalid" {
				t.Fatalf("invalid output = %#v", output)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "insights", 0)
		})
	}
}

func TestAssessRefusalFailureAndTimeoutPersistNoInsight(t *testing.T) {
	input := accessConstraintInput(7)
	tests := []struct {
		name       string
		client     *fixtureClient
		wantError  error
		wantStatus string
	}{
		{
			name:       "refusal",
			client:     &fixtureClient{response: terra.Response{RefusalDetail: "insufficient evidence", ResponseID: "refused-001"}},
			wantError:  terra.ErrAssessmentRefused,
			wantStatus: "refused",
		},
		{
			name:       "failure",
			client:     &fixtureClient{err: errors.New("fixture transport unavailable")},
			wantError:  terra.ErrAssessmentFailed,
			wantStatus: "failed",
		},
		{
			name:       "timeout",
			client:     &fixtureClient{err: context.DeadlineExceeded},
			wantError:  terra.ErrAssessmentFailed,
			wantStatus: "timed_out",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, test.client, &fixtureResolver{})
			output, err := h.service.Assess(context.Background(), input)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Assess error = %v, want %v", err, test.wantError)
			}
			if output.Insight.InsightID != "" || output.ModelRun.ValidationStatus != test.wantStatus {
				t.Fatalf("failure output = %#v", output)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "insights", 0)
		})
	}
}

func TestAssessRejectsOutOfScopeUnresolvableAndRevisionMismatchedEvidence(t *testing.T) {
	input := accessConstraintInput(7)
	outOfScope := activeInsight("insight-out-of-scope", 7, input.Evidence)
	outOfScope.Evidence = evidenceRefs([]gen.Evidence{evidence("evidence-unpermitted", "canonical_event", "canonical-not-permitted", "Not in the allowed input.")})
	revisionMismatch := activeInsight("insight-revision-mismatch", 8, input.Evidence)
	tests := []struct {
		name      string
		client    *fixtureClient
		resolver  *fixtureResolver
		wantCalls int
	}{
		{
			name:      "out of scope response evidence",
			client:    &fixtureClient{response: terra.Response{InsightJSON: insightJSON(t, outOfScope)}},
			resolver:  &fixtureResolver{},
			wantCalls: 1,
		},
		{
			name:      "revision mismatch",
			client:    &fixtureClient{response: terra.Response{InsightJSON: insightJSON(t, revisionMismatch)}},
			resolver:  &fixtureResolver{},
			wantCalls: 1,
		},
		{
			name:      "unresolvable permitted evidence",
			client:    &fixtureClient{},
			resolver:  &fixtureResolver{err: errors.New("canonical event is not durable at revision")},
			wantCalls: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, test.client, test.resolver)
			output, err := h.service.Assess(context.Background(), input)
			if !errors.Is(err, terra.ErrInvalidAssessment) {
				t.Fatalf("Assess error = %v, want invalid assessment", err)
			}
			if test.client.calls != test.wantCalls || output.ModelRun.ValidationStatus != "invalid" {
				t.Fatalf("client calls/status = %d/%q", test.client.calls, output.ModelRun.ValidationStatus)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "insights", 0)
		})
	}
}

func TestAssessAppendsP04ActiveAndObsoleteInsightLifecycle(t *testing.T) {
	activeInput := accessConstraintInput(7)
	obsoleteInput := roadOpenInput(9)
	client := &fixtureClient{}
	h := newHarness(t, client, &fixtureResolver{})

	active := activeInsight("insight-domestic-access-001", 7, activeInput.Evidence)
	client.response = terra.Response{InsightJSON: insightJSON(t, active)}
	if _, err := h.service.Assess(context.Background(), activeInput); err != nil {
		t.Fatalf("persist active Insight: %v", err)
	}

	obsolete := obsoleteInsight("insight-domestic-access-001-obsolete", 9, "insight-domestic-access-001", obsoleteInput.Evidence)
	client.response = terra.Response{InsightJSON: insightJSON(t, obsolete)}
	if _, err := h.service.Assess(context.Background(), obsoleteInput); err != nil {
		t.Fatalf("persist obsolete Insight: %v", err)
	}
	assertCount(t, h.store, "model_runs", 2)
	assertCount(t, h.store, "insights", 2)

	secondNoticeInput := roadOpenInput(10)
	secondNotice := obsoleteInsight("insight-domestic-access-001-obsolete-again", 10, "insight-domestic-access-001", secondNoticeInput.Evidence)
	client.response = terra.Response{InsightJSON: insightJSON(t, secondNotice)}
	if _, err := h.service.Assess(context.Background(), secondNoticeInput); !errors.Is(err, terra.ErrInvalidAssessment) {
		t.Fatalf("second obsolete notice error = %v, want invalid assessment", err)
	}
	assertCount(t, h.store, "model_runs", 3)
	assertCount(t, h.store, "insights", 2)
}

func accessConstraintInput(revision int64) contracts.TerraInput {
	evidence := []gen.Evidence{
		evidence("evidence-road-closure", "canonical_event", "canonical-domestic-007-repaired-road", "Repaired Brook Lane closure is effective."),
		evidence("evidence-weather", "canonical_event", "canonical-domestic-003-weather", "Heavy rain alert remains active."),
	}
	return contracts.TerraInput{
		StateRevision: revision,
		COP: map[string]any{
			"state_revision":      revision,
			"effective_event_ids": []any{"canonical-domestic-003-weather", "canonical-domestic-007-repaired-road"},
			"roads":               []any{map[string]any{"road_id": "road-brook-lane", "status": "blocked"}},
			"weather_alerts":      []any{map[string]any{"weather_alert_id": "weather-heavy-rain-001", "status": "active"}},
		},
		Evidence: evidence,
	}
}

func roadOpenInput(revision int64) contracts.TerraInput {
	evidence := []gen.Evidence{
		evidence("evidence-road-open", "canonical_event", "canonical-domestic-009-road-open", "The correction opens Brook Lane."),
	}
	return contracts.TerraInput{
		StateRevision: revision,
		COP: map[string]any{
			"state_revision":      revision,
			"effective_event_ids": []any{"canonical-domestic-003-weather", "canonical-domestic-009-road-open"},
			"roads":               []any{map[string]any{"road_id": "road-brook-lane", "status": "open"}},
			"weather_alerts":      []any{map[string]any{"weather_alert_id": "weather-heavy-rain-001", "status": "active"}},
		},
		Evidence: evidence,
	}
}

func evidence(id, kind, target, explanation string) gen.Evidence {
	return gen.Evidence{
		SchemaVersion: "1.0.0",
		EvidenceID:    id,
		TargetKind:    kind,
		TargetID:      target,
		Explanation:   explanation,
		CreatedAt:     terraTime.Format(time.RFC3339Nano),
	}
}

func activeInsight(id string, revision int64, permitted []gen.Evidence) gen.Insight {
	return gen.Insight{
		SchemaVersion:   "1.0.0",
		InsightID:       id,
		StateRevision:   revision,
		LifecycleStatus: "active",
		Assertions:      []any{"Brook Lane access may be constrained while heavy rain is active."},
		Evidence:        evidenceRefs(permitted),
		Confidence:      json.RawMessage(`{"source_quality":"medium","transformation_certainty":"medium","reasoning_support":"high","basis":"The permitted road and weather evidence support a bounded access assessment."}`),
		CreatedAt:       terraTime.Format(time.RFC3339Nano),
	}
}

func obsoleteInsight(id string, revision int64, prior string, permitted []gen.Evidence) gen.Insight {
	insight := activeInsight(id, revision, permitted)
	insight.LifecycleStatus = "obsolete"
	insight.Assertions = []any{"The Brook Lane-specific access constraint is no longer current after the reopening correction."}
	insight.SupersedesInsightID = prior
	insight.ObsoleteReason = "The supporting Brook Lane closure was superseded by an opening correction."
	insight.Confidence = json.RawMessage(`{"source_quality":"high","transformation_certainty":"high","reasoning_support":"high","basis":"The permitted correction supersedes the prior closure evidence."}`)
	return insight
}

func evidenceRefs(evidence []gen.Evidence) []any {
	refs := make([]any, 0, len(evidence))
	for _, item := range evidence {
		reference := map[string]any{
			"target_kind": item.TargetKind,
			"target_id":   item.TargetID,
			"explanation": item.Explanation,
		}
		if item.JsonPointer != "" {
			reference["json_pointer"] = item.JsonPointer
		}
		refs = append(refs, reference)
	}
	return refs
}

func insightJSON(t *testing.T, insight gen.Insight) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(insight)
	if err != nil {
		t.Fatalf("marshal fixture Insight: %v", err)
	}
	return encoded
}

func cloneRequest(request terra.Request) terra.Request {
	clone := terra.Request{StateRevision: request.StateRevision}
	clone.SerializedCOP = append(json.RawMessage(nil), request.SerializedCOP...)
	clone.Evidence = append([]gen.Evidence(nil), request.Evidence...)
	return clone
}

func canonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test value: %v", err)
	}
	return encoded
}

func assertCount(t *testing.T, database *store.Store, table string, want int) {
	t.Helper()
	var count int
	if err := database.SQLDB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func storedRunStatus(t *testing.T, database *store.Store, id string) string {
	t.Helper()
	var record string
	if err := database.SQLDB().QueryRowContext(context.Background(), "SELECT record_json FROM model_runs WHERE model_run_id = ?", id).Scan(&record); err != nil {
		t.Fatalf("load model run %q: %v", id, err)
	}
	var run gen.ModelRun
	if err := json.Unmarshal([]byte(record), &run); err != nil {
		t.Fatalf("decode stored model run: %v", err)
	}
	return run.ValidationStatus
}
