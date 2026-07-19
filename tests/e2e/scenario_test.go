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

// TestDomesticDisturbanceHTTPAuditAndAdvisoryBoundaries starts the real P07
// fixture spine and public P17 HTTP handlers in-process. The only advisory
// responses are checked-in P04 fixture artifacts returned by tiny test doubles:
// no model transport, network call, or operational action is present in this
// test.
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
	operations, err := api.NewSQLiteOperationsReader(database)
	if err != nil {
		t.Fatalf("compose bounded operations reader: %v", err)
	}
	activeInsight, recommendation := appendFixtureAdvisories(t, ctx, root, database, resolver, run)

	broker := stream.NewBroker()
	server, err := api.New(api.Config{
		Recovery:   scenario,
		Records:    database,
		Evidence:   resolver,
		Operations: operations,
		Stream:     broker,
		Version:    "e2e",
	})
	if err != nil {
		t.Fatalf("compose API server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	assertPublicCOPRead(t, httpServer.URL, "/api/v1/cop")
	assertResolvableEvidence(t, httpServer.URL, "canonical_event", "canonical-domestic-009-road-open")
	assertResolvableEvidence(t, httpServer.URL, "raw_event", "raw-domestic-001-call")
	assertBoundedSSE(t, httpServer, server)
	assertAuditBoundary(t, httpServer.URL, activeInsight, recommendation)
	assertPublicOperations(t, httpServer.URL)
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

func assertPublicCOPRead(t *testing.T, baseURL, path string) {
	t.Helper()
	response := request(t, http.DefaultClient, http.MethodGet, baseURL+path, "", "")
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
	response := request(t, http.DefaultClient, http.MethodGet, baseURL+"/api/v1/evidence/"+kind+"/"+id, "", "")
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
	briefing := request(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/briefings", "", "{\"briefing_id\":\"briefing-p20-public\",\"note\":\"Synthetic fixture briefing review.\"}")
	defer briefing.Body.Close()
	if briefing.StatusCode != http.StatusAccepted {
		t.Fatalf("public briefing status = %d", briefing.StatusCode)
	}
	briefingData := responseData(t, briefing)
	if executed, _ := briefingData["executed"].(bool); executed {
		t.Fatal("briefing endpoint reported an executed action")
	}
	assertPublicAuditActor(t, briefingData)

	body := fmt.Sprintf("{\"action\":\"acknowledged\",\"target_kind\":\"recommendation\",\"target_id\":%q,\"note\":\"Synthetic fixture recommendation reviewed.\"}", recommendation.RecommendationID)
	action := request(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/audit-actions", "", body)
	defer action.Body.Close()
	if action.StatusCode != http.StatusCreated {
		t.Fatalf("public audit action status = %d", action.StatusCode)
	}
	actionData := responseData(t, action)
	if executed, _ := actionData["executed"].(bool); executed {
		t.Fatal("audit action endpoint reported an executed action")
	}
	assertPublicAuditActor(t, actionData)
	if active.InsightID != "insight-domestic-access-001" || recommendation.RecommendationID != "recommendation-domestic-001" {
		t.Fatalf("unexpected fixture advisory identifiers: %q / %q", active.InsightID, recommendation.RecommendationID)
	}
}

func assertPublicAuditActor(t *testing.T, data map[string]any) {
	t.Helper()
	audit, ok := data["audit_record"].(map[string]any)
	if !ok {
		t.Fatalf("audit response does not include an audit record: %#v", data)
	}
	if actor, _ := audit["actor_id"].(string); actor != "public-demo" {
		t.Fatalf("audit actor = %q, want public-demo", actor)
	}
	if role, _ := audit["actor_role"].(string); role != "viewer" {
		t.Fatalf("no-header audit role = %q, want viewer display mode", role)
	}
}

func assertPublicOperations(t *testing.T, baseURL string) {
	t.Helper()
	response := request(t, http.DefaultClient, http.MethodGet, baseURL+"/api/v1/operations", "", "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("public operations status = %d", response.StatusCode)
	}

	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read operations response: %v", err)
	}
	for _, field := range []string{
		"\"payload\"",
		"\"payload_bytes_b64\"",
		"\"raw_sha256\"",
		"\"prompt\"",
		"\"prompt_version\"",
		"\"response\"",
		"\"response_id\"",
		"\"model_response\"",
	} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("operations response disclosed forbidden field %s: %s", field, encoded)
		}
	}

	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode operations response: %v", err)
	}
	data := mapValue(t, envelope, "data")
	assertTimestamp(t, data, "observed_at")
	assertTimestamp(t, data, "latest_source_received_at")

	service := mapValue(t, data, "service")
	if version, _ := service["version"].(string); version != "e2e" {
		t.Fatalf("operations service version = %q, want e2e", version)
	}
	recovery := mapValue(t, data, "recovery")
	if status, _ := recovery["status"].(string); status != "recovered" || countValue(t, recovery, "state_revision") != 9 {
		t.Fatalf("operations recovery = %#v", recovery)
	}

	counts := mapValue(t, data, "counts")
	for key, want := range map[string]int{
		"raw_events": 10, "canonical_events": 9, "projected_events": 9,
		"unprojected_events": 0, "checkpoints": 9, "insights": 1,
		"recommendations": 1, "audit_records": 2,
	} {
		if got := countValue(t, counts, key); got != want {
			t.Fatalf("operations %s = %d, want %d", key, got, want)
		}
	}
	lifecycle := mapValue(t, counts, "luna_lifecycle")
	for key, want := range map[string]int{"accepted": 8, "repaired": 1, "quarantined": 1, "rejected": 0} {
		if got := countValue(t, lifecycle, key); got != want {
			t.Fatalf("operations Luna %s = %d, want %d", key, got, want)
		}
	}
	modelRuns := mapValue(t, counts, "model_runs")
	if countValue(t, modelRuns, "total") != 12 {
		t.Fatalf("operations model-run count = %d, want 12", countValue(t, modelRuns, "total"))
	}
	assertAgentCounts(t, modelRuns["by_agent"])

	streamData := mapValue(t, data, "stream")
	published := mapValue(t, streamData, "last_published")
	if name, _ := published["name"].(string); name != "system.status" {
		t.Fatalf("operations last stream event = %q, want system.status", name)
	}
	assertTimestamp(t, published, "published_at")
	assertCapabilities(t, data["capabilities"])
}

func mapValue(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("operations %s = %#v, want object", key, parent[key])
	}
	return value
}

func countValue(t *testing.T, parent map[string]any, key string) int {
	t.Helper()
	value, ok := parent[key].(float64)
	if !ok {
		t.Fatalf("operations %s = %#v, want JSON number", key, parent[key])
	}
	return int(value)
}

func assertTimestamp(t *testing.T, parent map[string]any, key string) {
	t.Helper()
	value, ok := parent[key].(string)
	if !ok {
		t.Fatalf("operations %s = %#v, want timestamp", key, parent[key])
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err != nil || parsed.IsZero() {
		t.Fatalf("operations %s = %q, want non-zero RFC3339 timestamp: %v", key, value, err)
	}
}

func assertAgentCounts(t *testing.T, value any) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("operations model by_agent = %#v, want array", value)
	}
	want := map[string]map[string]int{
		"luna":  {"valid": 10, "invalid": 0, "refused": 0, "failed": 0, "timed_out": 0},
		"sol":   {"valid": 1, "invalid": 0, "refused": 0, "failed": 0, "timed_out": 0},
		"terra": {"valid": 1, "invalid": 0, "refused": 0, "failed": 0, "timed_out": 0},
	}
	if len(items) != len(want) {
		t.Fatalf("operations model agent count = %d, want %d", len(items), len(want))
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		agent := objectValue(t, item, "model by_agent item")
		name, _ := agent["agent"].(string)
		statuses, known := want[name]
		if !known || seen[name] {
			t.Fatalf("unexpected operations model agent %#v", agent)
		}
		seen[name] = true
		if got := countValue(t, agent, "total"); got != statuses["valid"] {
			t.Fatalf("operations model agent %q total = %d, want %d", name, got, statuses["valid"])
		}
		validation := mapValue(t, agent, "validation_statuses")
		for status, expected := range statuses {
			if got := countValue(t, validation, status); got != expected {
				t.Fatalf("operations model agent %q %s = %d, want %d", name, status, got, expected)
			}
		}
	}
}

func assertCapabilities(t *testing.T, value any) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("operations capabilities = %#v, want array", value)
	}
	want := map[string]struct{ mode, status string }{
		"source_intake":           {"fixture", "available"},
		"luna_normalization":      {"fixture", "available"},
		"deterministic_projector": {"composed", "available"},
		"startup_recovery":        {"composed", "recovered"},
		"terra_assessment":        {"unavailable", "unavailable"},
		"sol_advisory":            {"unavailable", "unavailable"},
		"human_audit":             {"composed", "available"},
		"reconciliation":          {"unavailable", "unavailable"},
		"operational_action":      {"permanently_unavailable", "unavailable"},
	}
	if len(items) != len(want) {
		t.Fatalf("operations capability count = %d, want %d", len(items), len(want))
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		capability := objectValue(t, item, "capability item")
		name, _ := capability["capability"].(string)
		expected, ok := want[name]
		if !ok || seen[name] {
			t.Fatalf("unexpected operations capability %#v", capability)
		}
		seen[name] = true
		mode, _ := capability["mode"].(string)
		status, _ := capability["status"].(string)
		if mode != expected.mode || status != expected.status {
			t.Fatalf("operations capability %q = mode %q status %q, want mode %q status %q", name, mode, status, expected.mode, expected.status)
		}
	}
}

func objectValue(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("operations %s = %#v, want object", label, value)
	}
	return object
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
	if strings.TrimSpace(identity) != "" {
		req.Header.Set(api.IdentityHeader, identity)
	}
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
