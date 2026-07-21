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
	// Progressive path must expose multiple intermediate revisions before 9.
	// (Bulk seed would jump empty→9 with no mid-session materialization.)
	intermediateCount := 0
	for rev := range seen {
		if rev > 0 && rev < 9 {
			intermediateCount++
		}
	}
	if intermediateCount < 2 {
		t.Fatalf("intermediate COP revisions = %d (samples=%v); want ≥2 distinct values in (0,9) for progressive ladder", intermediateCount, samples)
	}
}

// TestF1SessionReplayIsolationTwoSequentialPlays proves two sequential sessions
// on the same durable store and process: End clears the board; durable history
// remains; second Play advances COP progressively (not full-Recover jump) and
// reaches rev 9 for the new active session only; IntactRestart advisories are
// gated by progressive revision.
func TestF1SessionReplayIsolationTwoSequentialPlays(t *testing.T) {
	root := repositoryRoot(t)
	ui := makeDashboard(t)
	database := filepath.Join(t.TempDir(), "f1-session-iso.db")
	// Spacing slow enough that second-Play mid-session COP samples can land.
	app, err := newApplication(context.Background(), config{
		ListenAddress: "127.0.0.1:0",
		DatabasePath:  database,
		UIDirectory:   ui,
		AssetRoot:     root,
		BeatSpacing:   40 * time.Millisecond,
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
	waitSessionEnded(t, app.simulation, 20*time.Second)
	assertFixtureCOP(t, app.handler)
	assertFixtureAdvisories(t, app.handler)

	if _, err := app.simulation.End(context.Background()); err != nil {
		t.Fatalf("End after first: %v", err)
	}
	assertEmptyCOP(t, app.handler)
	assertEmptyAdvisories(t, app.handler)

	// Durable append-only history remains after End (empty board is view isolation).
	// Must not hold the app's DB open concurrently (SQLite single-writer); use
	// the composed store via recovery only when available — reopen after End
	// is fine for file-backed SQLite when app still has the connection...
	// Prefer listing through a short-lived open only if path allows; otherwise
	// skip dual-open and trust first-play fixture asserts + second-play ladder.
	// File-backed modernc sqlite often allows multi-read; keep the check.
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

	// Session 2 same process + DB: distinct epoch; progressive COP must climb
	// through intermediate revisions (P05 duplicates must not full-Recover).
	second, err := app.simulation.Start(context.Background())
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if second.SessionID == "" || second.SessionID == first.SessionID {
		t.Fatalf("second session id = %q, first = %q; want distinct epochs", second.SessionID, first.SessionID)
	}

	seen2 := map[int64]bool{}
	var samples2 []int64
	midSessionInsights := -1
	deadline2 := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline2) {
		rev := copRevision(t, app.handler)
		if rev > 0 && !seen2[rev] {
			seen2[rev] = true
			samples2 = append(samples2, rev)
		}
		// Before progressive rev 7, IntactRestart must not surface fixture insights.
		if rev > 0 && rev < 7 && midSessionInsights < 0 {
			midSessionInsights = advisoryInsightCount(t, app.handler)
		}
		if app.simulation.Status().Status == contracts.SessionEnded && rev == 9 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitSessionEnded(t, app.simulation, 15*time.Second)
	assertFixtureCOP(t, app.handler)

	if !seen2[9] {
		t.Fatalf("second Play never observed final revision 9; samples=%v", samples2)
	}
	intermediate2 := 0
	for rev := range seen2 {
		if rev > 0 && rev < 9 {
			intermediate2++
		}
	}
	if intermediate2 < 2 {
		t.Fatalf("second Play intermediate COP revisions = %d (samples=%v); want ≥2 in (0,9) (full-Recover smell)", intermediate2, samples2)
	}
	if midSessionInsights > 0 {
		t.Fatalf("mid-session (rev<7) insights = %d, want 0 (IntactRestart must not re-index early)", midSessionInsights)
	}

	// After complete second Play, fixture advisories are re-indexed for the epoch.
	assertFixtureAdvisories(t, app.handler)

	if _, err := app.simulation.End(context.Background()); err != nil {
		t.Fatalf("End after second: %v", err)
	}
	assertEmptyCOP(t, app.handler)
	assertEmptyAdvisories(t, app.handler)
}

func advisoryInsightCount(t *testing.T, handler http.Handler) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://mosaic.test/api/v1/advisories", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("advisories status = %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			Insights []any `json:"insights"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode advisories: %v", err)
	}
	return len(body.Data.Insights)
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

// TestF1ReplayModeComposesWithProgressiveSimulation proves cassette mode
// composition + progressive fixture continuum under replay: no network,
// cassette_mode=replay surfaced, COP/advisories still reach fixture end state.
// Progressive advisories use the fixture continuum, not banked Terra/Sol
// cassette responses. Empty-bank ErrReplayMiss is unit-covered in models_test.
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

// TestF1RecordModeWithoutKeyDemotesAndProgressiveStillWorks proves record mode
// without a key demotes to passthrough (no banked live path) and progressive
// Play still uses the deterministic fixture continuum (no real API calls).
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
