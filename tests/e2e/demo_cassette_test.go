// Demo cassette e2e: no-live CI replay of the committed bank, plus a gated
// live re-recorder (MOSAIC_RECORD_LIVE=1) that spends real OpenAI once.
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/democast"
	"mosaic.local/mosaic/internal/simulation/cassette"
)

// TestDemoCastReplayNoLiveE2E starts mosaicdemo in replay mode with the
// committed testdata/demo/cassettes bank, no OPENAI_API_KEY, drives the full
// manifest, and asserts every step hits (status ok/quarantined, fixture
// provider). Runs twice for determinism. Default CI path — not gated.
func TestDemoCastReplayNoLiveE2E(t *testing.T) {
	root := advisoryRepositoryRoot(t)
	bank := democast.CassetteDir(root)
	store, err := cassette.NewFileStore(bank)
	if err != nil {
		t.Fatalf("open bank: %v", err)
	}
	recs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if len(recs) == 0 {
		t.Fatalf("committed cassette bank is empty at %s — produce with offline stub record (MOSAIC_WRITE_DEMO_CASSETTES=1) or MOSAIC_RECORD_LIVE=1", bank)
	}
	if len(recs) != 12 {
		t.Fatalf("committed bank size = %d, want 12 (10 luna + terra + sol)", len(recs))
	}

	manifest, err := democast.LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	rawIdx, err := democast.LoadRawEvents(root, manifest.Scenario)
	if err != nil {
		t.Fatalf("raw events: %v", err)
	}
	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)

	// Determinism: run the full verifier twice.
	for pass := 1; pass <= 2; pass++ {
		t.Run(fmt.Sprintf("pass%d", pass), func(t *testing.T) {
			runDemoCastReplayOnce(t, binary, root, uiDirectory, bank, manifest, rawIdx)
		})
	}
}

func runDemoCastReplayOnce(t *testing.T, binary, root, uiDirectory, bank string, manifest democast.Manifest, raw democast.RawEventIndex) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "democast-replay-e2e.db")
	proc := startMosaicDemoProgressiveWithEnv(t, binary, root, databasePath, uiDirectory, map[string]string{
		"MOSAIC_SIM_BEAT_SPACING": "1ms",
		"MOSAIC_SEED_ON_START":    "0",
		"MOSAIC_SIM_MODE":         "replay",
		"MOSAIC_CASSETTE_DIR":     bank,
		// Explicitly clear ambient key so the path cannot hit network.
		"OPENAI_API_KEY": "",
	})
	defer proc.stop(t)

	version := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/version", ""))
	if mode, _ := version["cassette_mode"].(string); mode != "replay" {
		t.Fatalf("cassette_mode = %#v, want replay", version["cassette_mode"])
	}

	driver, err := democast.NewDriver(democast.DriverConfig{
		BaseURL:            proc.baseURL,
		SupervisorIdentity: manifest.SupervisorIdentity,
		ExpectedCOPRev:     manifest.ExpectedCOPRevision,
		PlayTimeout:        45 * time.Second,
	}, raw)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	results, err := driver.RunAll(manifest)
	if err != nil {
		t.Fatalf("run manifest: %v\n%s", err, proc.output.String())
	}
	for _, res := range results {
		if err := democast.AssertOperatorOK(res, true); err != nil {
			t.Fatalf("step %s %s: %v\nbody=%s\nproc:\n%s", res.Kind, res.RawEventID, err, string(res.RawBody), proc.output.String())
		}
		if res.Body != nil {
			if mr, ok := res.Body["model_run"].(map[string]any); ok {
				if detail, _ := mr["failure_detail"].(string); strings.Contains(detail, "no recording for key") {
					t.Fatalf("ErrReplayMiss on %s %s: %s", res.Kind, res.RawEventID, detail)
				}
			}
		}
	}
}

// TestDemoCastRecordLiveE2E is the gated one-shot live re-recorder. It is
// skipped unless MOSAIC_RECORD_LIVE=1 and OPENAI_API_KEY is set. When run it
// clears the committed bank, then writes real model responses into
// testdata/demo/cassettes under the same request-derived keys validated offline.
//
// After a successful live pass: re-run TestDemoCastReplayNoLiveE2E. If any Luna
// terminal status diverged from the manifest expected_status (e.g. weather
// quarantined), update expected_status on those steps before committing the bank.
func TestDemoCastRecordLiveE2E(t *testing.T) {
	if os.Getenv("MOSAIC_RECORD_LIVE") != "1" {
		t.Skip("set MOSAIC_RECORD_LIVE=1 (and OPENAI_API_KEY) to re-record the live demo bank")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Fatal("MOSAIC_RECORD_LIVE=1 requires OPENAI_API_KEY")
	}

	root := advisoryRepositoryRoot(t)
	bank := democast.CassetteDir(root)

	// Wipe the committed bank first so orphan keys from a prior manifest or a
	// half-finished run cannot leave a mixed stub/live tree that still passes
	// a size check.
	if err := os.RemoveAll(bank); err != nil {
		t.Fatalf("clear bank %s: %v", bank, err)
	}
	if err := os.MkdirAll(bank, 0o755); err != nil {
		t.Fatalf("mkdir bank: %v", err)
	}

	manifest, err := democast.LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	rawIdx, err := democast.LoadRawEvents(root, manifest.Scenario)
	if err != nil {
		t.Fatalf("raw events: %v", err)
	}

	binary := buildMosaicDemo(t, root)
	uiDirectory := testDashboard(t)
	databasePath := filepath.Join(t.TempDir(), "democast-live-record.db")

	proc := startMosaicDemoProgressiveWithEnv(t, binary, root, databasePath, uiDirectory, map[string]string{
		"MOSAIC_SIM_BEAT_SPACING": "1ms",
		"MOSAIC_SEED_ON_START":    "0",
		"MOSAIC_SIM_MODE":         "record",
		"MOSAIC_LUNA_PROVIDER":    "live",
		"MOSAIC_TERRA_PROVIDER":   "live",
		"MOSAIC_SOL_PROVIDER":     "live",
		"MOSAIC_CASSETTE_DIR":     bank,
		"OPENAI_API_KEY":          apiKey,
	})
	defer proc.stop(t)

	version := advisoryResponseData(t, getResponse(t, http.MethodGet, proc.baseURL+"/api/v1/version", ""))
	if mode, _ := version["cassette_mode"].(string); mode != "record" {
		t.Fatalf("cassette_mode = %#v, want record (check key + providers)", version["cassette_mode"])
	}

	driver, err := democast.NewDriver(democast.DriverConfig{
		BaseURL:            proc.baseURL,
		SupervisorIdentity: manifest.SupervisorIdentity,
		ExpectedCOPRev:     manifest.ExpectedCOPRevision,
		PlayTimeout:        3 * time.Minute,
	}, rawIdx)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	results, err := driver.RunAll(manifest)
	if err != nil {
		t.Fatalf("live record run: %v\n%s", err, proc.output.String())
	}
	for _, res := range results {
		// Live path: ok|quarantined|refused for Luna; ok for Terra/Sol; ended for Play.
		if err := democast.AssertOperatorOK(res, false); err != nil {
			t.Fatalf("live %s %s: %v\nbody=%s\nproc:\n%s", res.Kind, res.RawEventID, err, string(res.RawBody), proc.output.String())
		}
		// Surface drift vs CI-strict expected so the operator can update the
		// manifest before committing a bank that would fail no-live replay.
		if res.Kind == "luna" && res.ExpectedLuna != "" && res.Status != res.ExpectedLuna {
			t.Logf("WARN: luna %s live status %q != manifest expected %q — update expected_status before committing bank",
				res.RawEventID, res.Status, res.ExpectedLuna)
		}
	}

	store, err := cassette.NewFileStore(bank)
	if err != nil {
		t.Fatalf("open bank: %v", err)
	}
	recs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if len(recs) != 12 {
		keys := make([]string, 0, len(recs))
		for _, r := range recs {
			keys = append(keys, r.Key)
		}
		t.Fatalf("live bank size = %d, want exactly 12 (10 luna + terra + sol); keys=%v", len(recs), keys)
	}
	var luna, terra, sol int
	for _, r := range recs {
		switch {
		case strings.HasPrefix(r.Key, "luna/"):
			luna++
		case strings.HasPrefix(r.Key, "terra/"):
			terra++
		case strings.HasPrefix(r.Key, "sol/"):
			sol++
		default:
			t.Fatalf("unexpected cassette key shape %q", r.Key)
		}
	}
	if luna != 10 || terra != 1 || sol != 1 {
		t.Fatalf("live bank composition luna=%d terra=%d sol=%d, want 10/1/1", luna, terra, sol)
	}
	t.Logf("live re-record wrote %d cassettes under %s", len(recs), bank)
}
