// Package e2e proves the interactive simulation and operator decision boundary
// through the real mosaicdemo composition root.
package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/store"
)

func TestInteractiveSimulationE2EBoundary(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "mosaic-interactive.db")

	// Progressive path: no bulk seed — empty board until Play.
	first := startMosaicDemoProgressive(t, binary, root, databasePath, uiDirectory)

	health := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/health", "")
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", health.StatusCode)
	}

	// Before Play: empty COP (revision 0)
	copBefore := advisoryResponseData(t, getResponse(t, http.MethodGet, first.baseURL+"/api/v1/cop", ""))
	if revision, _ := copBefore["state_revision"].(float64); revision != 0 {
		t.Fatalf("COP before Play revision = %#v, want 0", copBefore["state_revision"])
	}

	startResp := getResponse(t, http.MethodPost, first.baseURL+"/api/v1/simulation/start", "")
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("simulation start status = %d\n%s", startResp.StatusCode, first.output.String())
	}
	startData := advisoryResponseData(t, startResp)
	sessionID, _ := startData["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("session_id is empty on start")
	}
	if status, _ := startData["status"].(string); status != "running" {
		t.Fatalf("session status = %q, want running", status)
	}

	// Wait for simulation to finish (10 beats @ 1ms + sync process work).
	deadline := time.Now().Add(30 * time.Second)
	var finalSessionData map[string]any
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for simulation to end\n%s", first.output.String())
		}
		statusResp := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/simulation/status", "")
		if statusResp.StatusCode != http.StatusOK {
			t.Fatalf("simulation status endpoint = %d", statusResp.StatusCode)
		}
		finalSessionData = advisoryResponseData(t, statusResp)
		if status, _ := finalSessionData["status"].(string); status == "ended" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	beats, _ := finalSessionData["beats"].([]any)
	if len(beats) == 0 {
		t.Fatalf("no beats were replayed in the simulation session")
	}

	// After natural end: final board stays visible (Active left set) at rev 9
	copAfter := advisoryResponseData(t, getResponse(t, http.MethodGet, first.baseURL+"/api/v1/cop", ""))
	if revision, _ := copAfter["state_revision"].(float64); revision != 9 {
		t.Fatalf("COP after Play revision = %#v, want 9\nprocess:\n%s", copAfter["state_revision"], first.output.String())
	}

	// Progressive advisories landed for the active session (Terra@7, Sol@7, Terra obsolete@9).
	// SessionAdvisories filters GET /advisories to ids recorded during Play.
	advisoriesBeforeOp := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/advisories", "")
	advisoriesBeforeBody, err := io.ReadAll(advisoriesBeforeOp.Body)
	advisoriesBeforeOp.Body.Close()
	if err != nil {
		t.Fatalf("read advisories: %v", err)
	}
	advisoriesBeforeData := decodeData(t, advisoriesBeforeBody)
	insights, _ := advisoriesBeforeData["insights"].([]any)
	if len(insights) != 2 {
		t.Fatalf("fixture insights after progressive Play = %d, want 2: %s", len(insights), advisoriesBeforeBody)
	}
	recs, _ := advisoriesBeforeData["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("fixture recommendations after progressive Play = %d, want 1", len(recs))
	}

	analyzeBody := `{"evidence": [{"kind": "raw_event", "id": "raw-domestic-001-call", "explanation": "E2E analyze request"}], "note": "E2E review note"}`
	analyzeResp := getResponse(t, http.MethodPost, first.baseURL+"/api/v1/operator/analyze", analyzeBody)
	defer analyzeResp.Body.Close()
	if analyzeResp.StatusCode != http.StatusOK {
		t.Fatalf("analyze status = %d\nprocess output:\n%s", analyzeResp.StatusCode, first.output.String())
	}
	analyzeData := decodeData(t, readBodyBytes(t, analyzeResp))
	if exec, _ := analyzeData["executed"].(bool); exec {
		t.Fatalf("operator action analyze reported executed: true")
	}
	audit, _ := analyzeData["audit_record"].(map[string]any)
	if actor, _ := audit["actor_id"].(string); actor != "public-demo" {
		t.Fatalf("audit actor = %q, want public-demo", actor)
	}

	maintBody := `{"recipient": "maintenance", "target_kind": "system", "target_id": "operator-maintenance-handoff", "note": "Prepare maintenance road condition notes"}`
	maintResp := getResponse(t, http.MethodPost, first.baseURL+"/api/v1/operator/prepare-handoff", maintBody)
	defer maintResp.Body.Close()
	if maintResp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare maintenance handoff status = %d", maintResp.StatusCode)
	}
	maintData := decodeData(t, readBodyBytes(t, maintResp))
	if exec, _ := maintData["executed"].(bool); exec {
		t.Fatalf("handoff reported executed: true")
	}
	if deliv, _ := maintData["delivered"].(bool); deliv {
		t.Fatalf("handoff reported delivered: true")
	}
	if status, _ := maintData["handoff_status"].(string); status != "recorded" {
		t.Fatalf("handoff_status = %q, want recorded", status)
	}

	dispBody := `{"recipient": "dispatch", "target_kind": "system", "target_id": "operator-dispatch-handoff", "note": "Prepare dispatch briefing notes"}`
	dispResp := getResponse(t, http.MethodPost, first.baseURL+"/api/v1/operator/prepare-handoff", dispBody)
	defer dispResp.Body.Close()
	if dispResp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare dispatch handoff status = %d", dispResp.StatusCode)
	}
	dispData := decodeData(t, readBodyBytes(t, dispResp))
	if exec, _ := dispData["executed"].(bool); exec {
		t.Fatalf("handoff reported executed: true")
	}
	if deliv, _ := dispData["delivered"].(bool); deliv {
		t.Fatalf("handoff reported delivered: true")
	}

	advisoriesResp := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/advisories", "")
	defer advisoriesResp.Body.Close()
	if advisoriesResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/advisories status = %d", advisoriesResp.StatusCode)
	}
	advisoriesEncoded, err := io.ReadAll(advisoriesResp.Body)
	if err != nil {
		t.Fatalf("read advisories response: %v", err)
	}
	for _, forbidden := range []string{"payload_bytes_b64", "raw_sha256", "prompt", "response", "failure_detail"} {
		if strings.Contains(string(advisoriesEncoded), forbidden) {
			t.Fatalf("advisories response exposed forbidden field %q: %s", forbidden, advisoriesEncoded)
		}
	}
	advisoriesData := decodeData(t, advisoriesEncoded)
	auditRecords, ok := advisoriesData["audit_records"].([]any)
	if !ok || len(auditRecords) == 0 {
		t.Fatalf("audit_records is missing or empty in advisories: %#v", advisoriesData)
	}

	var foundMaintHandoff bool
	for _, recordVal := range auditRecords {
		record, ok := recordVal.(map[string]any)
		if !ok {
			continue
		}
		note, _ := record["note"].(string)
		if record["action"] == "noted" && strings.Contains(note, "recipient=maintenance") {
			foundMaintHandoff = true
			break
		}
	}
	if !foundMaintHandoff {
		t.Fatalf("maintenance handoff audit record not found in advisories: %s", advisoriesEncoded)
	}

	copResp := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/cop", "")
	defer copResp.Body.Close()
	copData := advisoryResponseData(t, copResp)
	if revision, _ := copData["state_revision"].(float64); revision != 9 {
		t.Fatalf("public COP revision = %#v, want 9", copData["state_revision"])
	}

	// Explicit End clears ActiveSession → empty advisories board policy (C3).
	endResp := getResponse(t, http.MethodPost, first.baseURL+"/api/v1/simulation/end", "")
	if endResp.StatusCode != http.StatusOK {
		t.Fatalf("simulation end status = %d\n%s", endResp.StatusCode, first.output.String())
	}
	endResp.Body.Close()
	advisoriesAfterEnd := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/advisories", "")
	advisoriesAfterEndBody, err := io.ReadAll(advisoriesAfterEnd.Body)
	advisoriesAfterEnd.Body.Close()
	if err != nil {
		t.Fatalf("read advisories after end: %v", err)
	}
	advisoriesAfterEndData := decodeData(t, advisoriesAfterEndBody)
	if insights, _ := advisoriesAfterEndData["insights"].([]any); len(insights) != 0 {
		t.Fatalf("insights after End = %d, want empty board: %s", len(insights), advisoriesAfterEndBody)
	}
	if recs, _ := advisoriesAfterEndData["recommendations"].([]any); len(recs) != 0 {
		t.Fatalf("recommendations after End = %d, want empty board", len(recs))
	}
	copAfterEnd := advisoryResponseData(t, getResponse(t, http.MethodGet, first.baseURL+"/api/v1/cop", ""))
	if revision, _ := copAfterEnd["state_revision"].(float64); revision != 0 {
		t.Fatalf("COP after End revision = %#v, want 0 empty board", copAfterEnd["state_revision"])
	}

	first.stop(t)

	// Retained restart: durable records survive; progressive board is empty until Play.
	second := startMosaicDemoProgressive(t, binary, root, databasePath, uiDirectory)
	defer second.stop(t)

	copRestart := advisoryResponseData(t, getResponse(t, http.MethodGet, second.baseURL+"/api/v1/cop", ""))
	if revision, _ := copRestart["state_revision"].(float64); revision != 0 {
		t.Fatalf("COP after restart without Play = %#v, want 0", copRestart["state_revision"])
	}

	// Fixture insights/recs + fixture audits (2) + operator handoffs persist in store.
	// model_runs: 3 fixture terra/sol (luna runs exist separately in model_runs table)
	database, err := store.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open retained database: %v", err)
	}
	defer database.Close()
	history, err := database.ReadAdvisoryHistory(context.Background())
	if err != nil {
		t.Fatalf("read retained advisory history: %v", err)
	}
	if len(history.Insights) != 2 || len(history.Recommendations) != 1 {
		t.Fatalf("retained fixture advisories = insights:%d recs:%d, want 2/1", len(history.Insights), len(history.Recommendations))
	}
	if len(history.AuditRecords) < 4 {
		// 2 fixture audits + maintenance handoff + dispatch handoff (+ maybe analyze)
		t.Fatalf("retained audit_records = %d, want >= 4", len(history.AuditRecords))
	}
}

// startMosaicDemoProgressive starts mosaicdemo without bulk seed so Play drives
// real progressive EventLog.Append + sync domain process.
func startMosaicDemoProgressive(t *testing.T, binary, root, databasePath, uiDirectory string) mosaicDemoProcess {
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
	command.Env = append(os.Environ(),
		"MOSAIC_SIM_BEAT_SPACING=1ms",
		"MOSAIC_SEED_ON_START=0",
	)
	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start mosaicdemo progressive: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	baseURL := "http://" + address
	deadline := time.NewTimer(20 * time.Second)
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

func readBodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	resp.Body = io.NopCloser(strings.NewReader(string(b)))
	return b
}
