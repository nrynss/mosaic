package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/state"
	"mosaic.local/mosaic/internal/replay"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
)

func TestPublicReadSurfaceReturnsCOPAndResolvableEvidenceWithoutWriting(t *testing.T) {
	fixture := newFixture(t)
	before := durableCounts(t, fixture.store)

	response := request(t, fixture.handler, http.MethodGet, "/api/v1/cop", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("COP status = %d, body = %s", response.Code, response.Body.String())
	}
	data := responseData(t, response)
	if revision, ok := data["state_revision"].(float64); !ok || revision != 1 {
		t.Fatalf("COP revision = %#v, want 1", data["state_revision"])
	}

	for _, path := range []string{
		"/api/v1/evidence/raw_event/raw-1",
		"/api/v1/evidence/canonical_event/canonical-1",
		"/api/v1/artifacts/recommendation/recommendation-1",
	} {
		response := request(t, fixture.handler, http.MethodGet, path, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
		if resolved, _ := responseData(t, response)["resolved"].(bool); !resolved {
			t.Fatalf("GET %s did not resolve", path)
		}
	}

	missing := request(t, fixture.handler, http.MethodGet, "/api/v1/evidence/raw_event/missing", "", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing evidence status = %d, body = %s", missing.Code, missing.Body.String())
	}
	if resolved, _ := responseData(t, missing)["resolved"].(bool); resolved {
		t.Fatal("missing evidence was presented as resolved")
	}
	if after := durableCounts(t, fixture.store); !reflect.DeepEqual(after, before) {
		t.Fatalf("GET requests mutated durable history: before=%#v after=%#v", before, after)
	}
}

// stubStateFacts is a domain-neutral StateFactResolver used to prove the core
// evidence resolver delegates state_fact interpretation without embedding any
// domain-specific collection or identifier.
type stubStateFacts struct {
	resolved map[string]any
}

func (s stubStateFacts) ResolveStateFact(_ context.Context, id string, _ map[string]any) (Resolution, error) {
	resolution := Resolution{Kind: "state_fact", ID: id}
	if artifact, ok := s.resolved[id]; ok {
		resolution.Resolved = true
		resolution.Artifact = artifact
		return resolution, nil
	}
	resolution.Reason = "not present"
	return resolution, nil
}

func TestStateFactEvidenceDelegatesToProfile(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Without an injected resolver the core reports state_fact as unavailable
	// rather than inventing a domain interpretation.
	bare, err := NewSQLiteEvidenceResolver(database)
	if err != nil {
		t.Fatalf("new bare resolver: %v", err)
	}
	unavailable, err := bare.Resolve(ctx, "state_fact", "fact-1", map[string]any{})
	if err != nil {
		t.Fatalf("resolve without profile: %v", err)
	}
	if unavailable.Resolved {
		t.Fatalf("state_fact resolved without a profile resolver: %#v", unavailable)
	}

	// With an injected resolver the core delegates and surfaces its result.
	wired, err := NewSQLiteEvidenceResolver(database, stubStateFacts{resolved: map[string]any{"fact-1": map[string]any{"ok": true}}})
	if err != nil {
		t.Fatalf("new wired resolver: %v", err)
	}
	resolved, err := wired.Resolve(ctx, "state_fact", "fact-1", map[string]any{})
	if err != nil {
		t.Fatalf("resolve with profile: %v", err)
	}
	if !resolved.Resolved {
		t.Fatalf("delegated state_fact did not resolve: %#v", resolved)
	}

	if _, err := NewSQLiteEvidenceResolver(database, stubStateFacts{}, stubStateFacts{}); err == nil {
		t.Fatal("expected error when configuring more than one state-fact resolver")
	}
}

func TestPublicAuditWritesNeverExecuteAnAction(t *testing.T) {
	fixture := newFixture(t)

	briefing := request(t, fixture.handler, http.MethodPost, "/api/v1/briefings", "", `{"briefing_id":"briefing-1","note":"review requested"}`)
	if briefing.Code != http.StatusAccepted {
		t.Fatalf("public briefing status = %d, body = %s", briefing.Code, briefing.Body.String())
	}
	briefingData := responseData(t, briefing)
	if executed, _ := briefingData["executed"].(bool); executed {
		t.Fatal("briefing endpoint claimed to execute an operational action")
	}
	assertAuditActor(t, briefingData, "public-demo", "viewer")

	acknowledgement := request(t, fixture.handler, http.MethodPost, "/api/v1/audit-actions", "", `{"action":"acknowledged","target_kind":"recommendation","target_id":"recommendation-1","note":"reviewed"}`)
	if acknowledgement.Code != http.StatusCreated {
		t.Fatalf("public acknowledgement status = %d, body = %s", acknowledgement.Code, acknowledgement.Body.String())
	}
	if executed, _ := responseData(t, acknowledgement)["executed"].(bool); executed {
		t.Fatal("acknowledgement endpoint claimed to execute an operational action")
	}
	if got := tableCount(t, fixture.store, "audit_records"); got != 2 {
		t.Fatalf("audit record count = %d, want 2", got)
	}
	if got := tableCount(t, fixture.store, "canonical_events"); got != 1 {
		t.Fatalf("audit API changed canonical history: canonical event count=%d", got)
	}
	if got := tableCount(t, fixture.store, "checkpoints"); got != 1 {
		t.Fatalf("audit API changed projection history: checkpoint count=%d", got)
	}
}

func TestIdentityHeaderIsDisplayMetadataNotAccessControl(t *testing.T) {
	fixture := newFixture(t)
	for _, identity := range []string{"", "viewer-token", "supervisor-token", "unrecognised"} {
		response := request(t, fixture.handler, http.MethodGet, "/api/v1/cop", identity, "")
		if response.Code != http.StatusOK {
			t.Fatalf("COP with identity %q status = %d, body = %s", identity, response.Code, response.Body.String())
		}
	}
}

func TestInjectedDenyPolicyProvesPublicPolicySeam(t *testing.T) {
	fixture := newFixture(t)
	server, err := New(Config{
		Recovery:   fixture.server.recovery,
		Records:    fixture.store,
		Evidence:   fixture.server.evidence,
		Operations: fixture.server.operations,
		Stream:     fixture.broker,
		Policy:     denyPolicy{deny: ActionReadOperations},
	})
	if err != nil {
		t.Fatalf("new server with deny policy: %v", err)
	}
	response := request(t, server.Handler(), http.MethodGet, "/api/v1/operations", "", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("denied operations status = %d, body = %s", response.Code, response.Body.String())
	}
	if code := responseErrorCode(t, response); code != "action_denied" {
		t.Fatalf("deny error code = %q", code)
	}
}

func TestOperationsReturnsBoundedEvidenceBackedTelemetry(t *testing.T) {
	fixture := newFixture(t)
	fixture.server.Publish("cop.updated", map[string]any{"payload_bytes_b64": "must-not-leak"})

	response := request(t, fixture.handler, http.MethodGet, "/api/v1/operations", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("operations status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "payload_bytes_b64") || strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("operations response leaked event payload: %s", response.Body.String())
	}
	data := responseData(t, response)
	if data["observed_at"] == nil || data["latest_source_received_at"] == nil {
		t.Fatalf("operations timestamps missing: %#v", data)
	}
	service, _ := data["service"].(map[string]any)
	if service["version"] != "v0.1" || service["uptime_seconds"] != float64(0) {
		t.Fatalf("service telemetry = %#v", service)
	}
	recovery, _ := data["recovery"].(map[string]any)
	if recovery["status"] != "recovered" || recovery["state_revision"] != float64(1) {
		t.Fatalf("recovery telemetry = %#v", recovery)
	}
	counts, _ := data["counts"].(map[string]any)
	for field, want := range map[string]float64{
		"raw_events": 1, "canonical_events": 1, "projected_events": 1,
		"unprojected_events": 0, "checkpoints": 1, "insights": 0,
		"recommendations": 1, "audit_records": 0,
	} {
		if got := counts[field]; got != want {
			t.Fatalf("count %s = %#v, want %v", field, got, want)
		}
	}
	lifecycle, _ := counts["luna_lifecycle"].(map[string]any)
	if lifecycle["accepted"] != float64(1) || lifecycle["quarantined"] != float64(1) {
		t.Fatalf("Luna lifecycle counts = %#v", lifecycle)
	}
	modelRuns, _ := counts["model_runs"].(map[string]any)
	if modelRuns["total"] != float64(3) {
		t.Fatalf("model run total = %#v", modelRuns)
	}
	streamData, _ := data["stream"].(map[string]any)
	last, _ := streamData["last_published"].(map[string]any)
	if streamData["local_subscriber_count"] != float64(0) || last["name"] != "cop.updated" || last["published_at"] == nil {
		t.Fatalf("stream telemetry = %#v", streamData)
	}
	capabilities, _ := data["capabilities"].([]any)
	if len(capabilities) != 9 {
		t.Fatalf("capability count = %d, want 9", len(capabilities))
	}
	assertCapability(t, capabilities, "startup_recovery", "composed", "recovered")
	assertCapability(t, capabilities, "terra_assessment", "unavailable", "unavailable")
	assertCapability(t, capabilities, "reconciliation", "unavailable", "unavailable")
	assertCapability(t, capabilities, "operational_action", "permanently_unavailable", "unavailable")
	if strings.Contains(strings.ToLower(response.Body.String()), "self-healing") {
		t.Fatal("operations response made an unsupported self-healing claim")
	}
}

func TestOperationsRequiresSameRequestRecoveryAndReader(t *testing.T) {
	fixture := newFixture(t)
	fixture.server.recovery = failingRecovery{}
	response := request(t, fixture.handler, http.MethodGet, "/api/v1/operations", "", "")
	if response.Code != http.StatusServiceUnavailable || responseErrorCode(t, response) != "cop_unavailable" {
		t.Fatalf("recovery failure = %d %s", response.Code, response.Body.String())
	}

	fixture = newFixture(t)
	fixture.server.operations = failingOperationsReader{}
	response = request(t, fixture.handler, http.MethodGet, "/api/v1/operations", "", "")
	if response.Code != http.StatusServiceUnavailable || responseErrorCode(t, response) != "operations_unavailable" {
		t.Fatalf("reader failure = %d %s", response.Code, response.Body.String())
	}
}

func TestStreamSendsSnapshotAndCleansUpOnCancellation(t *testing.T) {
	fixture := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	writer := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(writer, request)
		close(done)
	}()

	select {
	case <-writer.flushed:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not flush its initial snapshot")
	}
	if body := writer.Body.String(); !strings.Contains(body, "event: cop.snapshot") || !strings.Contains(body, "state_revision") {
		t.Fatalf("SSE snapshot body = %q", body)
	}
	if got := fixture.broker.SubscriberCount(); got != 1 {
		t.Fatalf("subscriber count before cancellation = %d, want 1", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not return after request cancellation")
	}
	if got := fixture.broker.SubscriberCount(); got != 0 {
		t.Fatalf("subscriber count after cancellation = %d, want 0", got)
	}
}

func TestHealthAndVersionRemainPublic(t *testing.T) {
	fixture := newFixture(t)
	for _, path := range []string{"/api/v1/health", "/api/v1/version"} {
		response := request(t, fixture.handler, http.MethodGet, path, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
	}
}

type apiFixture struct {
	store   *store.Store
	server  *Server
	handler http.Handler
	broker  *stream.Broker
}

func newFixture(t *testing.T) apiFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	raw := gen.RawEvent{
		SchemaVersion: "1.0.0",
		RawEventID:    "raw-1",
		Source: map[string]any{
			"source_id":        "api-test",
			"source_record_id": "source-1",
		},
		ContentType:     "application/json",
		PayloadBytesB64: "e30=",
		RawSha256:       "test",
		ReceivedAt:      "2026-07-18T12:00:00Z",
	}
	if _, err := database.AppendRawEvent(ctx, raw); err != nil {
		t.Fatalf("append raw: %v", err)
	}
	event, err := database.AppendCanonicalEvent(ctx, gen.CanonicalEvent{
		SchemaVersion:    "1.0.0",
		CanonicalEventID: "canonical-1",
		RawEventID:       raw.RawEventID,
		EventType:        "incident_reported",
		IncidentRefs:     []any{"incident-1"},
		OccurredAt:       "2026-07-18T11:59:00Z",
		ReceivedAt:       raw.ReceivedAt,
		Payload: map[string]any{
			"incident_id": "incident-1",
			"category":    "domestic_disturbance",
			"location_id": "location-1",
		},
	})
	if err != nil {
		t.Fatalf("append canonical: %v", err)
	}
	projector, err := state.NewProjector(database, database, database)
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	if _, err := projector.ApplyCanonicalEvent(ctx, event); err != nil {
		t.Fatalf("project canonical: %v", err)
	}
	if err := database.AppendRecommendation(ctx, gen.Recommendation{
		SchemaVersion:    "1.0.0",
		RecommendationID: "recommendation-1",
		StateRevision:    1,
		Text:             "Review available support options.",
		CreatedAt:        "2026-07-18T12:01:00Z",
	}); err != nil {
		t.Fatalf("append recommendation: %v", err)
	}
	for _, result := range []gen.LunaResult{
		{SchemaVersion: "1.0.0", LunaResultID: "luna-accepted", RawEventID: raw.RawEventID, CanonicalEventID: event.CanonicalEventID, Status: "accepted", CreatedAt: raw.ReceivedAt},
		{SchemaVersion: "1.0.0", LunaResultID: "luna-quarantined", RawEventID: raw.RawEventID, Status: "quarantined", Reason: "fixture", CreatedAt: raw.ReceivedAt},
	} {
		if err := database.AppendLunaResult(ctx, result); err != nil {
			t.Fatalf("append Luna result: %v", err)
		}
	}
	for _, run := range []gen.ModelRun{
		{SchemaVersion: "1.0.0", ModelRunID: "run-luna", Agent: "luna", Provider: "fixture", Model: "fixture", PromptVersion: "v1", OutputSchemaVersion: "1.0.0", ValidationStatus: "valid", StartedAt: raw.ReceivedAt, CompletedAt: raw.ReceivedAt},
		{SchemaVersion: "1.0.0", ModelRunID: "run-terra", Agent: "terra", Provider: "fixture", Model: "fixture", PromptVersion: "v1", OutputSchemaVersion: "1.0.0", ValidationStatus: "failed", StartedAt: raw.ReceivedAt, CompletedAt: raw.ReceivedAt},
		{SchemaVersion: "1.0.0", ModelRunID: "run-sol", Agent: "sol", Provider: "fixture", Model: "fixture", PromptVersion: "v1", OutputSchemaVersion: "1.0.0", ValidationStatus: "refused", StartedAt: raw.ReceivedAt, CompletedAt: raw.ReceivedAt},
	} {
		if err := database.AppendModelRun(ctx, run); err != nil {
			t.Fatalf("append model run: %v", err)
		}
	}

	resolver, err := NewSQLiteEvidenceResolver(database)
	if err != nil {
		t.Fatalf("new evidence resolver: %v", err)
	}
	operations, err := NewSQLiteOperationsReader(database)
	if err != nil {
		t.Fatalf("new operations reader: %v", err)
	}
	broker := stream.NewBrokerWithClock(func() time.Time {
		return time.Date(2026, 7, 18, 12, 2, 0, 0, time.UTC)
	})
	server, err := New(Config{
		Recovery:   replay.Runner{Canonical: database, Checkpoints: database, Projector: projector},
		Records:    database,
		Evidence:   resolver,
		Operations: operations,
		Stream:     broker,
		Clock: func() time.Time {
			return time.Date(2026, 7, 18, 12, 2, 0, 0, time.UTC)
		},
		NewID: sequentialIDs(),
	})
	if err != nil {
		t.Fatalf("new API server: %v", err)
	}
	return apiFixture{store: database, server: server, handler: server.Handler(), broker: broker}
}

func request(t *testing.T, handler http.Handler, method, path, identity, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if identity != "" {
		request.Header.Set(IdentityHeader, identity)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func responseData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	return envelope.Data
}

func responseErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response %q: %v", response.Body.String(), err)
	}
	return envelope.Error.Code
}

func assertAuditActor(t *testing.T, data map[string]any, id, role string) {
	t.Helper()
	audit, _ := data["audit_record"].(map[string]any)
	if audit["actor_id"] != id || audit["actor_role"] != role {
		t.Fatalf("audit actor = %#v, want %s/%s", audit, id, role)
	}
}

func assertCapability(t *testing.T, capabilities []any, want, mode, status string) {
	t.Helper()
	for _, value := range capabilities {
		capability, _ := value.(map[string]any)
		if capability["capability"] == want {
			if capability["mode"] != mode || capability["status"] != status {
				t.Fatalf("capability %s = %#v", want, capability)
			}
			return
		}
	}
	t.Fatalf("capability %q was not returned", want)
}

func tableCount(t *testing.T, database *store.Store, table string) int {
	t.Helper()
	allowed := map[string]bool{
		"audit_records":                 true,
		"canonical_events":              true,
		"checkpoints":                   true,
		"canonical_projection_receipts": true,
		"recommendations":               true,
		"raw_events":                    true,
		"luna_results":                  true,
		"insights":                      true,
		"model_runs":                    true,
	}
	if !allowed[table] {
		t.Fatalf("unsupported test table %q", table)
	}
	var count int
	if err := database.SQLDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func durableCounts(t *testing.T, database *store.Store) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, table := range []string{
		"raw_events", "canonical_events", "luna_results", "insights", "recommendations",
		"model_runs", "audit_records", "checkpoints", "canonical_projection_receipts",
	} {
		counts[table] = tableCount(t, database, table)
	}
	return counts
}

func sequentialIDs() func() string {
	var mu sync.Mutex
	next := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		next++
		return "test-" + string(rune('0'+next))
	}
}

type denyPolicy struct {
	deny Action
}

func (p denyPolicy) Authorize(_ context.Context, _ Actor, action Action) (PolicyDecision, error) {
	if action == p.deny {
		return PolicyDecision{Reason: "test denial"}, nil
	}
	return PolicyDecision{Allowed: true}, nil
}

type failingRecovery struct{}

func (failingRecovery) Recover(context.Context) (contracts.ProjectionResult, error) {
	return contracts.ProjectionResult{}, errors.New("fixture recovery failed")
}

type failingOperationsReader struct{}

func (failingOperationsReader) ReadOperations(context.Context) (OperationsSnapshot, error) {
	return OperationsSnapshot{}, errors.New("fixture operations reader failed")
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
}

func (r *flushRecorder) Flush() {
	r.once.Do(func() { close(r.flushed) })
}

type stubAdvisoryHistoryReader struct {
	history contracts.AdvisoryHistory
	err     error
}

func (s stubAdvisoryHistoryReader) ReadAdvisoryHistory(context.Context) (contracts.AdvisoryHistory, error) {
	return s.history, s.err
}

type stubRecovery struct {
	result contracts.ProjectionResult
	err    error
}

func (s stubRecovery) Recover(context.Context) (contracts.ProjectionResult, error) {
	return s.result, s.err
}

func TestAdvisoriesEndpoint(t *testing.T) {
	// 1. Default setup has no AdvisoryHistory reader -> should return 503
	fixture := newFixture(t)
	resp := request(t, fixture.handler, http.MethodGet, "/api/v1/advisories", "", "")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("default advisories status = %d, want 503", resp.Code)
	}
	if got := responseErrorCode(t, resp); got != "advisory_history_unavailable" {
		t.Fatalf("default advisories error code = %q, want advisory_history_unavailable", got)
	}

	// 2. Deny policy should return 403
	serverDeny, err := New(Config{
		Recovery: fixture.server.recovery,
		Records:  fixture.store,
		Evidence: fixture.server.evidence,
		Policy:   denyPolicy{deny: ActionReadAdvisories},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	respDeny := request(t, serverDeny.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if respDeny.Code != http.StatusForbidden {
		t.Fatalf("deny advisories status = %d, want 403", respDeny.Code)
	}
	if got := responseErrorCode(t, respDeny); got != "action_denied" {
		t.Fatalf("deny advisories error code = %q, want action_denied", got)
	}

	// 3. Test correct derived statuses at different revisions
	history := contracts.AdvisoryHistory{
		Insights: []gen.Insight{
			{
				SchemaVersion:   "1.0.0",
				InsightID:       "insight-1",
				StateRevision:   7,
				LifecycleStatus: "active",
			},
			{
				SchemaVersion:       "1.0.0",
				InsightID:           "insight-2",
				StateRevision:       9,
				LifecycleStatus:     "obsolete",
				SupersedesInsightID: "insight-1",
			},
		},
		Recommendations: []gen.Recommendation{
			{
				SchemaVersion:    "1.0.0",
				RecommendationID: "rec-1",
				StateRevision:    7,
				Evidence: []any{
					map[string]any{"target_kind": "insight", "target_id": "insight-1", "explanation": "test"},
				},
				Text: "consider something",
			},
			{
				SchemaVersion:    "1.0.0",
				RecommendationID: "rec-2",
				StateRevision:    9,
				Evidence: []any{
					map[string]any{"target_kind": "insight", "target_id": "insight-2", "explanation": "test"},
				},
				Text: "consider other",
			},
		},
	}

	// Test at Recovery revision 7
	recoverySeven := stubRecovery{
		result: contracts.ProjectionResult{StateRevision: 7},
	}
	serverSeven, err := New(Config{
		Recovery:        recoverySeven,
		Records:         fixture.store,
		Evidence:        fixture.server.evidence,
		AdvisoryHistory: stubAdvisoryHistoryReader{history: history},
		AdvisoryMode:    "fixture-composed",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	respSeven := request(t, serverSeven.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if respSeven.Code != http.StatusOK {
		t.Fatalf("advisories status = %d, want 200, body = %s", respSeven.Code, respSeven.Body.String())
	}

	var envelopeSeven struct {
		Data struct {
			Insights        []advisoryInsight        `json:"insights"`
			Recommendations []advisoryRecommendation `json:"recommendations"`
			Status          string                   `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respSeven.Body.Bytes(), &envelopeSeven); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if envelopeSeven.Data.Status != "fixture-composed" {
		t.Fatalf("expected status fixture-composed, got %q", envelopeSeven.Data.Status)
	}

	// At revision 7, only insight-1 and rec-1 should be returned (revision 9 is in the future).
	if len(envelopeSeven.Data.Insights) != 1 || envelopeSeven.Data.Insights[0].InsightID != "insight-1" {
		t.Fatalf("expected only insight-1 at revision 7, got: %#v", envelopeSeven.Data.Insights)
	}
	if got := envelopeSeven.Data.Insights[0].Status; got != "current" {
		t.Fatalf("expected insight-1 status to be current, got %q", got)
	}
	if len(envelopeSeven.Data.Recommendations) != 1 || envelopeSeven.Data.Recommendations[0].RecommendationID != "rec-1" {
		t.Fatalf("expected only rec-1 at revision 7, got: %#v", envelopeSeven.Data.Recommendations)
	}
	if got := envelopeSeven.Data.Recommendations[0].Status; got != "current" {
		t.Fatalf("expected rec-1 status to be current, got %q", got)
	}

	// Test at Recovery revision 9
	recoveryNine := stubRecovery{
		result: contracts.ProjectionResult{StateRevision: 9},
	}
	serverNine, err := New(Config{
		Recovery:        recoveryNine,
		Records:         fixture.store,
		Evidence:        fixture.server.evidence,
		AdvisoryHistory: stubAdvisoryHistoryReader{history: history},
		AdvisoryMode:    "fixture-composed",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	respNine := request(t, serverNine.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if respNine.Code != http.StatusOK {
		t.Fatalf("advisories status = %d, want 200, body = %s", respNine.Code, respNine.Body.String())
	}

	var envelopeNine struct {
		Data struct {
			Insights        []advisoryInsight        `json:"insights"`
			Recommendations []advisoryRecommendation `json:"recommendations"`
			Status          string                   `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respNine.Body.Bytes(), &envelopeNine); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// At revision 9, both insights and recommendations are returned.
	// - insight-1 (rev 7) should be superseded because insight-2 (rev 9) supersedes it.
	// - insight-2 (rev 9) lifecycle is obsolete, so it is superseded.
	// - rec-1 (rev 7) is not_current because its revision < 9.
	// - rec-2 (rev 9) is not_current because its cited insight (insight-2) is superseded.
	if len(envelopeNine.Data.Insights) != 2 {
		t.Fatalf("expected 2 insights at revision 9, got %d", len(envelopeNine.Data.Insights))
	}
	for _, ins := range envelopeNine.Data.Insights {
		if ins.Status != "superseded" {
			t.Fatalf("expected insight %q status to be superseded, got %q", ins.InsightID, ins.Status)
		}
	}

	if len(envelopeNine.Data.Recommendations) != 2 {
		t.Fatalf("expected 2 recommendations at revision 9, got %d", len(envelopeNine.Data.Recommendations))
	}
	for _, rec := range envelopeNine.Data.Recommendations {
		if rec.Status != "not_current" {
			t.Fatalf("expected rec %q status to be not_current, got %q", rec.RecommendationID, rec.Status)
		}
	}
}

func TestOperationsCapabilitiesWithAdvisoryMode(t *testing.T) {
	fixture := newFixture(t)
	server, err := New(Config{
		Recovery:     fixture.server.recovery,
		Records:      fixture.store,
		Evidence:     fixture.server.evidence,
		Operations:   fixture.server.operations,
		AdvisoryMode: "fixture_composed",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	response := request(t, server.Handler(), http.MethodGet, "/api/v1/operations", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("operations status = %d, body = %s", response.Code, response.Body.String())
	}
	data := responseData(t, response)
	capabilities, _ := data["capabilities"].([]any)
	assertCapability(t, capabilities, "terra_assessment", "fixture_composed", "available")
	assertCapability(t, capabilities, "sol_advisory", "fixture_composed", "available")
}

// TestCassetteModeSurfacing proves process-level cassette mode is additive on
// public version + advisories payloads (D2 mode/status surface). Mode is set
// only at composition; empty defaults to passthrough.
func TestCassetteModeSurfacing(t *testing.T) {
	fixture := newFixture(t)

	// Default composition → passthrough on /version
	defaultVersion := request(t, fixture.handler, http.MethodGet, "/api/v1/version", "", "")
	if defaultVersion.Code != http.StatusOK {
		t.Fatalf("default version status = %d", defaultVersion.Code)
	}
	defaultData := responseData(t, defaultVersion)
	if got, _ := defaultData["cassette_mode"].(string); got != "passthrough" {
		t.Fatalf("default cassette_mode = %q, want passthrough", got)
	}
	if _, hasDir := defaultData["cassette_dir"]; hasDir {
		t.Fatalf("default version must omit cassette_dir when unset, got %#v", defaultData["cassette_dir"])
	}

	// Explicit replay + dir on version and advisories
	server, err := New(Config{
		Recovery: stubRecovery{
			result: contracts.ProjectionResult{StateRevision: 7, COP: map[string]any{}},
		},
		Records:         fixture.store,
		Evidence:        fixture.server.evidence,
		AdvisoryHistory: stubAdvisoryHistoryReader{history: contracts.AdvisoryHistory{}},
		AdvisoryMode:    "fixture-composed",
		CassetteMode:    "replay",
		CassetteDir:     "/tmp/mosaic-recordings",
		ProviderSelection: contracts.AgentProviderSelection{
			"terra": contracts.ProviderFixture,
			"sol":   contracts.ProviderFixture,
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	handler := server.Handler()

	versionResp := request(t, handler, http.MethodGet, "/api/v1/version", "", "")
	if versionResp.Code != http.StatusOK {
		t.Fatalf("version status = %d body=%s", versionResp.Code, versionResp.Body.String())
	}
	versionData := responseData(t, versionResp)
	if got, _ := versionData["cassette_mode"].(string); got != "replay" {
		t.Fatalf("version cassette_mode = %q, want replay", got)
	}
	if got, _ := versionData["cassette_dir"].(string); got != "/tmp/mosaic-recordings" {
		t.Fatalf("version cassette_dir = %q, want /tmp/mosaic-recordings", got)
	}

	advResp := request(t, handler, http.MethodGet, "/api/v1/advisories", "", "")
	if advResp.Code != http.StatusOK {
		t.Fatalf("advisories status = %d body=%s", advResp.Code, advResp.Body.String())
	}
	advData := responseData(t, advResp)
	if got, _ := advData["cassette_mode"].(string); got != "replay" {
		t.Fatalf("advisories cassette_mode = %q, want replay", got)
	}
	providers, _ := advData["providers"].(map[string]any)
	if providers["terra"] != "fixture" {
		t.Fatalf("advisories providers = %#v", providers)
	}

	// Empty-session advisories also carry cassette_mode
	active := &stubActiveSession{active: false}
	emptyServer, err := New(Config{
		Recovery:        stubRecovery{result: contracts.ProjectionResult{StateRevision: 0}},
		Records:         fixture.store,
		Evidence:        fixture.server.evidence,
		AdvisoryHistory: fixedAdvisoryHistory{},
		ActiveSession:   active,
		CassetteMode:    "record",
	})
	if err != nil {
		t.Fatalf("new empty-session server: %v", err)
	}
	emptyResp := request(t, emptyServer.Handler(), http.MethodGet, "/api/v1/advisories", "", "")
	if emptyResp.Code != http.StatusOK {
		t.Fatalf("empty advisories status = %d", emptyResp.Code)
	}
	emptyData := responseData(t, emptyResp)
	if got, _ := emptyData["cassette_mode"].(string); got != "record" {
		t.Fatalf("empty advisories cassette_mode = %q, want record", got)
	}
}
