package sol_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
)

var solTime = time.Date(2026, time.July, 18, 10, 5, 34, 0, time.UTC)

type fixtureClient struct {
	response sol.Response
	err      error
	calls    int
	request  sol.Request
	mutate   func(*sol.Request)
}

func (c *fixtureClient) Brief(_ context.Context, request sol.Request) (sol.Response, error) {
	c.calls++
	c.request = cloneRequest(request)
	if c.mutate != nil {
		c.mutate(&request)
	}
	return c.response, c.err
}

type fixtureResolver struct {
	evidenceErr error
	insightsErr error
	evidence    []gen.Evidence
	insights    []gen.Insight
	revision    int64
	evidenceN   int
	insightsN   int
}

func (r *fixtureResolver) ResolveEvidence(_ context.Context, revision int64, evidence []gen.Evidence) error {
	r.evidenceN++
	r.revision = revision
	r.evidence = append([]gen.Evidence(nil), evidence...)
	return r.evidenceErr
}

func (r *fixtureResolver) ResolveInsights(_ context.Context, revision int64, insights []gen.Insight) error {
	r.insightsN++
	r.revision = revision
	r.insights = cloneInsights(insights)
	return r.insightsErr
}

type harness struct {
	store    *store.Store
	service  *sol.Service
	client   *fixtureClient
	resolver *fixtureResolver
}

func newHarness(t *testing.T, client *fixtureClient, resolver *fixtureResolver) harness {
	t.Helper()
	ctx := context.Background()
	database, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	validator, err := sol.LoadSchemaValidator(filepath.Join("..", "..", "ontology"))
	if err != nil {
		t.Fatalf("load Sol schema validator: %v", err)
	}
	sequence := 0
	service, err := sol.New(sol.Config{
		Client:            client,
		RequiredRequester: authorizedRequester,
		Resolver:          resolver,
		Records:           database,
		Validator:         validator,
		PromptVersion:     "1.0.0",
		Provider:          "fixture",
		Model:             "fixture-sol",
		Clock:             func() time.Time { return solTime },
		NewModelRunID: func() string {
			sequence++
			return fmt.Sprintf("sol-run-%03d", sequence)
		},
	})
	if err != nil {
		t.Fatalf("new Sol service: %v", err)
	}
	return harness{store: database, service: service, client: client, resolver: resolver}
}

func TestBriefPersistsValidRecommendationWithoutMutatingCOP(t *testing.T) {
	input := briefingInput(7)
	recommendation := validRecommendation("recommendation-access-001", input.StateRevision, input.Evidence)
	client := &fixtureClient{response: sol.Response{
		RecommendationJSON: recommendationJSON(t, recommendation),
		ResponseID:         "response-valid-001",
	}}
	client.mutate = func(request *sol.Request) {
		request.SerializedCOP[0] = '['
		request.Insights[0].InsightID = "client-mutation"
		request.Evidence[0].TargetID = "client-mutation"
	}
	h := newHarness(t, client, &fixtureResolver{})
	beforeCOP := canonicalJSON(t, input.COP)
	beforeInsight := cloneInsights(input.Insights)[0]
	beforeEvidence := input.Evidence[0]

	output, err := h.service.Brief(context.Background(), input)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if output.Recommendation.RecommendationID != recommendation.RecommendationID || output.ModelRun.ValidationStatus != "valid" {
		t.Fatalf("output = %#v", output)
	}
	if output.ModelRun.Agent != "sol" || output.ModelRun.StateRevision != 7 || output.ModelRun.ResponseID != "response-valid-001" {
		t.Fatalf("model run provenance = %#v", output.ModelRun)
	}
	if got := canonicalJSON(t, input.COP); string(got) != string(beforeCOP) {
		t.Fatalf("COP mutated by Sol: %s != %s", got, beforeCOP)
	}
	if got := canonicalJSON(t, input.Insights[0]); string(got) != string(canonicalJSON(t, beforeInsight)) {
		t.Fatalf("active Insight mutated by Sol: %s", got)
	}
	if input.Evidence[0] != beforeEvidence {
		t.Fatalf("permitted evidence mutated: %#v != %#v", input.Evidence[0], beforeEvidence)
	}
	if client.calls != 1 || h.resolver.evidenceN != 1 || h.resolver.insightsN != 1 || h.resolver.revision != 7 {
		t.Fatalf("client/resolver calls = %d/%d/%d at revision %d", client.calls, h.resolver.evidenceN, h.resolver.insightsN, h.resolver.revision)
	}
	if !json.Valid(client.request.SerializedCOP) || client.request.RequestedBy != authorizedRequester {
		t.Fatalf("fixture client received invalid least-privilege request: %#v", client.request)
	}
	assertCount(t, h.store, "model_runs", 1)
	assertCount(t, h.store, "recommendations", 1)
	assertCount(t, h.store, "canonical_events", 0)
	assertCount(t, h.store, "checkpoints", 0)
	if status := storedRunStatus(t, h.store, output.ModelRun.ModelRunID); status != "valid" {
		t.Fatalf("stored model run status = %q, want valid", status)
	}
}

func TestBriefRejectsNonSupervisorAndInvalidInputWithModelRun(t *testing.T) {
	tests := []struct {
		name  string
		input contracts.SolInput
		want  error
	}{
		{name: "unauthorized requester", input: withRequester(briefingInput(7), "unauthorized-requester"), want: sol.ErrSupervisorRequired},
		{name: "missing revision", input: withRevision(briefingInput(7), 0), want: sol.ErrInvalidBriefing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, &fixtureClient{}, &fixtureResolver{})
			output, err := h.service.Brief(context.Background(), test.input)
			if !errors.Is(err, test.want) {
				t.Fatalf("Brief error = %v, want %v", err, test.want)
			}
			if output.Recommendation.RecommendationID != "" || output.ModelRun.ValidationStatus != "invalid" || output.ModelRun.Agent != "sol" {
				t.Fatalf("invalid output = %#v", output)
			}
			if h.client.calls != 0 || h.resolver.evidenceN != 0 || h.resolver.insightsN != 0 {
				t.Fatalf("role/input violation called client or resolver: %d/%d/%d", h.client.calls, h.resolver.evidenceN, h.resolver.insightsN)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "recommendations", 0)
		})
	}
}

func TestBriefRefusalFailureAndTimeoutPersistNoRecommendation(t *testing.T) {
	input := briefingInput(7)
	tests := []struct {
		name       string
		client     *fixtureClient
		wantError  error
		wantStatus string
	}{
		{
			name:       "refusal",
			client:     &fixtureClient{response: sol.Response{RefusalDetail: "insufficient permitted evidence", ResponseID: "refused-001"}},
			wantError:  sol.ErrBriefingRefused,
			wantStatus: "refused",
		},
		{
			name:       "failure",
			client:     &fixtureClient{err: errors.New("fixture transport unavailable")},
			wantError:  sol.ErrBriefingFailed,
			wantStatus: "failed",
		},
		{
			name:       "timeout",
			client:     &fixtureClient{err: context.DeadlineExceeded},
			wantError:  sol.ErrBriefingFailed,
			wantStatus: "timed_out",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, test.client, &fixtureResolver{})
			output, err := h.service.Brief(context.Background(), input)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Brief error = %v, want %v", err, test.wantError)
			}
			if output.Recommendation.RecommendationID != "" || output.ModelRun.ValidationStatus != test.wantStatus {
				t.Fatalf("failure output = %#v", output)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "recommendations", 0)
		})
	}
}

func TestBriefRejectsMalformedEvidenceInsightAndRevisionMismatches(t *testing.T) {
	input := briefingInput(7)
	invalidSchema := validRecommendation("recommendation-invalid-schema", 7, input.Evidence)
	invalidSchema.Text = "Dispatch the nearest unit now."
	evidenceMismatch := validRecommendation("recommendation-evidence-mismatch", 7, []gen.Evidence{
		evidence("evidence-other", "canonical_event", "canonical-other", "Not permitted for this briefing."),
	})
	revisionMismatch := validRecommendation("recommendation-revision-mismatch", 8, input.Evidence)
	missingInsightInput := briefingInput(7)
	missingInsightInput.Evidence[0].TargetID = "insight-not-active"
	tests := []struct {
		name      string
		input     contracts.SolInput
		client    *fixtureClient
		resolver  *fixtureResolver
		wantCalls int
	}{
		{
			name:      "malformed JSON",
			input:     input,
			client:    &fixtureClient{response: sol.Response{RecommendationJSON: json.RawMessage(`{"recommendation_id":`)}},
			resolver:  &fixtureResolver{},
			wantCalls: 1,
		},
		{
			name:      "schema invalid",
			input:     input,
			client:    &fixtureClient{response: sol.Response{RecommendationJSON: recommendationJSON(t, invalidSchema)}},
			resolver:  &fixtureResolver{},
			wantCalls: 1,
		},
		{
			name:      "evidence mismatch",
			input:     input,
			client:    &fixtureClient{response: sol.Response{RecommendationJSON: recommendationJSON(t, evidenceMismatch)}},
			resolver:  &fixtureResolver{},
			wantCalls: 1,
		},
		{
			name:      "revision mismatch",
			input:     input,
			client:    &fixtureClient{response: sol.Response{RecommendationJSON: recommendationJSON(t, revisionMismatch)}},
			resolver:  &fixtureResolver{},
			wantCalls: 1,
		},
		{
			name:      "unavailable cited Insight",
			input:     missingInsightInput,
			client:    &fixtureClient{},
			resolver:  &fixtureResolver{},
			wantCalls: 0,
		},
		{
			name:      "unresolvable active Insight",
			input:     input,
			client:    &fixtureClient{},
			resolver:  &fixtureResolver{insightsErr: errors.New("Insight is not durable")},
			wantCalls: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, test.client, test.resolver)
			output, err := h.service.Brief(context.Background(), test.input)
			if !errors.Is(err, sol.ErrInvalidBriefing) {
				t.Fatalf("Brief error = %v, want invalid briefing", err)
			}
			if test.client.calls != test.wantCalls || output.ModelRun.ValidationStatus != "invalid" || output.Recommendation.RecommendationID != "" {
				t.Fatalf("client calls/status/output = %d/%q/%#v", test.client.calls, output.ModelRun.ValidationStatus, output.Recommendation)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "recommendations", 0)
		})
	}
}

func TestBriefPersistsP04ExpectedRecommendationAtRevisionSeven(t *testing.T) {
	document := loadExpectedOutcomes(t)
	var active gen.Insight
	for _, insight := range document.Insights {
		if insight.InsightID == "insight-domestic-access-001" {
			active = insight
			break
		}
	}
	if active.InsightID == "" || len(document.Recommendations) == 0 {
		t.Fatal("P04 expected outcomes are missing the Sol fixture")
	}
	recommendation := document.Recommendations[0]
	if recommendation.RecommendationID != "recommendation-domestic-001" || recommendation.StateRevision != 7 {
		t.Fatalf("unexpected P04 recommendation fixture: %#v", recommendation)
	}
	input := contracts.SolInput{
		StateRevision: 7,
		COP: map[string]any{
			"state_revision":     int64(7),
			"active_insight_ids": []any{active.InsightID},
			"roads":              []any{map[string]any{"road_id": "road-brook-lane", "status": "blocked"}},
		},
		Insights:    []gen.Insight{active},
		Evidence:    evidenceFromReferences(recommendation.Evidence),
		RequestedBy: authorizedRequester,
	}
	h := newHarness(t, &fixtureClient{response: sol.Response{RecommendationJSON: recommendationJSON(t, recommendation)}}, &fixtureResolver{})
	output, err := h.service.Brief(context.Background(), input)
	if err != nil {
		t.Fatalf("persist P04 Sol fixture: %v", err)
	}
	if output.Recommendation.RecommendationID != recommendation.RecommendationID || output.ModelRun.ValidationStatus != "valid" {
		t.Fatalf("P04 output = %#v", output)
	}
	assertCount(t, h.store, "model_runs", 1)
	assertCount(t, h.store, "recommendations", 1)
}

// authorizedRequester is a domain-neutral requester identity used to exercise
// the configured requester-role guard without embedding a demo or domain token.
const authorizedRequester = "authorized-requester"

func briefingInput(revision int64) contracts.SolInput {
	active := activeInsight("insight-access-001", revision)
	return contracts.SolInput{
		StateRevision: revision,
		COP: map[string]any{
			"state_revision":     revision,
			"active_insight_ids": []any{active.InsightID},
			"roads":              []any{map[string]any{"road_id": "road-brook-lane", "status": "blocked"}},
		},
		Insights: []gen.Insight{active},
		Evidence: []gen.Evidence{
			evidence("evidence-insight-access", "insight", active.InsightID, "The recommendation is limited to the cited derived assessment."),
		},
		RequestedBy: authorizedRequester,
	}
}

func withRequester(input contracts.SolInput, requester string) contracts.SolInput {
	input.RequestedBy = requester
	return input
}

func withRevision(input contracts.SolInput, revision int64) contracts.SolInput {
	input.StateRevision = revision
	return input
}

func activeInsight(id string, revision int64) gen.Insight {
	return gen.Insight{
		SchemaVersion:   "1.0.0",
		InsightID:       id,
		StateRevision:   revision,
		LifecycleStatus: "active",
		Assertions:      []any{"Brook Lane access may be constrained while heavy rain is active."},
		Evidence: []any{
			map[string]any{
				"target_kind": "canonical_event",
				"target_id":   "canonical-domestic-007-repaired-road",
				"explanation": "Repaired Brook Lane closure is effective.",
			},
		},
		Confidence: json.RawMessage(`{"source_quality":"medium","transformation_certainty":"medium","reasoning_support":"high","basis":"The cited synthetic closure supports a bounded access assessment."}`),
		CreatedAt:  solTime.Format(time.RFC3339Nano),
	}
}

func validRecommendation(id string, revision int64, permitted []gen.Evidence) gen.Recommendation {
	return gen.Recommendation{
		SchemaVersion:    "1.0.0",
		RecommendationID: id,
		StateRevision:    revision,
		Text:             "Consider reviewing the Brook Lane access constraint with the supervisor before deciding.",
		Evidence:         evidenceReferences(permitted),
		CreatedAt:        solTime.Format(time.RFC3339Nano),
	}
}

func evidence(id, kind, target, explanation string) gen.Evidence {
	return gen.Evidence{
		SchemaVersion: "1.0.0",
		EvidenceID:    id,
		TargetKind:    kind,
		TargetID:      target,
		Explanation:   explanation,
		CreatedAt:     solTime.Format(time.RFC3339Nano),
	}
}

func evidenceReferences(evidence []gen.Evidence) []any {
	references := make([]any, 0, len(evidence))
	for _, item := range evidence {
		reference := map[string]any{
			"target_kind": item.TargetKind,
			"target_id":   item.TargetID,
			"explanation": item.Explanation,
		}
		if item.JsonPointer != "" {
			reference["json_pointer"] = item.JsonPointer
		}
		references = append(references, reference)
	}
	return references
}

func evidenceFromReferences(references []any) []gen.Evidence {
	evidence := make([]gen.Evidence, 0, len(references))
	for index, reference := range references {
		encoded, _ := json.Marshal(reference)
		var decoded struct {
			TargetKind  string `json:"target_kind"`
			TargetID    string `json:"target_id"`
			JSONPointer string `json:"json_pointer"`
			Explanation string `json:"explanation"`
		}
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			continue
		}
		evidence = append(evidence, gen.Evidence{
			SchemaVersion: "1.0.0",
			EvidenceID:    fmt.Sprintf("p04-evidence-%d", index+1),
			TargetKind:    decoded.TargetKind,
			TargetID:      decoded.TargetID,
			JsonPointer:   decoded.JSONPointer,
			Explanation:   decoded.Explanation,
			CreatedAt:     solTime.Format(time.RFC3339Nano),
		})
	}
	return evidence
}

func recommendationJSON(t *testing.T, recommendation gen.Recommendation) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(recommendation)
	if err != nil {
		t.Fatalf("marshal fixture Recommendation: %v", err)
	}
	return encoded
}

func cloneRequest(request sol.Request) sol.Request {
	return sol.Request{
		StateRevision: request.StateRevision,
		SerializedCOP: append(json.RawMessage(nil), request.SerializedCOP...),
		Insights:      cloneInsights(request.Insights),
		Evidence:      append([]gen.Evidence(nil), request.Evidence...),
		RequestedBy:   request.RequestedBy,
	}
}

func cloneInsights(insights []gen.Insight) []gen.Insight {
	cloned := make([]gen.Insight, len(insights))
	for index, insight := range insights {
		encoded, err := json.Marshal(insight)
		if err != nil {
			cloned[index] = insight
			continue
		}
		if err := json.Unmarshal(encoded, &cloned[index]); err != nil {
			cloned[index] = insight
		}
	}
	return cloned
}

func canonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
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

type expectedOutcomes struct {
	Insights        []gen.Insight        `json:"insights"`
	Recommendations []gen.Recommendation `json:"recommendations"`
}

func loadExpectedOutcomes(t *testing.T) expectedOutcomes {
	t.Helper()
	path := filepath.Join("..", "..", "datasets", "domestic-disturbance", "expected-outcomes.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read P04 expected outcomes: %v", err)
	}
	var document expectedOutcomes
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode P04 expected outcomes: %v", err)
	}
	return document
}
