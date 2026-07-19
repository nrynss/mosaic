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
	"mosaic.local/mosaic/internal/replay"
	"mosaic.local/mosaic/internal/state"
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
		"/api/v1/evidence/state_fact/incident-1",
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
	for _, identity := range []string{"", viewerIdentity, supervisorIdentity, "unrecognised"} {
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
