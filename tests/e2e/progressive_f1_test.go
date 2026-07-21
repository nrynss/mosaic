// F1 e2e proofs: simulation-driven progressive projection, session isolation,
// and cassette mode parity through the real mosaicdemo binary.
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

// TestF1ProgressiveProjectionAndSessionIsolationE2E extends interactive_simulation
// with intermediate COP sampling and a second sequential Play in-process
// (no restart) to prove session isolation + durable history retention.
func TestF1ProgressiveProjectionAndSessionIsolationE2E(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "f1-progressive-e2e.db")

	proc := startMosaicDemoProgressiveWithEnv(t, binary, root, databasePath, uiDirectory, map[string]string{
		// Slow enough to observe intermediate COP revisions during Play.
		"MOSAIC_SIM_BEAT_SPACING": "50ms",
		"MOSAIC_SEED_ON_START":    "0",
		"MOSAIC_SIM_MODE":         "fixture",
	})
	defer proc.stop(t)

	// Default fixture/passthrough mode is surfaced.
	version := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/version", ""))
	if mode, _ := version["cassette_mode"].(string); mode != "passthrough" {
		t.Fatalf("cassette_mode before Play = %#v, want passthrough", version["cassette_mode"])
	}

	// Empty board before Play.
	copBefore := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := copBefore["state_revision"].(float64); rev != 0 {
		t.Fatalf("COP before Play revision = %#v, want 0", copBefore["state_revision"])
	}

	startResp := getResponse(t, http.MethodPost, proc.baseURL+"/api/v1/simulation/start", "")
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("simulation start status = %d\n%s", startResp.StatusCode, proc.output.String())
	}
	startData := advisoryResponseData(t, startResp)
	session1, _ := startData["session_id"].(string)
	if session1 == "" {
		t.Fatal("session_id empty on first start")
	}

	// Mid-session progressive samples: ≥2 distinct revisions in (0, 9).
	seen := map[int]bool{}
	var samples []int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		copResp := getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", "")
		cop := advisoryResponseData(t, copResp)
		revF, _ := cop["state_revision"].(float64)
		rev := int(revF)
		if rev > 0 && !seen[rev] {
			seen[rev] = true
			samples = append(samples, rev)
		}
		statusResp := getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/simulation/status", "")
		statusData := advisoryResponseData(t, statusResp)
		if status, _ := statusData["status"].(string); status == "ended" && rev == 9 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !seen[9] {
		t.Fatalf("final revision 9 not observed; samples=%v\n%s", samples, proc.output.String())
	}
	intermediateCount := 0
	for rev := range seen {
		if rev > 0 && rev < 9 {
			intermediateCount++
		}
	}
	if intermediateCount < 2 {
		t.Fatalf("intermediate COP revisions = %d; samples=%v (want ≥2 in (0,9); bulk-seed smell)\n%s",
			intermediateCount, samples, proc.output.String())
	}

	// Progressive advisories for active session.
	adv := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/advisories", ""))
	if insights, _ := adv["insights"].([]any); len(insights) != 2 {
		t.Fatalf("insights after Play = %d, want 2", len(insights))
	}
	if recs, _ := adv["recommendations"].([]any); len(recs) != 1 {
		t.Fatalf("recommendations after Play = %d, want 1", len(recs))
	}

	// End → empty board (session isolation).
	endResp := getResponse(t, http.MethodPost, proc.baseURL+"/api/v1/simulation/end", "")
	if endResp.StatusCode != http.StatusOK {
		t.Fatalf("end status = %d", endResp.StatusCode)
	}
	endResp.Body.Close()
	copAfterEnd := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := copAfterEnd["state_revision"].(float64); rev != 0 {
		t.Fatalf("COP after End = %#v, want 0", copAfterEnd["state_revision"])
	}
	advAfterEnd := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/advisories", ""))
	if insights, _ := advAfterEnd["insights"].([]any); len(insights) != 0 {
		t.Fatalf("insights after End = %d, want 0", len(insights))
	}

	// Durable history retained while board is empty.
	db, err := store.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	events, err := db.ListCanonicalEventsAfter(context.Background(), 0)
	if err != nil {
		_ = db.Close()
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 9 {
		_ = db.Close()
		t.Fatalf("durable canonical events = %d, want 9 after first session", len(events))
	}
	history, err := db.ReadAdvisoryHistory(context.Background())
	_ = db.Close()
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history.Insights) != 2 || len(history.Recommendations) != 1 {
		t.Fatalf("durable advisories = insights:%d recs:%d, want 2/1", len(history.Insights), len(history.Recommendations))
	}

	// Second sequential Play (same process): new session, board fills again, no leak.
	start2 := getResponse(t, http.MethodPost, proc.baseURL+"/api/v1/simulation/start", "")
	if start2.StatusCode != http.StatusOK {
		t.Fatalf("second start status = %d\n%s", start2.StatusCode, proc.output.String())
	}
	start2Data := advisoryResponseData(t, start2)
	session2, _ := start2Data["session_id"].(string)
	if session2 == "" || session2 == session1 {
		t.Fatalf("session2 = %q, session1 = %q; want distinct epochs", session2, session1)
	}

	waitSimulationEnded(t, proc, 30*time.Second)
	cop2 := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := cop2["state_revision"].(float64); rev != 9 {
		t.Fatalf("COP after second Play = %#v, want 9\n%s", cop2["state_revision"], proc.output.String())
	}
	// IntactRestart must re-index session-scoped advisories for the new epoch.
	adv2 := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/advisories", ""))
	if insights, _ := adv2["insights"].([]any); len(insights) != 2 {
		t.Fatalf("insights after second Play = %d, want 2", len(insights))
	}
	if recs, _ := adv2["recommendations"].([]any); len(recs) != 1 {
		t.Fatalf("recommendations after second Play = %d, want 1", len(recs))
	}

	end2 := getResponse(t, http.MethodPost, proc.baseURL+"/api/v1/simulation/end", "")
	if end2.StatusCode != http.StatusOK {
		t.Fatalf("second end status = %d", end2.StatusCode)
	}
	end2.Body.Close()
	copFinal := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := copFinal["state_revision"].(float64); rev != 0 {
		t.Fatalf("COP after second End = %#v, want 0", copFinal["state_revision"])
	}
	advFinal := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/advisories", ""))
	if insights, _ := advFinal["insights"].([]any); len(insights) != 0 {
		t.Fatalf("insights after second End = %d, want 0", len(insights))
	}
}

// TestF1ReplayModeProgressiveParityE2E proves MOSAIC_SIM_MODE=replay composes
// without a key, surfaces cassette_mode=replay, and progressive Play still
// reaches the fixture continuum board (no network; fixture advisories, not
// banked operator cassette responses).
func TestF1ReplayModeProgressiveParityE2E(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "f1-replay-e2e.db")
	cassetteDir := t.TempDir()

	proc := startMosaicDemoProgressiveWithEnv(t, binary, root, databasePath, uiDirectory, map[string]string{
		"MOSAIC_SIM_BEAT_SPACING": "1ms",
		"MOSAIC_SEED_ON_START":    "0",
		"MOSAIC_SIM_MODE":         "replay",
		"MOSAIC_CASSETTE_DIR":     cassetteDir,
		// Explicitly clear any ambient key so replay path cannot hit network.
		"OPENAI_API_KEY": "",
	})
	defer proc.stop(t)

	version := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/version", ""))
	if mode, _ := version["cassette_mode"].(string); mode != "replay" {
		t.Fatalf("cassette_mode = %#v, want replay", version["cassette_mode"])
	}

	copBefore := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := copBefore["state_revision"].(float64); rev != 0 {
		t.Fatalf("COP before Play under replay = %#v, want 0", copBefore["state_revision"])
	}

	startResp := getResponse(t, http.MethodPost, proc.baseURL+"/api/v1/simulation/start", "")
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start under replay = %d\n%s", startResp.StatusCode, proc.output.String())
	}
	startResp.Body.Close()

	waitSimulationEnded(t, proc, 30*time.Second)

	copAfter := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := copAfter["state_revision"].(float64); rev != 9 {
		t.Fatalf("COP after progressive under replay = %#v, want 9\n%s", copAfter["state_revision"], proc.output.String())
	}
	// Mode still replay after Play (process-level, not hot-swapped).
	versionAfter := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/version", ""))
	if mode, _ := versionAfter["cassette_mode"].(string); mode != "replay" {
		t.Fatalf("cassette_mode after Play = %#v, want replay", versionAfter["cassette_mode"])
	}
	// Advisories also surface cassette_mode.
	adv := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/advisories", ""))
	if mode, _ := adv["cassette_mode"].(string); mode != "replay" {
		t.Fatalf("advisories cassette_mode = %#v, want replay", adv["cassette_mode"])
	}
	if insights, _ := adv["insights"].([]any); len(insights) != 2 {
		t.Fatalf("insights under replay progressive = %d, want 2", len(insights))
	}
}

// TestF1RecordWithoutKeyDemotesE2E proves record mode without a key demotes to
// passthrough and progressive Play remains the deterministic fixture path.
func TestF1RecordWithoutKeyDemotesE2E(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "f1-record-demote-e2e.db")

	proc := startMosaicDemoProgressiveWithEnv(t, binary, root, databasePath, uiDirectory, map[string]string{
		"MOSAIC_SIM_BEAT_SPACING": "1ms",
		"MOSAIC_SEED_ON_START":    "0",
		"MOSAIC_SIM_MODE":         "record",
		"MOSAIC_TERRA_PROVIDER":   "live",
		"MOSAIC_SOL_PROVIDER":     "live",
		"MOSAIC_CASSETTE_DIR":     t.TempDir(),
		"OPENAI_API_KEY":          "",
	})
	defer proc.stop(t)

	version := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/version", ""))
	if mode, _ := version["cassette_mode"].(string); mode != "passthrough" {
		t.Fatalf("record without key cassette_mode = %#v, want passthrough demotion", version["cassette_mode"])
	}

	startResp := getResponse(t, http.MethodPost, proc.baseURL+"/api/v1/simulation/start", "")
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start = %d\n%s", startResp.StatusCode, proc.output.String())
	}
	startResp.Body.Close()
	waitSimulationEnded(t, proc, 30*time.Second)

	cop := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/cop", ""))
	if rev, _ := cop["state_revision"].(float64); rev != 9 {
		t.Fatalf("COP after demoted-record progressive = %#v, want 9", cop["state_revision"])
	}
}

// startMosaicDemoProgressiveWithEnv starts mosaicdemo on the progressive path
// with extra env overrides (F1 mode parity). Caller keys replace defaults and
// ambient process env for those keys (including clearing OPENAI_API_KEY).
func startMosaicDemoProgressiveWithEnv(t *testing.T, binary, root, databasePath, uiDirectory string, extraEnv map[string]string) mosaicDemoProcess {
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

	envMap := map[string]string{
		"MOSAIC_SIM_BEAT_SPACING": "1ms",
		"MOSAIC_SEED_ON_START":    "0",
	}
	for k, v := range extraEnv {
		envMap[k] = v
	}

	base := os.Environ()
	filtered := make([]string, 0, len(base)+len(envMap))
	overrideKeys := make(map[string]struct{}, len(envMap))
	for k := range envMap {
		overrideKeys[k] = struct{}{}
	}
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overrideKeys[key]; ok {
			continue
		}
		filtered = append(filtered, entry)
	}
	for k, v := range envMap {
		filtered = append(filtered, k+"="+v)
	}
	command.Env = filtered

	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start mosaicdemo progressive+env: %v", err)
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

func waitSimulationEnded(t *testing.T, proc mosaicDemoProcess, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statusResp := getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/simulation/status", "")
		if statusResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(statusResp.Body)
			statusResp.Body.Close()
			t.Fatalf("simulation status = %d: %s", statusResp.StatusCode, body)
		}
		data := advisoryResponseData(t, statusResp)
		if status, _ := data["status"].(string); status == "ended" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for simulation to end\n%s", proc.output.String())
}
