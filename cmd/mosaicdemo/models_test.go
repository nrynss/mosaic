package main

import (
	"context"
	"path/filepath"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/store"
)

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
