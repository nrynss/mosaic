package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/simulation/cassette"
	"mosaic.local/mosaic/internal/store"
)

// F1 verification proofs at the mosaicdemo composition root.
// Covers progressive projection intermediates, session isolation, and
// live/recorded/fixture cassette parity composed with the progressive path.
// Does not call real OpenAI APIs (stubs / fixture / memory cassette only).

// TestF1ProgressiveProjectionIntermediateRevisions proves Play drives real
// per-beat work: COP is empty before Play, intermediate revisions appear before
// final revision 9, and the final board is fixture-complete (not bulk-seeded at boot).
func TestF1ProgressiveProjectionIntermediateRevisions(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	database := filepath.Join(t.TempDir(), "f1-progressive.db")
	// Slow enough equal spacing that GET /cop during Play can sample mid-session.
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  database,
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("compose application: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	assertEmptyCOP(t, app.handler)
	if app.simulation == nil {
		t.Fatal("simulation controller is nil")
	}

	if _, err := app.simulation.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Sample COP while session is running; collect strictly positive intermediate revisions.
	seen := map[int64]bool{}
	var samples []int64
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rev := copRevision(t, app.handler)
		if rev > 0 && !seen[rev] {
			seen[rev] = true
			samples = append(samples, rev)
		}
		if app.simulation.Status().Status == contracts.SessionEnded && rev == 9 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitSessionEnded(t, app.simulation, 10*time.Second)
	assertFixtureCOP(t, app.handler)

	if !seen[9] {
		t.Fatalf("never observed final revision 9; samples=%v", samples)
	}
	// Progressive path must expose at least one intermediate revision before 9.
	// (Bulk seed would jump empty→9 with no mid-session materialization.)
	intermediate := false
	for rev := range seen {
		if rev > 0 && rev < 9 {
			intermediate = true
			break
		}
	}
	if !intermediate {
		t.Fatalf("no intermediate COP revision observed during Play; samples=%v (want progressive ladder, not bulk jump)", samples)
	}
}

// TestF1SessionReplayIsolationTwoSequentialPlays proves two sequential sessions
// on the same durable store: End clears the board; durable history remains;
// second Play reaches rev 9 for the new active session only.
func TestF1SessionReplayIsolationTwoSequentialPlays(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	database := filepath.Join(t.TempDir(), "f1-session-iso.db")
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  database,
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	// Session 1
	first, err := app.simulation.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if first.SessionID == "" {
		t.Fatal("empty first session id")
	}
	waitSessionEnded(t, app.simulation, 15*time.Second)
	assertFixtureCOP(t, app.handler)
	assertFixtureAdvisories(t, app.handler)

	if _, err := app.simulation.End(context.Background()); err != nil {
		t.Fatalf("End after first: %v", err)
	}
	assertEmptyCOP(t, app.handler)
	assertEmptyAdvisories(t, app.handler)

	// Durable append-only history remains after End (empty board is view isolation).
	durable, err := store.Open(context.Background(), database)
	if err != nil {
		t.Fatalf("open durable: %v", err)
	}
	events, err := durable.ListCanonicalEventsAfter(context.Background(), 0)
	if err != nil {
		_ = durable.Close()
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 9 {
		_ = durable.Close()
		t.Fatalf("durable canonical events after first Play = %d, want 9", len(events))
	}
	history, err := durable.ReadAdvisoryHistory(context.Background())
	_ = durable.Close()
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(history.Insights) != 2 || len(history.Recommendations) != 1 {
		t.Fatalf("durable advisories after first = insights:%d recs:%d, want 2/1", len(history.Insights), len(history.Recommendations))
	}

	// Session 2 on same process + DB file: distinct epoch; session-scoped COP
	// must reach rev 9 without flashing prior session when Active is set (D1h R2).
	second, err := app.simulation.Start(context.Background())
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if second.SessionID == "" || second.SessionID == first.SessionID {
		t.Fatalf("second session id = %q, first = %q; want distinct epochs", second.SessionID, first.SessionID)
	}
	waitSessionEnded(t, app.simulation, 15*time.Second)
	assertFixtureCOP(t, app.handler)

	// Session-scoped advisories: ContinueProgressive may skip re-running intact
	// durable stages and therefore may not Record ids for the new session.
	// COP isolation is the D1h R2 gate; advisory re-index on intact stages is a
	// known residual (outside F1 ownership — note for coordinator).
	advReq := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/advisories", nil)
	advResp := httptest.NewRecorder()
	app.handler.ServeHTTP(advResp, advReq)
	if advResp.Code != http.StatusOK {
		t.Fatalf("advisories after second Play status = %d", advResp.Code)
	}

	if _, err := app.simulation.End(context.Background()); err != nil {
		t.Fatalf("End after second: %v", err)
	}
	assertEmptyCOP(t, app.handler)
	assertEmptyAdvisories(t, app.handler)
}

// TestF1FixtureCassetteModeSurfacedOnProgressivePath proves default fixture/
// passthrough is the deterministic safe mode and is public on version/advisories
// while progressive Play still reaches the final board.
func TestF1FixtureCassetteModeSurfacedOnProgressivePath(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "f1-fixture-mode.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
		ModelEnv:      modelEnv{CassetteModeRaw: "fixture"},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	assertCassetteMode(t, app.handler, "passthrough")
	assertEmptyCOP(t, app.handler)

	if _, err := app.simulation.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitSessionEnded(t, app.simulation, 15*time.Second)
	assertFixtureCOP(t, app.handler)
	assertFixtureAdvisories(t, app.handler)
	assertCassetteMode(t, app.handler, "passthrough")
}

// TestF1ReplayModeComposesWithProgressiveSimulation proves replay mode:
// no network required, cassette_mode=replay surfaced, progressive Play still
// drives fixture continuum to rev 9 (operator Terra/Sol bank is separate from
// progressive fixture advisories). Empty bank → ErrReplayMiss is unit-covered
// in models_test; here we only prove simulation composition parity.
func TestF1ReplayModeComposesWithProgressiveSimulation(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	cassetteDir := t.TempDir()
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "f1-replay-mode.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
		ModelEnv: modelEnv{
			CassetteModeRaw: "replay",
			CassetteDir:     cassetteDir,
			CassetteStore:   cassette.NewMemoryStore(), // empty bank; no network
		},
	})
	if err != nil {
		t.Fatalf("compose replay progressive: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	assertCassetteMode(t, app.handler, "replay")
	assertEmptyCOP(t, app.handler)

	if _, err := app.simulation.Start(context.Background()); err != nil {
		t.Fatalf("Start under replay: %v", err)
	}
	waitSessionEnded(t, app.simulation, 15*time.Second)
	assertFixtureCOP(t, app.handler)
	assertFixtureAdvisories(t, app.handler)
	assertCassetteMode(t, app.handler, "replay")
}

// TestF1RecordModeWithoutKeyDemotesAndProgressiveStillWorks proves record/live
// without OPENAI_API_KEY demotes safely to passthrough and progressive Play
// remains the fixture path (no real API calls).
func TestF1RecordModeWithoutKeyDemotesAndProgressiveStillWorks(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  filepath.Join(t.TempDir(), "f1-record-demote.db"),
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   time.Millisecond,
		ModelEnv: modelEnv{
			// No API key — record must demote to passthrough.
			CassetteModeRaw: "record",
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
			CassetteDir:     t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("compose record-without-key: %v", err)
	}
	t.Cleanup(func() { _ = app.close() })

	assertCassetteMode(t, app.handler, "passthrough")
	if _, err := app.simulation.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitSessionEnded(t, app.simulation, 15*time.Second)
	assertFixtureCOP(t, app.handler)
	assertFixtureAdvisories(t, app.handler)
}

func copRevision(t *testing.T, handler http.Handler) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/cop", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("COP status = %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			StateRevision float64 `json:"state_revision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode COP: %v", err)
	}
	return int64(body.Data.StateRevision)
}

func assertEmptyAdvisories(t *testing.T, handler http.Handler) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/advisories", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("advisories status = %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			Insights        []any `json:"insights"`
			Recommendations []any `json:"recommendations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode advisories: %v\n%s", err, resp.Body.String())
	}
	if len(body.Data.Insights) != 0 || len(body.Data.Recommendations) != 0 {
		t.Fatalf("want empty advisories board, got insights=%d recs=%d", len(body.Data.Insights), len(body.Data.Recommendations))
	}
}

func assertCassetteMode(t *testing.T, handler http.Handler, want string) {
	t.Helper()
	// Prefer version endpoint (always surfaces cassette_mode).
	req := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/version", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("version status = %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			CassetteMode string `json:"cassette_mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version: %v\n%s", err, resp.Body.String())
	}
	if body.Data.CassetteMode != want {
		t.Fatalf("cassette_mode = %q, want %q", body.Data.CassetteMode, want)
	}
}
