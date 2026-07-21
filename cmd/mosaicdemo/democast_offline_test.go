package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/democast"
	"mosaic.local/mosaic/internal/simulation/cassette"
)

// TestDemoCastOfflineRecordReplay proves the full scripted demo manifest with
// stub Luna/Terra/Sol clients: record → bank → replay hits every step, keys
// stable across two record runs, zero ErrReplayMiss, no OpenAI.
func TestDemoCastOfflineRecordReplay(t *testing.T) {
	root := repositoryRoot(t)
	manifest, err := democast.LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	rawIdx, err := democast.LoadRawEvents(root, manifest.Scenario)
	if err != nil {
		t.Fatalf("load raw events: %v", err)
	}

	// Two independent record runs into separate FileStores must produce the
	// same cassette keys (request identity only).
	bankA := filepath.Join(t.TempDir(), "bank-a")
	bankB := filepath.Join(t.TempDir(), "bank-b")
	keysA := recordDemoManifest(t, root, manifest, rawIdx, bankA)
	keysB := recordDemoManifest(t, root, manifest, rawIdx, bankB)
	if len(keysA) == 0 {
		t.Fatal("record produced no cassette keys")
	}
	if len(keysA) != len(keysB) {
		t.Fatalf("key count A=%d B=%d", len(keysA), len(keysB))
	}
	setB := make(map[string]struct{}, len(keysB))
	for _, k := range keysB {
		setB[k] = struct{}{}
	}
	for _, k := range keysA {
		if _, ok := setB[k]; !ok {
			t.Fatalf("key %q present in run A but missing in run B", k)
		}
	}

	// Replay from bank A with a fresh composition — no stubs needed.
	replayDemoManifest(t, root, manifest, rawIdx, bankA, true)

	// Optional: refresh the committed CI bank when explicitly requested.
	// Keys are response-independent; content remains stub-shaped until a live
	// re-record (MOSAIC_RECORD_LIVE=1) overwrites the same paths.
	if os.Getenv("MOSAIC_WRITE_DEMO_CASSETTES") == "1" {
		committed := democast.CassetteDir(root)
		if err := os.RemoveAll(committed); err != nil {
			t.Fatalf("clear committed bank: %v", err)
		}
		_ = recordDemoManifest(t, root, manifest, rawIdx, committed)
		t.Logf("wrote stub cassette bank to %s (%d keys)", committed, len(keysA))
	}
}

// TestDemoCastReplayCommittedBankNoLive is the in-process no-live verifier
// against the committed testdata/demo/cassettes bank (skipped if empty so a
// fresh checkout that has not banked yet can still run offline identity proof).
func TestDemoCastReplayCommittedBankNoLive(t *testing.T) {
	root := repositoryRoot(t)
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
		t.Skip("committed cassette bank is empty; run with MOSAIC_WRITE_DEMO_CASSETTES=1 after offline record")
	}

	manifest, err := democast.LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	rawIdx, err := democast.LoadRawEvents(root, manifest.Scenario)
	if err != nil {
		t.Fatalf("load raw events: %v", err)
	}

	// Run twice for determinism.
	replayDemoManifest(t, root, manifest, rawIdx, bank, true)
	replayDemoManifest(t, root, manifest, rawIdx, bank, true)
}

// TestDemoInteractionsReplayBankHits proves the UI path: GET
// /api/v1/demo/interactions then POST each served request body under replay
// mode against the committed bank (no OPENAI_API_KEY). Covers one Luna (ok),
// one Luna (quarantine beat 8), Terra, and Sol after Play reaches COP rev 9.
func TestDemoInteractionsReplayBankHits(t *testing.T) {
	root := repositoryRoot(t)
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
		t.Skip("committed cassette bank empty; skip interactions→POST bank-hit proof")
	}

	ui := makeDashboard(t)
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "interactions-replay.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
		ModelEnv: modelEnv{
			APIKey:          "",
			Luna:            contracts.ProviderLive,
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
			CassetteModeRaw: "replay",
			CassetteDir:     bank,
		},
	})
	if err != nil {
		t.Fatalf("compose replay app: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	// GET interactions document (same endpoint the UI uses).
	getReq, err := http.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/demo/interactions", nil)
	if err != nil {
		t.Fatal(err)
	}
	getRec := httptest.NewRecorder()
	app.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET interactions status = %d: %s", getRec.Code, getRec.Body.String())
	}
	var getEnvelope struct {
		Data struct {
			Scenario            string `json:"scenario"`
			ExpectedCOPRevision int64  `json:"expected_cop_revision"`
			SupervisorIdentity  string `json:"supervisor_identity"`
			CassetteMode        string `json:"cassette_mode"`
			Steps               []struct {
				Kind           string         `json:"kind"`
				BeatID         string         `json:"beat_id"`
				RawEventRef    string         `json:"raw_event_ref"`
				ExpectedStatus string         `json:"expected_status"`
				Endpoint       string         `json:"endpoint"`
				Request        map[string]any `json:"request"`
			} `json:"steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getEnvelope); err != nil {
		t.Fatalf("decode interactions: %v", err)
	}
	doc := getEnvelope.Data
	if doc.CassetteMode != "replay" {
		t.Fatalf("cassette_mode = %q, want replay", doc.CassetteMode)
	}
	if doc.ExpectedCOPRevision != 9 {
		t.Fatalf("expected_cop_revision = %d", doc.ExpectedCOPRevision)
	}
	if len(doc.Steps) == 0 {
		t.Fatal("no interaction steps")
	}

	// Play to COP rev 9 (required for Terra/Sol bank keys).
	rawIdx, err := democast.LoadRawEvents(root, doc.Scenario)
	if err != nil {
		t.Fatalf("raw events: %v", err)
	}
	driver, err := democast.NewDriver(democast.DriverConfig{
		BaseURL:            "http://mosaic.test",
		SupervisorIdentity: doc.SupervisorIdentity,
		ExpectedCOPRev:     doc.ExpectedCOPRevision,
		Client:             democast.HandlerClient(app.handler),
		PlayTimeout:        45 * time.Second,
	}, rawIdx)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	playRes, err := driver.RunStep(democast.Step{Kind: "play"})
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if playRes.Status != "ended" {
		t.Fatalf("play status = %q", playRes.Status)
	}

	// Select one accepted Luna, quarantine Luna (beat 8), Terra, Sol from served steps.
	var lunaOK, lunaQuarantine, terra, sol map[string]any
	for _, step := range doc.Steps {
		switch step.Kind {
		case "luna":
			if step.RawEventRef == "raw-domestic-008-invalid-input" {
				lunaQuarantine = step.Request
			} else if lunaOK == nil && (step.ExpectedStatus == "" || step.ExpectedStatus == "ok") {
				lunaOK = step.Request
			}
		case "terra":
			terra = step.Request
		case "sol":
			sol = step.Request
		}
	}
	if lunaOK == nil || lunaQuarantine == nil || terra == nil || sol == nil {
		t.Fatalf("missing steps: lunaOK=%v lunaQ=%v terra=%v sol=%v", lunaOK != nil, lunaQuarantine != nil, terra != nil, sol != nil)
	}

	postJSON := func(path string, body map[string]any, identity string) map[string]any {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, "http://mosaic.test/api/v1/"+path, strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if identity != "" {
			req.Header.Set("X-Mosaic-Demo-Identity", identity)
		}
		rec := httptest.NewRecorder()
		app.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %s: %v body=%s", path, err, rec.Body.String())
		}
		return env.Data
	}

	assertBankHit := func(label string, data map[string]any, wantStatus string) {
		t.Helper()
		status, _ := data["status"].(string)
		if status != wantStatus {
			t.Fatalf("%s status = %q, want %q body=%v", label, status, wantStatus, data)
		}
		if data["executed"] == true {
			t.Fatalf("%s must not execute (board boundary)", label)
		}
		if mr, ok := data["model_run"].(map[string]any); ok {
			if p, _ := mr["provider"].(string); p != "" && p != "mosaic-fixture" {
				t.Fatalf("%s provider = %q, want mosaic-fixture", label, p)
			}
			if detail, _ := mr["failure_detail"].(string); strings.Contains(detail, "no recording for key") {
				t.Fatalf("%s ErrReplayMiss: %s", label, detail)
			}
		}
	}

	assertBankHit("luna ok", postJSON("operator/interpret", lunaOK, ""), "ok")
	assertBankHit("luna quarantine", postJSON("operator/interpret", lunaQuarantine, ""), "quarantined")
	assertBankHit("terra", postJSON("operator/analyze", terra, ""), "ok")
	assertBankHit("sol", postJSON("operator/brief", sol, doc.SupervisorIdentity), "ok")
}

func recordDemoManifest(t *testing.T, root string, manifest democast.Manifest, raw democast.RawEventIndex, bankDir string) []string {
	t.Helper()
	if err := os.MkdirAll(bankDir, 0o755); err != nil {
		t.Fatalf("mkdir bank: %v", err)
	}
	ui := makeDashboard(t)
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "democast-record.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
		ModelEnv: modelEnv{
			APIKey:          "test-offline-key",
			Luna:            contracts.ProviderLive,
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
			CassetteModeRaw: "record",
			CassetteDir:     bankDir,
			testLiveLuna:    democast.StubLuna{},
			testLiveTerra:   democast.EchoTerra{},
			testLiveSol:     democast.EchoSol{},
		},
	})
	if err != nil {
		t.Fatalf("compose record app: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	driver, err := democast.NewDriver(democast.DriverConfig{
		BaseURL:            "http://mosaic.test",
		SupervisorIdentity: manifest.SupervisorIdentity,
		ExpectedCOPRev:     manifest.ExpectedCOPRevision,
		Client:             democast.HandlerClient(app.handler),
		PlayTimeout:        45 * time.Second,
	}, raw)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	results, err := driver.RunAll(manifest)
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	for _, res := range results {
		if err := democast.AssertOperatorOK(res, false); err != nil {
			t.Fatalf("record step %s %s: %v\nbody=%s", res.Kind, res.RawEventID, err, string(res.RawBody))
		}
	}

	fs, err := cassette.NewFileStore(bankDir)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	recs, err := fs.List(context.Background())
	if err != nil {
		t.Fatalf("list recordings: %v", err)
	}
	// Expect 10 luna + 1 terra + 1 sol = 12.
	if len(recs) != 12 {
		keys := make([]string, 0, len(recs))
		for _, r := range recs {
			keys = append(keys, r.Key)
		}
		t.Fatalf("banked recordings = %d, want 12; keys=%v", len(recs), keys)
	}
	keys := make([]string, 0, len(recs))
	for _, r := range recs {
		keys = append(keys, r.Key)
		if !strings.HasPrefix(r.Key, "luna/") && !strings.HasPrefix(r.Key, "terra/") && !strings.HasPrefix(r.Key, "sol/") {
			t.Fatalf("unexpected cassette key shape %q", r.Key)
		}
	}
	return keys
}

func replayDemoManifest(t *testing.T, root string, manifest democast.Manifest, raw democast.RawEventIndex, bankDir string, requireFixture bool) {
	t.Helper()
	ui := makeDashboard(t)
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "democast-replay.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
		ModelEnv: modelEnv{
			// No API key — replay must never need network.
			APIKey:          "",
			Luna:            contracts.ProviderLive,
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
			CassetteModeRaw: "replay",
			CassetteDir:     bankDir,
		},
	})
	if err != nil {
		t.Fatalf("compose replay app: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	// Surface cassette_mode=replay on version.
	req := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/version", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("version status = %d: %s", rec.Code, rec.Body.String())
	}
	var version struct {
		Data struct {
			CassetteMode string `json:"cassette_mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &version); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if version.Data.CassetteMode != "replay" {
		t.Fatalf("cassette_mode = %q, want replay", version.Data.CassetteMode)
	}

	driver, err := democast.NewDriver(democast.DriverConfig{
		BaseURL:            "http://mosaic.test",
		SupervisorIdentity: manifest.SupervisorIdentity,
		ExpectedCOPRev:     manifest.ExpectedCOPRevision,
		Client:             democast.HandlerClient(app.handler),
		PlayTimeout:        45 * time.Second,
	}, raw)
	if err != nil {
		t.Fatalf("replay driver: %v", err)
	}
	results, err := driver.RunAll(manifest)
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	for _, res := range results {
		if err := democast.AssertOperatorOK(res, requireFixture); err != nil {
			t.Fatalf("replay step %s %s: %v\nbody=%s", res.Kind, res.RawEventID, err, string(res.RawBody))
		}
		// Replay misses surface as failed/error with ErrReplayMiss detail.
		if res.Body != nil {
			if mr, ok := res.Body["model_run"].(map[string]any); ok {
				if detail, _ := mr["failure_detail"].(string); strings.Contains(detail, "no recording for key") {
					t.Fatalf("ErrReplayMiss on %s %s: %s", res.Kind, res.RawEventID, detail)
				}
			}
		}
	}
}
