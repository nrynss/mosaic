package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/store"
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
func TestComposeModelsLiveSelectionWithKey(t *testing.T) {
	ctx := context.Background()
	root := repositoryRoot(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "models-live.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

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
