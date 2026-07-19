package main

import (
	"context"
	"encoding/json"
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
	"mosaic.local/mosaic/internal/simulator"
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
	if err := first.close(); err != nil {
		t.Fatalf("close first application: %v", err)
	}

	second, err := newApplication(context.Background(), configuration)
	if err != nil {
		t.Fatalf("compose second application: %v", err)
	}
	assertFixtureCOP(t, second.handler)
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
