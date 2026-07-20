package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/store"
)

func TestNewApplicationSeedsFixtureOnlyOnce(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	database := filepath.Join(t.TempDir(), "mosaic.db")
	configuration := config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  database,
		UIDirectory:   ui,
		AssetRoot:     root,
	}

	first, err := newApplication(context.Background(), configuration)
	if err != nil {
		t.Fatalf("compose first application: %v", err)
	}
	assertFixtureCOP(t, first.handler)
	assertFixtureOperations(t, first.handler)
	assertFixtureAdvisories(t, first.handler)
	if err := first.close(); err != nil {
		t.Fatalf("close first application: %v", err)
	}

	second, err := newApplication(context.Background(), configuration)
	if err != nil {
		t.Fatalf("compose second application: %v", err)
	}
	assertFixtureCOP(t, second.handler)
	assertFixtureAdvisories(t, second.handler)
	if err := second.close(); err != nil {
		t.Fatalf("close second application: %v", err)
	}

	durable, err := store.Open(context.Background(), database)
	if err != nil {
		t.Fatalf("reopen durable fixture store: %v", err)
	}
	t.Cleanup(func() { _ = durable.Close() })
	events, err := durable.ListCanonicalEventsAfter(context.Background(), 0)
	if err != nil {
		t.Fatalf("read durable canonical events: %v", err)
	}
	if len(events) != 9 {
		t.Fatalf("canonical event count after two starts = %d, want 9", len(events))
	}
	history, err := durable.ReadAdvisoryHistory(context.Background())
	if err != nil {
		t.Fatalf("read durable advisory history: %v", err)
	}
	if len(history.Insights) != 2 || len(history.Recommendations) != 1 || len(history.ModelRuns) != 3 || len(history.AuditRecords) != 2 {
		t.Fatalf("advisory history after two starts = %#v", history)
	}
}

func TestNewApplicationRejectsPartialAdvisoryHistory(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	databasePath := filepath.Join(t.TempDir(), "mosaic.db")
	durable, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open durable store: %v", err)
	}
	scenario, err := simulator.New(simulator.Config{
		Store:      durable,
		SchemaDir:  filepath.Join(root, "ontology"),
		FixtureDir: filepath.Join(root, "datasets", simulator.DomesticDisturbance),
	})
	if err != nil {
		_ = durable.Close()
		t.Fatalf("compose scenario: %v", err)
	}
	if _, err := scenario.Run(ctx); err != nil {
		_ = durable.Close()
		t.Fatalf("seed scenario: %v", err)
	}
	if err := durable.AppendModelRun(ctx, gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          "modelrun-fixture-terra-insight-domestic-access-001",
		Agent:               "terra",
		Provider:            "mosaic-fixture",
		Model:               "mosaic-fixture-terra-v1",
		PromptVersion:       "domestic-disturbance-fixture-v1",
		OutputSchemaVersion: "1.0.0",
		StateRevision:       7,
		OutputIds:           []any{"insight-domestic-access-001"},
		ValidationStatus:    "valid",
		StartedAt:           "2026-07-18T10:05:00Z",
		CompletedAt:         "2026-07-18T10:05:01Z",
	}); err != nil {
		_ = durable.Close()
		t.Fatalf("seed partial advisory Model Run: %v", err)
	}
	if err := durable.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}

	_, err = newApplication(ctx, config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  databasePath,
		UIDirectory:   makeDashboard(t),
		AssetRoot:     root,
	})
	if !errors.Is(err, simulator.ErrPartialAdvisoryStage) {
		t.Fatalf("partial advisory startup error = %v, want %v", err, simulator.ErrPartialAdvisoryStage)
	}
}
func TestComposeHandlerNeverFallsBackForAPI(t *testing.T) {
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "api")
	})
	// The standalone handler below avoids an HTTP client and makes the routing
	// assertion independent of dashboard implementation details.
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "dashboard")
	})
	handler := composeHandler(apiHandler, static)

	apiRequest := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/not-registered", nil)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusTeapot || apiResponse.Body.String() != "api" {
		t.Fatalf("API route fell through to dashboard: status=%d body=%q", apiResponse.Code, apiResponse.Body.String())
	}

	dashboardRequest := httptest.NewRequest(http.MethodGet, "http://mosaic.test/", nil)
	dashboardResponse := httptest.NewRecorder()
	handler.ServeHTTP(dashboardResponse, dashboardRequest)
	if dashboardResponse.Code != http.StatusOK || dashboardResponse.Body.String() != "dashboard" {
		t.Fatalf("dashboard route = status=%d body=%q", dashboardResponse.Code, dashboardResponse.Body.String())
	}
}

func TestDashboardHandlerContainsTraversalAndFallsBackToIndex(t *testing.T) {
	ui := makeDashboard(t)
	handler, err := newDashboardHandler(ui)
	if err != nil {
		t.Fatalf("create dashboard handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://mosaic.test/deep-link", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Mosaic dashboard") {
		t.Fatalf("dashboard fallback = status=%d body=%q", response.Code, response.Body.String())
	}

	traversal := httptest.NewRequest(http.MethodGet, "http://mosaic.test/%2e%2e/secret.txt", nil)
	traversalResponse := httptest.NewRecorder()
	handler.ServeHTTP(traversalResponse, traversal)
	if traversalResponse.Code != http.StatusBadRequest {
		t.Fatalf("traversal status = %d, want %d", traversalResponse.Code, http.StatusBadRequest)
	}
}

func TestParseConfigUsesRuntimeEnvironment(t *testing.T) {
	root := t.TempDir()
	ui := makeDashboard(t)
	configuration, err := parseConfig(nil, func(name string) string {
		switch name {
		case "MOSAIC_LISTEN_ADDR":
			return ":8080"
		case "MOSAIC_DB_PATH":
			return filepath.Join(root, "mosaic.db")
		case "MOSAIC_UI_DIR":
			return ui
		case "MOSAIC_ASSET_ROOT":
			return root
		case "OPENAI_API_KEY":
			return "should-not-appear-in-flags"
		case "MOSAIC_TERRA_PROVIDER":
			return "live"
		case "MOSAIC_RECURRENCE_AREA":
			return "bridge-7"
		case "MOSAIC_RECURRENCE_WINDOW":
			return "24h"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("parse runtime environment: %v", err)
	}
	if configuration.ListenAddress != ":8080" || configuration.DatabasePath != filepath.Join(root, "mosaic.db") || configuration.UIDirectory != ui || configuration.AssetRoot != root {
		t.Fatalf("runtime configuration = %#v", configuration)
	}
	if configuration.ModelEnv.APIKey != "should-not-appear-in-flags" {
		t.Fatalf("server-only key not loaded: %#v", configuration.ModelEnv)
	}
	if configuration.ModelEnv.Terra != contracts.ProviderLive {
		t.Fatalf("terra provider = %s, want live", configuration.ModelEnv.Terra)
	}
	if configuration.RecurrenceArea != "bridge-7" || configuration.RecurrenceWindow != 24*time.Hour {
		t.Fatalf("recurrence config = area=%q window=%s", configuration.RecurrenceArea, configuration.RecurrenceWindow)
	}
}

func TestNewApplicationWiresSimulationModelsAndRecurrence(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	database := filepath.Join(t.TempDir(), "mosaic.db")
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  database,
		UIDirectory:   ui,
		AssetRoot:     root,
	})
	if err != nil {
		t.Fatalf("compose application: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	if app.simulation == nil {
		t.Fatal("simulation controller was not wired")
	}
	if app.recurrence == nil {
		t.Fatal("recurrence detector was not wired")
	}
	if app.modelProviders[openaimodel.AgentTerra] != contracts.ProviderFixture {
		t.Fatalf("default terra provider = %s, want fixture", app.modelProviders[openaimodel.AgentTerra])
	}

	// Simulation status is available over the composed API.
	statusReq := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/simulation/status", nil)
	statusResp := httptest.NewRecorder()
	app.handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("simulation status = %d: %s", statusResp.Code, statusResp.Body.String())
	}

	// Fixture startup COP remains revision 9.
	assertFixtureCOP(t, app.handler)
	assertFixtureAdvisories(t, app.handler)
}

func TestNewApplicationLiveSelectionOnlyWithSecret(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)

	// Explicit live without secret → fixture effective selection.
	fallback, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "fallback.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		ModelEnv: modelEnv{
			APIKey: "",
			Luna:   contracts.ProviderLive,
			Terra:  contracts.ProviderLive,
			Sol:    contracts.ProviderLive,
		},
	})
	if err != nil {
		t.Fatalf("compose fallback application: %v", err)
	}
	t.Cleanup(func() { _ = fallback.close() })
	for _, agent := range []string{openaimodel.AgentLuna, openaimodel.AgentTerra, openaimodel.AgentSol} {
		if fallback.modelProviders[agent] != contracts.ProviderFixture {
			t.Fatalf("fallback %s = %s, want fixture", agent, fallback.modelProviders[agent])
		}
	}

	// Explicit live with secret → live effective selection (no network call at startup).
	live, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "live.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		ModelEnv: modelEnv{
			APIKey: "test-key",
			Luna:   contracts.ProviderLive,
			Terra:  contracts.ProviderLive,
			Sol:    contracts.ProviderLive,
		},
	})
	if err != nil {
		t.Fatalf("compose live application: %v", err)
	}
	t.Cleanup(func() { _ = live.close() })
	for _, agent := range []string{openaimodel.AgentLuna, openaimodel.AgentTerra, openaimodel.AgentSol} {
		if live.modelProviders[agent] != contracts.ProviderLive {
			t.Fatalf("live %s = %s, want live", agent, live.modelProviders[agent])
		}
	}
	// Deterministic fixture COP seed is unchanged even when live models are selected.
	assertFixtureCOP(t, live.handler)
}

func assertFixtureCOP(t *testing.T, handler http.Handler) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/cop", nil)
	request.Header.Set(api.IdentityHeader, "viewer-demo")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("COP status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"state_revision":9`) {
		t.Fatalf("COP did not contain expected fixture revision: %s", response.Body.String())
	}
}

func assertFixtureOperations(t *testing.T, handler http.Handler) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/operations", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("operations status = %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Data struct {
			Recovery struct {
				Status        string `json:"status"`
				StateRevision int64  `json:"state_revision"`
			} `json:"recovery"`
			Counts struct {
				RawEvents         int `json:"raw_events"`
				CanonicalEvents   int `json:"canonical_events"`
				ProjectedEvents   int `json:"projected_events"`
				UnprojectedEvents int `json:"unprojected_events"`
				Checkpoints       int `json:"checkpoints"`
			} `json:"counts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode operations response: %v", err)
	}
	if body.Data.Recovery.Status != "recovered" || body.Data.Recovery.StateRevision != 9 {
		t.Fatalf("operations recovery = %#v", body.Data.Recovery)
	}
	if got, want := body.Data.Counts.RawEvents, 10; got != want {
		t.Fatalf("operations raw event count = %d, want %d", got, want)
	}
	if got, want := body.Data.Counts.CanonicalEvents, 9; got != want {
		t.Fatalf("operations canonical event count = %d, want %d", got, want)
	}
	if got, want := body.Data.Counts.ProjectedEvents, 9; got != want {
		t.Fatalf("operations projected event count = %d, want %d", got, want)
	}
	if body.Data.Counts.UnprojectedEvents != 0 || body.Data.Counts.Checkpoints != 9 {
		t.Fatalf("operations projection counts = %#v", body.Data.Counts)
	}
}
func assertFixtureAdvisories(t *testing.T, handler http.Handler) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/advisories", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("advisories status = %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Data struct {
			Status   string `json:"status"`
			Insights []struct {
				InsightID string `json:"insight_id"`
				Status    string `json:"status"`
			} `json:"insights"`
			Recommendations []struct {
				RecommendationID string `json:"recommendation_id"`
				Status           string `json:"status"`
			} `json:"recommendations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode advisory response: %v", err)
	}
	if body.Data.Status != "fixture-composed" {
		t.Fatalf("advisory composition status = %q, want fixture-composed", body.Data.Status)
	}
	if len(body.Data.Insights) != 2 || body.Data.Insights[0].Status != "superseded" || body.Data.Insights[1].Status != "superseded" {
		t.Fatalf("advisory insight lifecycle = %#v", body.Data.Insights)
	}
	if len(body.Data.Recommendations) != 1 || body.Data.Recommendations[0].Status != "not_current" {
		t.Fatalf("advisory recommendation lifecycle = %#v", body.Data.Recommendations)
	}
}
func makeDashboard(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html><title>Mosaic dashboard</title>"), 0o600); err != nil {
		t.Fatalf("write test dashboard: %v", err)
	}
	return directory
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := simulator.RepositoryRoot(".")
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	database := filepath.Join(t.TempDir(), "mosaic.db")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notice := &notifyingWriter{ready: make(chan struct{})}
	finished := make(chan error, 1)
	go func() {
		finished <- run(ctx, nil, func(name string) string {
			switch name {
			case "MOSAIC_LISTEN_ADDR":
				return "127.0.0.1:0"
			case "MOSAIC_DB_PATH":
				return database
			case "MOSAIC_UI_DIR":
				return ui
			case "MOSAIC_ASSET_ROOT":
				return root
			default:
				return ""
			}
		}, notice)
	}()

	select {
	case <-notice.ready:
	case err := <-finished:
		t.Fatalf("run returned before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("demo did not begin listening within five seconds")
	}
	line := strings.TrimSpace(notice.String())
	const prefix = "mosaicdemo listening on http://"
	address := strings.TrimPrefix(line, prefix)
	address = strings.TrimSuffix(address, " (synthetic fixture ready)")
	if address == line || address == "" {
		t.Fatalf("unexpected startup line %q", line)
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/cop", nil)
	if err != nil {
		t.Fatalf("create running COP request: %v", err)
	}
	request.Header.Set(api.IdentityHeader, "viewer-demo")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("read running COP endpoint: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("running COP status = %d", response.StatusCode)
	}

	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("graceful run shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demo did not shut down within five seconds")
	}
}

type notifyingWriter struct {
	mu    sync.Mutex
	once  sync.Once
	ready chan struct{}
	text  string
}

func (w *notifyingWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	w.text += string(value)
	w.mu.Unlock()
	w.once.Do(func() { close(w.ready) })
	return len(value), nil
}

func (w *notifyingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text
}
