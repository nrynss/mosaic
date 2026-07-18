// Package e2e proves the local, synthetic Mosaic demo boundary without a
// network model or an operational-action client.
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/simulator"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
	"mosaic.local/mosaic/internal/terra"
)

const (
	viewerDemo     = "viewer-demo"
	supervisorDemo = "supervisor-demo"
)

// TestDomesticDisturbanceHTTPAuditAndAdvisoryBoundaries starts the real P07
// fixture spine and P08 HTTP handlers in-process. The only advisory responses
// are checked-in P04 fixture artifacts returned by tiny test doubles: no model
// transport, network call, or operational action is present in this test.
func TestDomesticDisturbanceHTTPAuditAndAdvisoryBoundaries(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	scenario, err := simulator.New(simulator.Config{
		Store:      database,
		SchemaDir:  filepath.Join(root, "ontology"),
		FixtureDir: filepath.Join(root, "datasets", simulator.DomesticDisturbance),
	})
	if err != nil {
		t.Fatalf("compose fixture simulator: %v", err)
	}
	run, err := scenario.Run(ctx)
	if err != nil {
		t.Fatalf("run domestic-disturbance fixture: %v", err)
	}
	if run.ScenarioID != "scenario-domestic-disturbance-v1" || run.StateRevision != 9 {
		t.Fatalf("scenario result = %#v, want fixture revision 9", run)
	}
	if !run.Verification.RawEventsRetained || !run.Verification.LunaLifecyclesMatch ||
		!run.Verification.ModelRunProvenanceValid || !run.Verification.CanonicalTimelineMatch ||
		!run.Verification.RoadCorrectionApplied || !run.Verification.CheckpointRecoveryMatches {
		t.Fatalf("fixture verification is incomplete: %#v", run.Verification)
	}

	resolver, err := api.NewSQLiteEvidenceResolver(database)
	if err != nil {
		t.Fatalf("compose persisted evidence resolver: %v", err)
	}
	activeInsight, recommendation := appendFixtureAdvisories(t, ctx, root, database, resolver, run)

	broker := stream.NewBroker()
	server, err := api.New(api.Config{
		Recovery: scenario,
		Records:  database,
		Evidence: resolver,
		Stream:   broker,
		Version:  "e2e",
	})
	if err != nil {
		t.Fatalf("compose API server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	assertAuthenticatedRead(t, httpServer.URL, "/api/v1/cop")
	assertResolvableEvidence(t, httpServer.URL, "canonical_event", "canonical-domestic-009-road-open")
	assertResolvableEvidence(t, httpServer.URL, "raw_event", "raw-domestic-001-call")
	assertBoundedSSE(t, httpServer, server)
	assertAuditBoundary(t, httpServer.URL, activeInsight, recommendation)
	assertDurableCount(t, database, "model_runs", 12)
	assertDurableCount(t, database, "insights", 1)
	assertDurableCount(t, database, "recommendations", 1)
	assertDurableCount(t, database, "audit_records", 2)
	if recovered, err := scenario.Recover(ctx); err != nil || recovered.StateRevision != 9 {
		t.Fatalf("audit records changed deterministic COP: revision=%d err=%v", recovered.StateRevision, err)
	}
}

func appendFixtureAdvisories(
	t *testing.T,
	ctx context.Context,
	root string,
	database *store.Store,
	persisted *api.SQLiteEvidenceResolver,
	run simulator.RunResult,
) (gen.Insight, gen.Recommendation) {
	t.Helper()
	var revisionSevenCOP map[string]any
	for _, entry := range run.Timeline {
		if entry.StateRevision == 7 {
			revisionSevenCOP = entry.COP
			break
		}
	}
	if revisionSevenCOP == nil {
		t.Fatal("fixture timeline did not retain revision seven COP for advisory validation")
	}

	active, recommendation := expectedAdvisories(t, root)
	terraEvidence := []gen.Evidence{
		fixtureEvidence("evidence-road-closure", "canonical_event", "canonical-domestic-007-repaired-road", "Repaired Brook Lane closure is effective at revision seven."),
		fixtureEvidence("evidence-weather", "canonical_event", "canonical-domestic-003-weather", "Heavy rain alert remains active."),
	}
	terraValidator, err := terra.LoadSchemaValidator(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatalf("load Terra validator: %v", err)
	}
	terraService, err := terra.New(terra.Config{
		Client:           terraFixtureClient{insight: active},
		EvidenceResolver: persistedEvidenceResolver{resolver: persisted},
		Records:          database,
		Validator:        terraValidator,
		PromptVersion:    "p12-e2e-fixture-v1",
		Provider:         "fixture",
		Model:            "fixture-terra",
		Clock:            fixtureClock,
		NewModelRunID:    func() string { return "modelrun-p12-terra-001" },
	})
	if err != nil {
		t.Fatalf("compose Terra fixture adapter: %v", err)
	}
	terraOutput, err := terraService.Assess(ctx, contracts.TerraInput{
		StateRevision: 7,
		COP:           revisionSevenCOP,
		Evidence:      terraEvidence,
	})
	if err != nil {
		t.Fatalf("append fixture Terra insight: %v", err)
	}
	if terraOutput.Insight.InsightID != active.InsightID || terraOutput.ModelRun.ValidationStatus != "valid" {
		t.Fatalf("Terra output = %#v", terraOutput)
	}

	solValidator, err := sol.LoadSchemaValidator(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatalf("load Sol validator: %v", err)
	}
	solService, err := sol.New(sol.Config{
		Client:        solFixtureClient{recommendation: recommendation},
		Resolver:      persistedSolResolver{resolver: persisted},
		Records:       database,
		Validator:     solValidator,
		PromptVersion: "p12-e2e-fixture-v1",
		Provider:      "fixture",
		Model:         "fixture-sol",
		Clock:         fixtureClock,
		NewModelRunID: func() string { return "modelrun-p12-sol-001" },
	})
	if err != nil {
		t.Fatalf("compose Sol fixture adapter: %v", err)
	}
	solOutput, err := solService.Brief(ctx, contracts.SolInput{
		StateRevision: 7,
		COP:           revisionSevenCOP,
		Insights:      []gen.Insight{active},
		Evidence: []gen.Evidence{
			fixtureEvidence("evidence-insight-access", "insight", active.InsightID, "The recommendation is limited to the cited derived assessment."),
		},
		RequestedBy: sol.SupervisorIdentity,
	})
	if err != nil {
		t.Fatalf("append fixture Sol recommendation: %v", err)
	}
	if solOutput.Recommendation.RecommendationID != recommendation.RecommendationID || solOutput.ModelRun.ValidationStatus != "valid" {
		t.Fatalf("Sol output = %#v", solOutput)
	}
	return active, recommendation
}

func assertAuthenticatedRead(t *testing.T, baseURL, path string) {
	t.Helper()
	response := request(t, http.DefaultClient, http.MethodGet, baseURL+path, viewerDemo, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", path, response.StatusCode)
	}
	data := responseData(t, response)
	if revision, ok := data["state_revision"].(float64); !ok || revision != 9 {
		t.Fatalf("GET %s state revision = %#v, want 9", path, data["state_revision"])
	}
}

func assertResolvableEvidence(t *testing.T, baseURL, kind, id string) {
	t.Helper()
	response := request(t, http.DefaultClient, http.MethodGet, baseURL+"/api/v1/evidence/"+kind+"/"+id, viewerDemo, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET evidence %s/%s status = %d", kind, id, response.StatusCode)
	}
	data := responseData(t, response)
	if resolved, _ := data["resolved"].(bool); !resolved {
		t.Fatalf("GET evidence %s/%s was not resolved: %#v", kind, id, data)
	}
}

// assertBoundedSSE reads the snapshot before publishing, which makes the
// notification ordering deterministic. Its context is a hard three-second
// bound so a regressing stream fails instead of hanging the quality gate.
func assertBoundedSSE(t *testing.T, testServer *httptest.Server, server *api.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testServer.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatalf("create SSE request: %v", err)
	}
	req.Header.Set(api.IdentityHeader, viewerDemo)
	response, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", response.StatusCode)
	}

	reader := bufio.NewReader(response.Body)
	snapshot := readSSEEvent(t, reader)
	if snapshot.name != "cop.snapshot" {
		t.Fatalf("first SSE event = %q, want cop.snapshot", snapshot.name)
	}
	var snapshotData map[string]any
	if err := json.Unmarshal(snapshot.data, &snapshotData); err != nil {
		t.Fatalf("decode SSE snapshot: %v", err)
	}
	if revision, ok := snapshotData["state_revision"].(float64); !ok || revision != 9 {
		t.Fatalf("SSE snapshot revision = %#v, want 9", snapshotData["state_revision"])
	}

	server.Publish("system.status", map[string]any{"state": "scenario_ready"})
	notice := readSSEEvent(t, reader)
	if notice.name != "system.status" {
		t.Fatalf("SSE notification = %q, want system.status", notice.name)
	}
}

func assertAuditBoundary(t *testing.T, baseURL string, active gen.Insight, recommendation gen.Recommendation) {
	t.Helper()
	viewer := request(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/briefings", viewerDemo, `{"briefing_id":"briefing-p12-viewer"}`)
	defer viewer.Body.Close()
	if viewer.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer briefing status = %d, want %d", viewer.StatusCode, http.StatusForbidden)
	}

	briefing := request(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/briefings", supervisorDemo, `{"briefing_id":"briefing-p12-supervisor","note":"Synthetic fixture briefing review."}`)
	defer briefing.Body.Close()
	if briefing.StatusCode != http.StatusAccepted {
		t.Fatalf("supervisor briefing status = %d", briefing.StatusCode)
	}
	if executed, _ := responseData(t, briefing)["executed"].(bool); executed {
		t.Fatal("briefing endpoint reported an executed action")
	}

	body := fmt.Sprintf(`{"action":"acknowledged","target_kind":"recommendation","target_id":%q,"note":"Synthetic fixture recommendation reviewed."}`, recommendation.RecommendationID)
	action := request(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/audit-actions", supervisorDemo, body)
	defer action.Body.Close()
	if action.StatusCode != http.StatusCreated {
		t.Fatalf("supervisor audit action status = %d", action.StatusCode)
	}
	if executed, _ := responseData(t, action)["executed"].(bool); executed {
		t.Fatal("audit action endpoint reported an executed action")
	}
	if active.InsightID != "insight-domestic-access-001" || recommendation.RecommendationID != "recommendation-domestic-001" {
		t.Fatalf("unexpected fixture advisory identifiers: %q / %q", active.InsightID, recommendation.RecommendationID)
	}
}

type sseEvent struct {
	name string
	data []byte
}

func readSSEEvent(t *testing.T, reader *bufio.Reader) sseEvent {
	t.Helper()
	event := sseEvent{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		if line == "\n" || line == "\r\n" {
			if event.name == "" || len(event.data) == 0 {
				t.Fatal("SSE event was missing a name or data")
			}
			return event
		}
		if len(line) > len("event: ") && line[:len("event: ")] == "event: " {
			event.name = strings.TrimSpace(line[len("event: "):])
			continue
		}
		if len(line) > len("data: ") && line[:len("data: ")] == "data: " {
			event.data = append(event.data, []byte(line[len("data: "):])...)
		}
	}
}

func request(t *testing.T, client *http.Client, method, url, identity, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	if body != "" {
		req.Body = io.NopCloser(strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(api.IdentityHeader, identity)
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return response
}

func responseData(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode HTTP response: %v", err)
	}
	return envelope.Data
}

func assertDurableCount(t *testing.T, database *store.Store, table string, want int) {
	t.Helper()
	var got int
	if err := database.SQLDB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

type persistedEvidenceResolver struct {
	resolver *api.SQLiteEvidenceResolver
}

func (r persistedEvidenceResolver) ResolveEvidence(ctx context.Context, _ int64, evidence []gen.Evidence) error {
	for _, item := range evidence {
		resolution, err := r.resolver.Resolve(ctx, item.TargetKind, item.TargetID, nil)
		if err != nil {
			return err
		}
		if !resolution.Resolved {
			return fmt.Errorf("evidence %s/%s is not durable: %s", item.TargetKind, item.TargetID, resolution.Reason)
		}
	}
	return nil
}

type persistedSolResolver struct {
	resolver *api.SQLiteEvidenceResolver
}

func (r persistedSolResolver) ResolveEvidence(ctx context.Context, revision int64, evidence []gen.Evidence) error {
	return persistedEvidenceResolver{resolver: r.resolver}.ResolveEvidence(ctx, revision, evidence)
}

func (r persistedSolResolver) ResolveInsights(ctx context.Context, _ int64, insights []gen.Insight) error {
	for _, insight := range insights {
		resolution, err := r.resolver.Resolve(ctx, "insight", insight.InsightID, nil)
		if err != nil {
			return err
		}
		if !resolution.Resolved {
			return fmt.Errorf("Insight %s is not durable: %s", insight.InsightID, resolution.Reason)
		}
	}
	return nil
}

type terraFixtureClient struct {
	insight gen.Insight
}

func (c terraFixtureClient) Assess(_ context.Context, _ terra.Request) (terra.Response, error) {
	encoded, err := json.Marshal(c.insight)
	return terra.Response{InsightJSON: encoded, ResponseID: "p12-fixture-terra"}, err
}

type solFixtureClient struct {
	recommendation gen.Recommendation
}

func (c solFixtureClient) Brief(_ context.Context, _ sol.Request) (sol.Response, error) {
	encoded, err := json.Marshal(c.recommendation)
	return sol.Response{RecommendationJSON: encoded, ResponseID: "p12-fixture-sol"}, err
}

func expectedAdvisories(t *testing.T, root string) (gen.Insight, gen.Recommendation) {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(root, "datasets", simulator.DomesticDisturbance, "expected-outcomes.json"))
	if err != nil {
		t.Fatalf("read fixture advisories: %v", err)
	}
	var outcomes struct {
		Insights        []gen.Insight        `json:"insights"`
		Recommendations []gen.Recommendation `json:"recommendations"`
	}
	if err := json.Unmarshal(encoded, &outcomes); err != nil {
		t.Fatalf("decode fixture advisories: %v", err)
	}
	var active gen.Insight
	for _, insight := range outcomes.Insights {
		if insight.InsightID == "insight-domestic-access-001" {
			active = insight
			break
		}
	}
	var recommendation gen.Recommendation
	for _, candidate := range outcomes.Recommendations {
		if candidate.RecommendationID == "recommendation-domestic-001" {
			recommendation = candidate
			break
		}
	}
	if active.InsightID == "" || active.StateRevision != 7 || recommendation.RecommendationID == "" || recommendation.StateRevision != 7 {
		t.Fatalf("P04 fixture advisory records are incomplete: %#v / %#v", active, recommendation)
	}
	return active, recommendation
}
func fixtureEvidence(id, kind, target, explanation string) gen.Evidence {
	return gen.Evidence{
		SchemaVersion: "1.0.0",
		EvidenceID:    id,
		TargetKind:    kind,
		TargetID:      target,
		Explanation:   explanation,
		CreatedAt:     fixtureClock().Format(time.RFC3339Nano),
	}
}

func fixtureClock() time.Time {
	return time.Date(2026, time.July, 18, 10, 5, 32, 0, time.UTC)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := simulator.RepositoryRoot(".")
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
}
