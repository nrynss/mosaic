// Package e2e proves the interactive simulation and operator decision boundary
// through the real mosaicdemo composition root.
package e2e

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInteractiveSimulationE2EBoundary(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "mosaic-interactive.db")

	// 1. Start mosaicdemo
	first := startMosaicDemo(t, binary, root, databasePath, uiDirectory)

	// Verify health check
	health := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/health", "")
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", health.StatusCode)
	}

	// 2. Start simulation
	startResp := getResponse(t, http.MethodPost, first.baseURL+"/api/v1/simulation/start", "")
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("simulation start status = %d", startResp.StatusCode)
	}
	startData := advisoryResponseData(t, startResp)
	sessionID, _ := startData["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("session_id is empty on start")
	}
	if status, _ := startData["status"].(string); status != "running" {
		t.Fatalf("session status = %q, want running", status)
	}

	// 3. Wait for simulation to finish (ended status)
	deadline := time.Now().Add(10 * time.Second)
	var finalSessionData map[string]any
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for simulation to end")
		}
		statusResp := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/simulation/status", "")
		if statusResp.StatusCode != http.StatusOK {
			t.Fatalf("simulation status endpoint = %d", statusResp.StatusCode)
		}
		finalSessionData = advisoryResponseData(t, statusResp)
		if status, _ := finalSessionData["status"].(string); status == "ended" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify we got beats replayed
	beats, _ := finalSessionData["beats"].([]any)
	if len(beats) == 0 {
		t.Fatalf("no beats were replayed in the simulation session")
	}

	// 4. Perform Operator Action: Analyze (Terra)
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

	// 5. Perform Operator Action: Prepare Maintenance Handoff
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

	// 6. Perform Operator Action: Prepare Dispatch Handoff
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

	// 7. Verify public advisories with provenance
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

	// Find the audit record for maintenance handoff
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

	// 8. Verify COP remains unchanged (state revision should still be 9)
	copResp := getResponse(t, http.MethodGet, first.baseURL+"/api/v1/cop", "")
	defer copResp.Body.Close()
	copData := advisoryResponseData(t, copResp)
	if revision, _ := copData["state_revision"].(float64); revision != 9 {
		t.Fatalf("public COP revision = %#v, want 9", copData["state_revision"])
	}

	// Stop the first run
	first.stop(t)

	// 9. Retained restart: start again with same SQLite database
	second := startMosaicDemo(t, binary, root, databasePath, uiDirectory)
	defer second.stop(t)

	// Retrieve advisories on the second run to verify persistence
	secondAdvisoriesResp := getResponse(t, http.MethodGet, second.baseURL+"/api/v1/advisories", "")
	defer secondAdvisoriesResp.Body.Close()
	if secondAdvisoriesResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/advisories status = %d (second run)", secondAdvisoriesResp.StatusCode)
	}
	secondAdvisoriesData := advisoryResponseData(t, secondAdvisoriesResp)
	secondAuditRecords, ok := secondAdvisoriesData["audit_records"].([]any)
	if !ok || len(secondAuditRecords) == 0 {
		t.Fatalf("audit_records is missing or empty on second run: %#v", secondAdvisoriesData)
	}

	// Verify maintenance handoff is still present in the list of audit records
	foundMaintHandoff = false
	for _, recordVal := range secondAuditRecords {
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
		t.Fatalf("maintenance handoff audit record not persisted: %#v", secondAuditRecords)
	}
}

func readBodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	// Re-assign body so it can be closed or read again if needed
	resp.Body = io.NopCloser(strings.NewReader(string(b)))
	return b
}
