package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/simulation/cassette"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/terra"
)

func TestLoadVersionedPromptUsesArtifactContentAndHash(t *testing.T) {
	root := repositoryRoot(t)
	prompt, err := loadVersionedPrompt(root, openaimodel.AgentTerra, "v1.0.0")
	if err != nil {
		t.Fatalf("load prompt: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "prompts", "terra", "v1.0.0.md"))
	if err != nil {
		t.Fatalf("read prompt fixture: %v", err)
	}
	if prompt.Instructions != strings.TrimSpace(string(contents)) {
		t.Fatal("loaded instructions do not match the versioned prompt artifact")
	}
	sum := sha256.Sum256(contents)
	want := "v1.0.0+sha256:" + hex.EncodeToString(sum[:])
	if prompt.Provenance != want {
		t.Fatalf("prompt provenance = %q, want %q", prompt.Provenance, want)
	}
}

func TestLoadVersionedLunaPromptUsesArtifactContentAndHash(t *testing.T) {
	root := repositoryRoot(t)
	prompt, err := loadVersionedPrompt(root, openaimodel.AgentLuna, "v1.0.0")
	if err != nil {
		t.Fatalf("load Luna prompt: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "prompts", "luna", "v1.0.0.md"))
	if err != nil {
		t.Fatalf("read Luna prompt fixture: %v", err)
	}
	if prompt.Instructions != strings.TrimSpace(string(contents)) {
		t.Fatal("loaded Luna instructions do not match the versioned prompt artifact")
	}
	sum := sha256.Sum256(contents)
	want := "v1.0.0+sha256:" + hex.EncodeToString(sum[:])
	if prompt.Provenance != want {
		t.Fatalf("Luna prompt provenance = %q, want %q", prompt.Provenance, want)
	}
}

func TestLoadVersionedPromptRejectsMissingAndEmptyArtifacts(t *testing.T) {
	root := t.TempDir()
	if _, err := loadVersionedPrompt(root, openaimodel.AgentTerra, "v1.0.0"); err == nil {
		t.Fatal("missing prompt did not fail")
	}
	path := filepath.Join(root, "prompts", "terra")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("make prompt directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "v1.0.0.md"), []byte(" \n\t "), 0o600); err != nil {
		t.Fatalf("write empty prompt: %v", err)
	}
	if _, err := loadVersionedPrompt(root, openaimodel.AgentTerra, "v1.0.0"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty prompt error = %v", err)
	}
}
func TestEffectiveSelectionRequiresKeyForLive(t *testing.T) {
	requested := contracts.AgentProviderSelection{
		openaimodel.AgentLuna:  contracts.ProviderLive,
		openaimodel.AgentTerra: contracts.ProviderLive,
		openaimodel.AgentSol:   contracts.ProviderFixture,
	}
	withoutKey := effectiveSelection(requested, "")
	if withoutKey[openaimodel.AgentLuna] != contracts.ProviderFixture {
		t.Fatalf("luna without key = %s, want fixture", withoutKey[openaimodel.AgentLuna])
	}
	if withoutKey[openaimodel.AgentTerra] != contracts.ProviderFixture {
		t.Fatalf("terra without key = %s, want fixture", withoutKey[openaimodel.AgentTerra])
	}
	if withoutKey[openaimodel.AgentSol] != contracts.ProviderFixture {
		t.Fatalf("sol without key = %s, want fixture", withoutKey[openaimodel.AgentSol])
	}

	withKey := effectiveSelection(requested, "test-key")
	if withKey[openaimodel.AgentLuna] != contracts.ProviderLive {
		t.Fatalf("luna with key = %s, want live", withKey[openaimodel.AgentLuna])
	}
	if withKey[openaimodel.AgentTerra] != contracts.ProviderLive {
		t.Fatalf("terra with key = %s, want live", withKey[openaimodel.AgentTerra])
	}
	if withKey[openaimodel.AgentSol] != contracts.ProviderFixture {
		t.Fatalf("sol stays fixture = %s", withKey[openaimodel.AgentSol])
	}
}

func TestParseModelEnvDefaultsToFixture(t *testing.T) {
	env := parseModelEnv(func(string) string { return "" })
	if env.APIKey != "" {
		t.Fatalf("expected empty API key, got %q", env.APIKey)
	}
	if env.Luna != contracts.ProviderFixture || env.Terra != contracts.ProviderFixture || env.Sol != contracts.ProviderFixture {
		t.Fatalf("default providers = %#v", env)
	}
	if env.CassetteModeRaw != "" {
		t.Fatalf("default cassette mode raw = %q, want empty", env.CassetteModeRaw)
	}
	mode, err := parseCassetteMode(env)
	if err != nil {
		t.Fatalf("parse default cassette mode: %v", err)
	}
	if mode != cassette.ModePassthrough {
		t.Fatalf("default cassette mode = %s, want passthrough", mode)
	}
	wantDir := filepath.Join(os.TempDir(), defaultCassetteDirName)
	if resolveCassetteDir(env) != wantDir {
		t.Fatalf("default cassette dir = %q, want %q", resolveCassetteDir(env), wantDir)
	}
}

func TestParseModelEnvReadsServerOnlyKey(t *testing.T) {
	env := parseModelEnv(func(name string) string {
		switch name {
		case "OPENAI_API_KEY":
			return "  test-key  "
		case "MOSAIC_TERRA_PROVIDER":
			return "live"
		case "MOSAIC_LUNA_PROVIDER":
			return "LIVE"
		case "MOSAIC_SOL_PROVIDER":
			return "fixture"
		default:
			return ""
		}
	})
	if env.APIKey != "test-key" {
		t.Fatalf("API key = %q", env.APIKey)
	}
	if env.Terra != contracts.ProviderLive || env.Luna != contracts.ProviderLive || env.Sol != contracts.ProviderFixture {
		t.Fatalf("parsed providers = %#v", env)
	}
}

func TestParseModelEnvCassetteModeAndDir(t *testing.T) {
	t.Run("sim mode wins over cassette mode alias", func(t *testing.T) {
		env := parseModelEnv(func(name string) string {
			switch name {
			case "MOSAIC_SIM_MODE":
				return "REPLAY"
			case "MOSAIC_CASSETTE_MODE":
				return "live"
			case "MOSAIC_CASSETTE_DIR":
				return "  /tmp/bank  "
			default:
				return ""
			}
		})
		if env.CassetteModeRaw != "replay" {
			t.Fatalf("CassetteModeRaw = %q, want replay", env.CassetteModeRaw)
		}
		mode, err := parseCassetteMode(env)
		if err != nil {
			t.Fatalf("parse mode: %v", err)
		}
		if mode != cassette.ModeReplay {
			t.Fatalf("mode = %s, want replay", mode)
		}
		if resolveCassetteDir(env) != "/tmp/bank" {
			t.Fatalf("dir = %q", resolveCassetteDir(env))
		}
	})

	t.Run("cassette mode alias when sim unset", func(t *testing.T) {
		env := parseModelEnv(func(name string) string {
			if name == "MOSAIC_CASSETTE_MODE" {
				return "record"
			}
			return ""
		})
		mode, err := parseCassetteMode(env)
		if err != nil {
			t.Fatalf("parse mode: %v", err)
		}
		if mode != cassette.ModeRecord {
			t.Fatalf("mode = %s, want record", mode)
		}
	})

	t.Run("aliases map via ParseMode", func(t *testing.T) {
		cases := map[string]cassette.Mode{
			"fixture":     cassette.ModePassthrough,
			"passthrough": cassette.ModePassthrough,
			"off":         cassette.ModePassthrough,
			"live":        cassette.ModeRecord,
			"record":      cassette.ModeRecord,
			"replay":      cassette.ModeReplay,
			"recorded":    cassette.ModeReplay,
		}
		for raw, want := range cases {
			mode, err := parseCassetteMode(modelEnv{CassetteModeRaw: raw})
			if err != nil {
				t.Fatalf("%q: %v", raw, err)
			}
			if mode != want {
				t.Fatalf("%q → %s, want %s", raw, mode, want)
			}
		}
	})

	t.Run("unknown mode errors", func(t *testing.T) {
		_, err := parseCassetteMode(modelEnv{CassetteModeRaw: "banana"})
		if err == nil {
			t.Fatal("expected error for unknown mode")
		}
	})
}

func TestSplitPromptProvenance(t *testing.T) {
	v, h := splitPromptProvenance("v1.0.0+sha256:deadbeef")
	if v != "v1.0.0" || h != "deadbeef" {
		t.Fatalf("split = %q, %q", v, h)
	}
	v, h = splitPromptProvenance("plain")
	if v != "plain" || h != "" {
		t.Fatalf("plain split = %q, %q", v, h)
	}
}

func TestComposeModelsFixturePath(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{}, // all fixture, no key
	)
	if err != nil {
		t.Fatalf("compose models: %v", err)
	}
	if bundle.Terra == nil || bundle.Sol == nil || bundle.Luna == nil {
		t.Fatalf("expected all adapters: %#v", bundle)
	}
	for _, agent := range []string{openaimodel.AgentLuna, openaimodel.AgentTerra, openaimodel.AgentSol} {
		if bundle.ProviderSelection[agent] != contracts.ProviderFixture {
			t.Fatalf("provider %s = %s, want fixture", agent, bundle.ProviderSelection[agent])
		}
	}
	if bundle.CassetteMode != cassette.ModePassthrough.String() {
		t.Fatalf("CassetteMode = %q, want passthrough", bundle.CassetteMode)
	}
}

func TestComposeModelsFixtureModeDoesNotRequireKey(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-fixture-mode.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{CassetteModeRaw: "fixture"},
	)
	if err != nil {
		t.Fatalf("compose fixture mode: %v", err)
	}
	if bundle.CassetteMode != "passthrough" {
		t.Fatalf("CassetteMode = %q, want passthrough", bundle.CassetteMode)
	}
}

func TestComposeModelsRecordWithoutKeyFallsBackToPassthrough(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-record-nokey.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// live mode requested but no key → providers fixture → demote record to passthrough
	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{
			CassetteModeRaw: "live",
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
		},
	)
	if err != nil {
		t.Fatalf("compose record without key: %v", err)
	}
	if bundle.CassetteMode != "passthrough" {
		t.Fatalf("CassetteMode = %q, want passthrough demotion", bundle.CassetteMode)
	}
	if bundle.ProviderSelection[openaimodel.AgentTerra] != contracts.ProviderFixture {
		t.Fatalf("terra provider = %s, want fixture", bundle.ProviderSelection[openaimodel.AgentTerra])
	}
}

func TestComposeModelsRecordModeWrapsLivePath(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-record.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	mem := cassette.NewMemoryStore()
	terraStub := &countingTerraClient{resp: terra.Response{
		InsightJSON: json.RawMessage(`{"schema_version":"1.0.0","insight_id":"ins-record"}`),
		ResponseID:  "resp-record-terra",
	}}
	solStub := &countingSolClient{resp: sol.Response{
		RecommendationJSON: json.RawMessage(`{"schema_version":"1.0.0","recommendation_id":"rec-record"}`),
		ResponseID:         "resp-record-sol",
	}}

	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{
			APIKey:          "test-key",
			CassetteModeRaw: "record",
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
			CassetteStore:   mem,
			CassetteDir:     t.TempDir(),
			testLiveTerra:   terraStub,
			testLiveSol:     solStub,
		},
	)
	if err != nil {
		t.Fatalf("compose record mode: %v", err)
	}
	if bundle.CassetteMode != "record" {
		t.Fatalf("CassetteMode = %q, want record", bundle.CassetteMode)
	}
	if bundle.ProviderSelection[openaimodel.AgentTerra] != contracts.ProviderLive {
		t.Fatalf("terra provider = %s, want live", bundle.ProviderSelection[openaimodel.AgentTerra])
	}

	// Drive the decorated StructuredClients via applyCassette unit path is
	// covered separately; here verify wrap helper banks into MemoryStore.
	terraInner := terraStub
	solInner := solStub
	construct := contracts.AgentProviderSelection{
		openaimodel.AgentTerra: contracts.ProviderLive,
		openaimodel.AgentSol:   contracts.ProviderLive,
	}
	terraClient, solClient, mode, _, err := applyCassette(
		cassette.ModeRecord,
		modelEnv{CassetteStore: mem, CassetteDir: t.TempDir()},
		terraInner, solInner, construct,
		versionedPrompt{Provenance: "v1.0.0+sha256:abc"},
		versionedPrompt{Provenance: "v1.0.0+sha256:def"},
	)
	if err != nil {
		t.Fatalf("applyCassette: %v", err)
	}
	if mode != cassette.ModeRecord {
		t.Fatalf("mode = %s", mode)
	}
	req := terra.Request{
		StateRevision: 7,
		SerializedCOP: json.RawMessage(`{"revision":7}`),
	}
	if _, err := terraClient.Assess(ctx, req); err != nil {
		t.Fatalf("record Assess: %v", err)
	}
	if terraStub.calls.Load() != 1 {
		t.Fatalf("inner terra calls = %d, want 1", terraStub.calls.Load())
	}
	if mem.Len() < 1 {
		t.Fatal("expected at least one recording after Assess")
	}
	_ = solClient
	_ = bundle
}

func TestComposeModelsReplayModeDoesNotCallNetwork(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-replay.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	mem := cassette.NewMemoryStore()
	// Pre-seed a Terra recording so Assess succeeds without an inner client.
	seedReq := terra.Request{
		StateRevision: 7,
		SerializedCOP: json.RawMessage(`{"revision":7}`),
	}
	key, fp, err := cassette.TerraKey(seedReq, cassette.KeyMeta{})
	if err != nil {
		t.Fatalf("terra key: %v", err)
	}
	if err := mem.Put(ctx, &cassette.Recording{
		SchemaVersion:      cassette.SchemaVersion,
		Key:                key,
		Agent:              "terra",
		StateRevision:      7,
		RequestFingerprint: fp,
		ResponseID:         "replay-resp",
		InsightJSON:        json.RawMessage(`{"schema_version":"1.0.0","insight_id":"ins-replay"}`),
	}); err != nil {
		t.Fatalf("seed recording: %v", err)
	}

	// No API key; providers may say live — replay must still compose.
	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{
			CassetteModeRaw: "replay",
			Terra:           contracts.ProviderLive,
			Sol:             contracts.ProviderLive,
			CassetteStore:   mem,
			CassetteDir:     t.TempDir(),
		},
	)
	if err != nil {
		t.Fatalf("compose replay mode: %v", err)
	}
	if bundle.CassetteMode != "replay" {
		t.Fatalf("CassetteMode = %q, want replay", bundle.CassetteMode)
	}
	if bundle.ProviderSelection[openaimodel.AgentTerra] != contracts.ProviderFixture {
		t.Fatalf("reported terra provider = %s, want fixture under replay", bundle.ProviderSelection[openaimodel.AgentTerra])
	}

	// Direct ModeReplay path: no inner, pre-seeded store, no network.
	terraClient, solClient, mode, _, err := applyCassette(
		cassette.ModeReplay,
		modelEnv{CassetteStore: mem, CassetteDir: t.TempDir()},
		nil, nil,
		contracts.AgentProviderSelection{},
		versionedPrompt{}, versionedPrompt{},
	)
	if err != nil {
		t.Fatalf("applyCassette replay: %v", err)
	}
	if mode != cassette.ModeReplay {
		t.Fatalf("mode = %s", mode)
	}
	resp, err := terraClient.Assess(ctx, seedReq)
	if err != nil {
		t.Fatalf("replay Assess: %v", err)
	}
	if resp.ResponseID != "replay-resp" {
		t.Fatalf("response id = %q", resp.ResponseID)
	}
	// Miss must not fall through to a network client.
	_, err = terraClient.Assess(ctx, terra.Request{
		StateRevision: 99,
		SerializedCOP: json.RawMessage(`{"revision":99}`),
	})
	if err == nil || !errors.Is(err, cassette.ErrReplayMiss) {
		t.Fatalf("miss error = %v, want ErrReplayMiss", err)
	}
	_, err = solClient.Brief(ctx, sol.Request{
		StateRevision: 1,
		SerializedCOP: json.RawMessage(`{}`),
		RequestedBy:   "supervisor-demo",
	})
	if err == nil || !errors.Is(err, cassette.ErrReplayMiss) {
		t.Fatalf("sol miss error = %v, want ErrReplayMiss", err)
	}
}

func TestComposeModelsInvalidCassetteMode(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-bad-mode.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{CassetteModeRaw: "not-a-mode"},
	)
	if err == nil || !strings.Contains(err.Error(), "parse cassette mode") {
		t.Fatalf("error = %v, want parse cassette mode", err)
	}
}

// countingTerraClient is a test double for live Terra StructuredClient injection.
type countingTerraClient struct {
	calls atomic.Int32
	resp  terra.Response
	err   error
}

func (c *countingTerraClient) Assess(_ context.Context, _ terra.Request) (terra.Response, error) {
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

// countingSolClient is a test double for live Sol StructuredClient injection.
type countingSolClient struct {
	calls atomic.Int32
	resp  sol.Response
	err   error
}

func (c *countingSolClient) Brief(_ context.Context, _ sol.Request) (sol.Response, error) {
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

func TestComposeModelsLiveTerraRequiresVersionedPrompt(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-live-missing-prompt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = composeModels(ctx, database, t.TempDir(),
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{APIKey: "test-key", Terra: contracts.ProviderLive},
	)
	if err == nil || !strings.Contains(err.Error(), "load live Terra prompt") {
		t.Fatalf("compose live Terra without prompt error = %v", err)
	}
}

func TestComposeModelsLiveLunaRequiresVersionedPrompt(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-live-luna-missing-prompt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = composeModels(ctx, database, t.TempDir(),
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{APIKey: "test-key", Luna: contracts.ProviderLive},
	)
	if err == nil || !strings.Contains(err.Error(), "load live Luna prompt") {
		t.Fatalf("compose live Luna without prompt error = %v", err)
	}
}
func TestComposeModelsLiveSelectionWithKey(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-live.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Without MOSAIC_SIM_MODE, live providers still compose; cassette stays passthrough.
	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{
			APIKey: "test-key",
			Luna:   contracts.ProviderLive,
			Terra:  contracts.ProviderLive,
			Sol:    contracts.ProviderLive,
		},
	)
	if err != nil {
		t.Fatalf("compose live models: %v", err)
	}
	for _, agent := range []string{openaimodel.AgentLuna, openaimodel.AgentTerra, openaimodel.AgentSol} {
		if bundle.ProviderSelection[agent] != contracts.ProviderLive {
			t.Fatalf("provider %s = %s, want live", agent, bundle.ProviderSelection[agent])
		}
	}
	if bundle.CassetteMode != "passthrough" {
		t.Fatalf("CassetteMode = %q, want passthrough when sim mode unset", bundle.CassetteMode)
	}
}

func TestComposeModelsLiveWithoutKeyFallsBackToFixture(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-fallback.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	bundle, err := composeModels(ctx, database, root,
		filepath.Join(root, "datasets", simulator.DomesticDisturbance),
		filepath.Join(root, "ontology"),
		"supervisor-demo",
		modelEnv{
			APIKey: "",
			Luna:   contracts.ProviderLive,
			Terra:  contracts.ProviderLive,
			Sol:    contracts.ProviderLive,
		},
	)
	if err != nil {
		t.Fatalf("compose fallback models: %v", err)
	}
	for _, agent := range []string{openaimodel.AgentLuna, openaimodel.AgentTerra, openaimodel.AgentSol} {
		if bundle.ProviderSelection[agent] != contracts.ProviderFixture {
			t.Fatalf("provider %s = %s, want fixture fallback", agent, bundle.ProviderSelection[agent])
		}
	}
}
