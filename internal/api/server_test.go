package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/replay"
	"mosaic.local/mosaic/internal/state"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
)

func TestReadSurfaceReturnsCOPAndResolvableEvidenceWithoutWriting(t *testing.T) {
	fixture := newFixture(t)
	before := durableCounts(t, fixture.store)

	response := request(t, fixture.handler, http.MethodGet, "/api/v1/cop", viewerIdentity, "")
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
		response := request(t, fixture.handler, http.MethodGet, path, viewerIdentity, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
		if resolved, _ := responseData(t, response)["resolved"].(bool); !resolved {
			t.Fatalf("GET %s did not resolve", path)
		}
	}

	missing := request(t, fixture.handler, http.MethodGet, "/api/v1/evidence/raw_event/missing", viewerIdentity, "")
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

func TestFixedRolesGateAuditWritesAndNeverExecuteAnAction(t *testing.T) {
	fixture := newFixture(t)

	unauthenticated := request(t, fixture.handler, http.MethodGet, "/api/v1/cop", "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated COP status = %d", unauthenticated.Code)
	}
	viewerBriefing := request(t, fixture.handler, http.MethodPost, "/api/v1/briefings", viewerIdentity, `{"briefing_id":"briefing-1"}`)
	if viewerBriefing.Code != http.StatusForbidden {
		t.Fatalf("viewer briefing status = %d, body = %s", viewerBriefing.Code, viewerBriefing.Body.String())
	}
	if got := tableCount(t, fixture.store, "audit_records"); got != 0 {
		t.Fatalf("viewer request appended audit record count=%d", got)
	}

	briefing := request(t, fixture.handler, http.MethodPost, "/api/v1/briefings", supervisorIdentity, `{"briefing_id":"briefing-1","note":"review requested"}`)
	if briefing.Code != http.StatusAccepted {
		t.Fatalf("supervisor briefing status = %d, body = %s", briefing.Code, briefing.Body.String())
	}
	if executed, _ := responseData(t, briefing)["executed"].(bool); executed {
		t.Fatal("briefing endpoint claimed to execute an operational action")
	}

	acknowledgement := request(t, fixture.handler, http.MethodPost, "/api/v1/audit-actions", supervisorIdentity, `{"action":"acknowledged","target_kind":"recommendation","target_id":"recommendation-1","note":"reviewed"}`)
	if acknowledgement.Code != http.StatusCreated {
		t.Fatalf("supervisor acknowledgement status = %d, body = %s", acknowledgement.Code, acknowledgement.Body.String())
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

func TestStreamSendsSnapshotAndCleansUpOnCancellation(t *testing.T) {
	fixture := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	request.Header.Set(IdentityHeader, viewerIdentity)
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

func TestHealthAndVersionArePublicButReadDataNeedsDemoIdentity(t *testing.T) {
	fixture := newFixture(t)
	for _, path := range []string{"/api/v1/health", "/api/v1/version"} {
		response := request(t, fixture.handler, http.MethodGet, path, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
	}
	response := request(t, fixture.handler, http.MethodGet, "/api/v1/evidence/raw_event/raw-1", "", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated evidence status = %d", response.Code)
	}
}

type apiFixture struct {
	store   *store.Store
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

	resolver, err := NewSQLiteEvidenceResolver(database)
	if err != nil {
		t.Fatalf("new evidence resolver: %v", err)
	}
	broker := stream.NewBroker()
	server, err := New(Config{
		Recovery: replay.Runner{Canonical: database, Checkpoints: database, Projector: projector},
		Records:  database,
		Evidence: resolver,
		Stream:   broker,
		Clock: func() time.Time {
			return time.Date(2026, 7, 18, 12, 2, 0, 0, time.UTC)
		},
		NewID: sequentialIDs(),
	})
	if err != nil {
		t.Fatalf("new API server: %v", err)
	}
	return apiFixture{store: database, handler: server.Handler(), broker: broker}
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
		"audit_records_test":            false,
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
