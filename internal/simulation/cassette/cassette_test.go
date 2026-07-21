package cassette_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/simulation/cassette"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

// --- stubs ---

type countingTerra struct {
	calls atomic.Int32
	resp  terra.Response
	err   error
}

func (c *countingTerra) Assess(_ context.Context, _ terra.Request) (terra.Response, error) {
	c.calls.Add(1)
	if c.err != nil {
		return terra.Response{}, c.err
	}
	return terra.Response{
		InsightJSON:   append(json.RawMessage(nil), c.resp.InsightJSON...),
		ResponseID:    c.resp.ResponseID,
		RefusalDetail: c.resp.RefusalDetail,
	}, nil
}

type countingSol struct {
	calls atomic.Int32
	resp  sol.Response
	err   error
}

func (c *countingSol) Brief(_ context.Context, _ sol.Request) (sol.Response, error) {
	c.calls.Add(1)
	if c.err != nil {
		return sol.Response{}, c.err
	}
	return sol.Response{
		RecommendationJSON: append(json.RawMessage(nil), c.resp.RecommendationJSON...),
		ResponseID:         c.resp.ResponseID,
		RefusalDetail:      c.resp.RefusalDetail,
	}, nil
}

func sampleEvidence(id string) gen.Evidence {
	return gen.Evidence{
		SchemaVersion: "1.0.0",
		EvidenceID:    id,
		TargetKind:    "canonical_event",
		TargetID:      "evt-1",
		Explanation:   "fixture evidence",
	}
}

func sampleTerraRequest() terra.Request {
	return terra.Request{
		StateRevision: 7,
		SerializedCOP: json.RawMessage(`{"revision":7,"incidents":[{"id":"inc-1"}]}`),
		Evidence:      []gen.Evidence{sampleEvidence("ev-b"), sampleEvidence("ev-a")},
	}
}

func sampleSolRequest() sol.Request {
	return sol.Request{
		StateRevision: 7,
		SerializedCOP: json.RawMessage(`{"revision":7,"incidents":[{"id":"inc-1"}]}`),
		Insights: []gen.Insight{
			{InsightID: "insight-2", LifecycleStatus: "active"},
			{InsightID: "insight-1", LifecycleStatus: "active"},
		},
		Evidence:    []gen.Evidence{sampleEvidence("ev-a")},
		RequestedBy: "supervisor-demo",
	}
}

// --- key stability ---

func TestTerraKeyStableForSameRequest(t *testing.T) {
	req := sampleTerraRequest()
	meta := cassette.KeyMeta{BeatID: "fixture-07-repaired-incomplete-road"}

	k1, fp1, err := cassette.TerraKey(req, meta)
	if err != nil {
		t.Fatalf("TerraKey: %v", err)
	}
	// Shuffle evidence order — ids are sorted in the fingerprint.
	req.Evidence = []gen.Evidence{sampleEvidence("ev-a"), sampleEvidence("ev-b")}
	k2, fp2, err := cassette.TerraKey(req, meta)
	if err != nil {
		t.Fatalf("TerraKey: %v", err)
	}
	if k1 != k2 || fp1 != fp2 {
		t.Fatalf("key/fp unstable: %q/%q vs %q/%q", k1, fp1, k2, fp2)
	}
	if want := "terra/rev7/fixture-07-repaired-incomplete-road/"; len(k1) <= len(want) || k1[:len(want)] != want {
		t.Fatalf("key = %q, want prefix %q", k1, want)
	}
}

func TestTerraKeyChangesWithRevisionCOPOrBeat(t *testing.T) {
	base := sampleTerraRequest()
	meta := cassette.KeyMeta{}
	kBase, _, err := cassette.TerraKey(base, meta)
	if err != nil {
		t.Fatal(err)
	}

	rev := base
	rev.StateRevision = 9
	kRev, _, _ := cassette.TerraKey(rev, meta)
	if kRev == kBase {
		t.Fatal("revision change should alter key")
	}

	cop := base
	cop.SerializedCOP = json.RawMessage(`{"revision":7,"incidents":[{"id":"inc-2"}]}`)
	kCOP, _, _ := cassette.TerraKey(cop, meta)
	if kCOP == kBase {
		t.Fatal("COP change should alter key")
	}

	kBeat, _, _ := cassette.TerraKey(base, cassette.KeyMeta{BeatID: "beat-x"})
	if kBeat == kBase {
		t.Fatal("beat_id should alter key")
	}
}

func TestSolKeyStableAndIncludesRequestedBy(t *testing.T) {
	req := sampleSolRequest()
	k1, _, err := cassette.SolKey(req, cassette.KeyMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// Insight order should not matter.
	req.Insights = []gen.Insight{
		{InsightID: "insight-1", LifecycleStatus: "active"},
		{InsightID: "insight-2", LifecycleStatus: "active"},
	}
	k2, _, err := cassette.SolKey(req, cassette.KeyMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Fatalf("insight order changed key: %q vs %q", k1, k2)
	}

	req.RequestedBy = "other"
	k3, _, _ := cassette.SolKey(req, cassette.KeyMeta{})
	if k3 == k1 {
		t.Fatal("requested_by should alter key")
	}
	if want := "sol/rev7/"; len(k1) <= len(want) || k1[:len(want)] != want {
		t.Fatalf("key = %q, want prefix %q", k1, want)
	}
}

// --- Terra record / replay ---

func TestTerraRecordThenReplay(t *testing.T) {
	store := cassette.NewMemoryStore()
	inner := &countingTerra{
		resp: terra.Response{
			InsightJSON: json.RawMessage(`{"insight_id":"insight-access-001","state_revision":7}`),
			ResponseID:  "resp-terra-1",
		},
	}
	req := sampleTerraRequest()

	recorder := cassette.NewTerra(cassette.ModeRecord, store, inner)
	recorder.BeatID = "beat-7"
	got, err := recorder.Assess(context.Background(), req)
	if err != nil {
		t.Fatalf("record Assess: %v", err)
	}
	if inner.calls.Load() != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls.Load())
	}
	if string(got.InsightJSON) != string(inner.resp.InsightJSON) || got.ResponseID != "resp-terra-1" {
		t.Fatalf("recorded response = %#v", got)
	}
	if store.Len() != 1 {
		t.Fatalf("store len = %d, want 1", store.Len())
	}

	// Replay with a nil inner — must not call network.
	replayer := cassette.NewTerra(cassette.ModeReplay, store, nil)
	replayer.BeatID = "beat-7"
	replayed, err := replayer.Assess(context.Background(), req)
	if err != nil {
		t.Fatalf("replay Assess: %v", err)
	}
	if inner.calls.Load() != 1 {
		t.Fatalf("inner calls after replay = %d, want 1 (no second call)", inner.calls.Load())
	}
	if string(replayed.InsightJSON) != string(got.InsightJSON) ||
		replayed.ResponseID != got.ResponseID ||
		replayed.RefusalDetail != got.RefusalDetail {
		t.Fatalf("replayed = %#v, want %#v", replayed, got)
	}
}

func TestTerraReplayMiss(t *testing.T) {
	store := cassette.NewMemoryStore()
	client := cassette.NewTerra(cassette.ModeReplay, store, nil)
	_, err := client.Assess(context.Background(), sampleTerraRequest())
	if !errors.Is(err, cassette.ErrReplayMiss) {
		t.Fatalf("error = %v, want ErrReplayMiss", err)
	}
}

func TestTerraPassthroughIgnoresStore(t *testing.T) {
	inner := &countingTerra{
		resp: terra.Response{InsightJSON: json.RawMessage(`{"ok":true}`), ResponseID: "p"},
	}
	client := cassette.NewTerra(cassette.ModePassthrough, nil, inner)
	got, err := client.Assess(context.Background(), sampleTerraRequest())
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls.Load() != 1 || got.ResponseID != "p" {
		t.Fatalf("passthrough failed: calls=%d got=%#v", inner.calls.Load(), got)
	}
}

func TestTerraRecordDoesNotPersistInnerError(t *testing.T) {
	store := cassette.NewMemoryStore()
	inner := &countingTerra{err: errors.New("network down")}
	client := cassette.NewTerra(cassette.ModeRecord, store, inner)
	_, err := client.Assess(context.Background(), sampleTerraRequest())
	if err == nil {
		t.Fatal("expected error")
	}
	if store.Len() != 0 {
		t.Fatalf("store should stay empty on transport failure, len=%d", store.Len())
	}
}

// --- Sol record / replay ---

func TestSolRecordThenReplay(t *testing.T) {
	store := cassette.NewMemoryStore()
	inner := &countingSol{
		resp: sol.Response{
			RecommendationJSON: json.RawMessage(`{"recommendation_id":"rec-1","state_revision":7}`),
			ResponseID:         "resp-sol-1",
		},
	}
	req := sampleSolRequest()

	recorder := cassette.NewSol(cassette.ModeRecord, store, inner)
	got, err := recorder.Brief(context.Background(), req)
	if err != nil {
		t.Fatalf("record Brief: %v", err)
	}
	if inner.calls.Load() != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls.Load())
	}

	replayer := cassette.NewSol(cassette.ModeReplay, store, nil)
	replayed, err := replayer.Brief(context.Background(), req)
	if err != nil {
		t.Fatalf("replay Brief: %v", err)
	}
	if inner.calls.Load() != 1 {
		t.Fatalf("inner calls after replay = %d, want 1", inner.calls.Load())
	}
	if string(replayed.RecommendationJSON) != string(got.RecommendationJSON) ||
		replayed.ResponseID != got.ResponseID {
		t.Fatalf("replayed = %#v, want %#v", replayed, got)
	}
}

func TestSolReplayMiss(t *testing.T) {
	client := cassette.NewSol(cassette.ModeReplay, cassette.NewMemoryStore(), nil)
	_, err := client.Brief(context.Background(), sampleSolRequest())
	if !errors.Is(err, cassette.ErrReplayMiss) {
		t.Fatalf("error = %v, want ErrReplayMiss", err)
	}
}

// --- FileStore ---

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := cassette.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	inner := &countingTerra{
		resp: terra.Response{
			InsightJSON: json.RawMessage(`{"insight_id":"i1"}`),
			ResponseID:  "file-resp",
		},
	}
	req := sampleTerraRequest()
	recorder := cassette.NewTerra(cassette.ModeRecord, store, inner)
	recorder.BeatID = "fixture-07"
	recorder.PromptVersion = "terra/v1.0.0" // H6 hook surface
	recorder.PromptHash = "deadbeef"

	if _, err := recorder.Assess(context.Background(), req); err != nil {
		t.Fatalf("record: %v", err)
	}

	// New store instance reading same dir (simulates process restart).
	store2, err := cassette.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	replayer := cassette.NewTerra(cassette.ModeReplay, store2, nil)
	replayer.BeatID = "fixture-07"
	got, err := replayer.Assess(context.Background(), req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got.ResponseID != "file-resp" || !bytes.Contains(got.InsightJSON, []byte("i1")) {
		t.Fatalf("replayed = %#v", got)
	}

	// Ensure a JSON file exists and carries H6 hooks.
	var found string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ".json" {
			found = path
		}
		return nil
	})
	if found == "" {
		t.Fatal("expected a json recording on disk")
	}
	raw, err := os.ReadFile(found)
	if err != nil {
		t.Fatal(err)
	}
	var rec cassette.Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.PromptVersion != "terra/v1.0.0" || rec.PromptHash != "deadbeef" {
		t.Fatalf("H6 hooks not persisted: %#v", rec)
	}
	if rec.Agent != "terra" || rec.SchemaVersion != cassette.SchemaVersion {
		t.Fatalf("envelope = %#v", rec)
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want cassette.Mode
	}{
		{"passthrough", cassette.ModePassthrough},
		{"off", cassette.ModePassthrough},
		{"fixture", cassette.ModePassthrough},
		{"record", cassette.ModeRecord},
		{"live", cassette.ModeRecord},
		{"replay", cassette.ModeReplay},
		{"recorded", cassette.ModeReplay},
	}
	for _, tc := range cases {
		got, err := cassette.ParseMode(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseMode(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	if _, err := cassette.ParseMode("bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
