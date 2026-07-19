// Package e2e proves the public fixture-advisory boundary through the real
// mosaicdemo composition root.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/simulator"
	"mosaic.local/mosaic/internal/store"
)

func TestFixtureComposedAdvisoryPublicBoundaryAndRetainedRestart(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "mosaic.db")

	first := startMosaicDemo(t, binary, root, databasePath, uiDirectory)
	assertPublicFixtureAdvisoryBoundary(t, first.baseURL)
	assertFixtureAdvisoryCounts(t, databasePath, 2, 1, 3, 2)
	assertImmutablePublicReview(t, first.baseURL)
	assertFixtureAdvisoryCounts(t, databasePath, 2, 1, 3, 3)
	first.stop(t)

	second := startMosaicDemo(t, binary, root, databasePath, uiDirectory)
	defer second.stop(t)
	assertPublicFixtureAdvisoryBoundary(t, second.baseURL)
	assertFixtureAdvisoryCounts(t, databasePath, 2, 1, 3, 3)
}

func assertPublicFixtureAdvisoryBoundary(t *testing.T, baseURL string) {
	t.Helper()

	dashboard := getResponse(t, http.MethodGet, baseURL+"/", "")
	defer dashboard.Body.Close()
	if dashboard.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d", dashboard.StatusCode)
	}

	cop := advisoryResponseData(t, getResponse(t, http.MethodGet, baseURL+"/api/v1/cop", ""))
	if revision, _ := cop["state_revision"].(float64); revision != 9 {
		t.Fatalf("public COP revision = %#v, want 9", cop["state_revision"])
	}

	response := getResponse(t, http.MethodGet, baseURL+"/api/v1/advisories", "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("public advisories status = %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read public advisories: %v", err)
	}
	for _, forbidden := range []string{"payload_bytes_b64", "raw_sha256", "prompt", "response", "failure_detail"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("advisories response exposed %q: %s", forbidden, encoded)
		}
	}
	data := decodeData(t, encoded)
	if status, _ := data["status"].(string); status != "fixture-composed" {
		t.Fatalf("advisory status = %q, want fixture-composed", status)
	}
	insights := arrayData(t, data, "insights")
	if len(insights) != 2 {
		t.Fatalf("public insight count = %d, want 2", len(insights))
	}
	for _, value := range insights {
		insight := objectData(t, value, "insight")
		if status, _ := insight["status"].(string); status != "superseded" {
			t.Fatalf("public insight status = %q, want superseded", status)
		}
	}
	recommendations := arrayData(t, data, "recommendations")
	if len(recommendations) != 1 {
		t.Fatalf("public recommendation count = %d, want 1", len(recommendations))
	}
	recommendation := objectData(t, recommendations[0], "recommendation")
	if status, _ := recommendation["status"].(string); status != "not_current" {
		t.Fatalf("public recommendation status = %q, want not_current", status)
	}

	evidence := advisoryResponseData(t, getResponse(t, http.MethodGet, baseURL+"/api/v1/evidence/insight/insight-domestic-access-001", ""))
	if resolved, _ := evidence["resolved"].(bool); !resolved {
		t.Fatalf("fixture insight evidence was not resolved: %#v", evidence)
	}

	operations := advisoryResponseData(t, getResponse(t, http.MethodGet, baseURL+"/api/v1/operations", ""))
	assertFixtureCapabilities(t, arrayData(t, operations, "capabilities"))
}

func assertImmutablePublicReview(t *testing.T, baseURL string) {
	t.Helper()
	body := `{"action":"acknowledged","target_kind":"recommendation","target_id":"recommendation-domestic-001","note":"P28 public fixture review."}`
	response := getResponse(t, http.MethodPost, baseURL+"/api/v1/audit-actions", body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		encoded, _ := io.ReadAll(response.Body)
		t.Fatalf("public fixture review status = %d: %s", response.StatusCode, encoded)
	}
	data := advisoryResponseData(t, response)
	if executed, _ := data["executed"].(bool); executed {
		t.Fatal("public fixture review reported an executed action")
	}
	audit := objectData(t, data["audit_record"], "audit record")
	if actor, _ := audit["actor_id"].(string); actor != "public-demo" {
		t.Fatalf("public review actor = %q, want public-demo", actor)
	}
}

func assertFixtureCapabilities(t *testing.T, capabilities []any) {
	t.Helper()
	want := map[string]struct{ mode, status string }{
		"terra_assessment": {"fixture_composed", "available"},
		"sol_advisory":     {"fixture_composed", "available"},
	}
	for _, value := range capabilities {
		capability := objectData(t, value, "capability")
		name, _ := capability["capability"].(string)
		expected, ok := want[name]
		if !ok {
			continue
		}
		if mode, _ := capability["mode"].(string); mode != expected.mode {
			t.Fatalf("capability %q mode = %q, want %q", name, mode, expected.mode)
		}
		if status, _ := capability["status"].(string); status != expected.status {
			t.Fatalf("capability %q status = %q, want %q", name, status, expected.status)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing fixture capabilities: %#v", want)
	}
}

func assertFixtureAdvisoryCounts(t *testing.T, databasePath string, insights, recommendations, modelRuns, audits int) {
	t.Helper()
	database, err := store.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open retained advisory database: %v", err)
	}
	defer database.Close()
	history, err := database.ReadAdvisoryHistory(context.Background())
	if err != nil {
		t.Fatalf("read retained advisory history: %v", err)
	}
	if len(history.Insights) != insights || len(history.Recommendations) != recommendations || len(history.ModelRuns) != modelRuns || len(history.AuditRecords) != audits {
		t.Fatalf("retained advisory counts = insights:%d recommendations:%d model_runs:%d audits:%d, want %d/%d/%d/%d", len(history.Insights), len(history.Recommendations), len(history.ModelRuns), len(history.AuditRecords), insights, recommendations, modelRuns, audits)
	}
}

type mosaicDemoProcess struct {
	baseURL string
	cancel  context.CancelFunc
	done    <-chan error
	output  *bytes.Buffer
}

func startMosaicDemo(t *testing.T, binary, root, databasePath, uiDirectory string) mosaicDemoProcess {
	t.Helper()
	address := freeAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary,
		"-listen-addr", address,
		"-db", databasePath,
		"-ui-dir", uiDirectory,
		"-asset-root", root,
	)
	command.Dir = root
	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start mosaicdemo: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	baseURL := "http://" + address
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		response, err := http.Get(baseURL + "/api/v1/health") // #nosec G107 -- bounded local test process.
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return mosaicDemoProcess{baseURL: baseURL, cancel: cancel, done: done, output: output}
			}
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("mosaicdemo exited before ready: %v\n%s", err, output.String())
		case <-deadline.C:
			cancel()
			t.Fatalf("mosaicdemo did not become ready\n%s", output.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (p mosaicDemoProcess) stop(t *testing.T) {
	t.Helper()
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("mosaicdemo did not stop\n%s", p.output.String())
	}
}

func buildMosaicDemo(t *testing.T, root string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "mosaicdemo")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.Command("go", "build", "-o", binary, "./cmd/mosaicdemo")
	command.Dir = root
	encoded, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build mosaicdemo: %v\n%s", err, encoded)
	}
	return binary
}

func testDashboard(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html><title>Mosaic P28 dashboard</title>"), 0o600); err != nil {
		t.Fatalf("write test dashboard: %v", err)
	}
	return directory
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate local port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release local port: %v", err)
	}
	return address
}

func getResponse(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return response
}

func advisoryResponseData(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return decodeData(t, encoded)
}

func decodeData(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode API envelope: %v\n%s", err, encoded)
	}
	return envelope.Data
}

func arrayData(t *testing.T, parent map[string]any, key string) []any {
	t.Helper()
	value, ok := parent[key].([]any)
	if !ok {
		t.Fatalf("%s = %#v, want array", key, parent[key])
	}
	return value
}

func objectData(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", label, value)
	}
	return object
}

func advisoryRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := simulator.RepositoryRoot(".")
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
}
