package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/terra"
)

// modelEnv holds server-only runtime inputs for agent provider selection.
// The OpenAI key never comes from flags or the UI — only process environment.
type modelEnv struct {
	APIKey string
	// Per-agent selections: "fixture" (default) or "live".
	Luna  contracts.ModelProvider
	Terra contracts.ModelProvider
	Sol   contracts.ModelProvider
}

// parseModelEnv reads OPENAI_API_KEY and optional MOSAIC_*_PROVIDER values.
// Unknown or empty provider values default to fixture.
func parseModelEnv(getenv func(string) string) modelEnv {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return modelEnv{
		APIKey: strings.TrimSpace(getenv("OPENAI_API_KEY")),
		Luna:   parseProvider(getenv("MOSAIC_LUNA_PROVIDER")),
		Terra:  parseProvider(getenv("MOSAIC_TERRA_PROVIDER")),
		Sol:    parseProvider(getenv("MOSAIC_SOL_PROVIDER")),
	}
}

func parseProvider(value string) contracts.ModelProvider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(contracts.ProviderLive):
		return contracts.ProviderLive
	default:
		return contracts.ProviderFixture
	}
}

// selectionFromEnv builds the requested AgentProviderSelection (before key gating).
func selectionFromEnv(env modelEnv) contracts.AgentProviderSelection {
	return contracts.AgentProviderSelection{
		openaimodel.AgentLuna:  env.Luna,
		openaimodel.AgentTerra: env.Terra,
		openaimodel.AgentSol:   env.Sol,
	}
}

// effectiveSelection applies the non-negotiable rule: live is used only when
// explicitly requested and a server-side API key is present. Otherwise fixture.
func effectiveSelection(requested contracts.AgentProviderSelection, apiKey string) contracts.AgentProviderSelection {
	key := strings.TrimSpace(apiKey)
	out := contracts.AgentProviderSelection{}
	for _, agent := range []string{openaimodel.AgentLuna, openaimodel.AgentTerra, openaimodel.AgentSol} {
		want := requested[agent]
		if want == contracts.ProviderLive && key != "" {
			out[agent] = contracts.ProviderLive
		} else {
			out[agent] = contracts.ProviderFixture
		}
	}
	return out
}

// modelBundle is the composed operator model surface for the API server.
type modelBundle struct {
	Luna              contracts.LunaAdapter
	Terra             contracts.TerraAdapter
	Sol               contracts.SolAdapter
	ProviderSelection contracts.AgentProviderSelection
	BriefingRequester string
}

const fixtureInteractivePromptVersion = "mosaic-fixture-interactive-v1"

type versionedPrompt struct {
	Instructions string
	Provenance   string
}

func loadVersionedPrompt(assetRoot, agent, version string) (versionedPrompt, error) {
	path := filepath.Join(assetRoot, "prompts", agent, version+".md")
	contents, err := os.ReadFile(path)
	if err != nil {
		return versionedPrompt{}, fmt.Errorf("read %s prompt: %w", agent, err)
	}
	instructions := strings.TrimSpace(string(contents))
	if instructions == "" {
		return versionedPrompt{}, fmt.Errorf("%s prompt %q is empty", agent, path)
	}
	sum := sha256.Sum256(contents)
	return versionedPrompt{
		Instructions: instructions,
		Provenance:   version + "+sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

// composeModels wires fixture/live structured clients behind Terra/Sol services
// and a Luna adapter. Live OpenAI clients are constructed only when the
// effective selection is live (explicit + key). The key is never logged.
func composeModels(
	ctx context.Context,
	database *store.Store,
	assetRoot string,
	fixtureDir string,
	schemaDir string,
	supervisorIdentity string,
	env modelEnv,
) (modelBundle, error) {
	if database == nil {
		return modelBundle{}, fmt.Errorf("store is required for model composition")
	}
	requested := selectionFromEnv(env)
	effective := effectiveSelection(requested, env.APIKey)

	fixture, err := simulator.LoadFixture(fixtureDir)
	if err != nil {
		return modelBundle{}, fmt.Errorf("load model fixtures: %w", err)
	}
	fixtureLuna, err := simulator.NewFixtureLuna(fixture)
	if err != nil {
		return modelBundle{}, fmt.Errorf("compose fixture Luna: %w", err)
	}

	// Structured fixture clients refuse interactive generation; historical
	// fixture advisories remain authoritative on the default path.
	fixtureTerraClient := refuseTerraClient{detail: "fixture mode: interactive Terra assessment declined; historical advisories remain authoritative"}
	fixtureSolClient := refuseSolClient{detail: "fixture mode: interactive Sol briefing declined; historical advisories remain authoritative"}

	var liveLuna openaimodel.LunaStructuredClient
	var liveTerra terra.StructuredClient
	var liveSol sol.StructuredClient
	var terraPrompt, solPrompt versionedPrompt
	if effective[openaimodel.AgentLuna] == contracts.ProviderLive ||
		effective[openaimodel.AgentTerra] == contracts.ProviderLive ||
		effective[openaimodel.AgentSol] == contracts.ProviderLive {
		// Construct only the live clients that are actually selected. Terra and
		// Sol prompts are immutable assets, loaded only for a live invocation;
		// fixture mode remains prompt-independent.
		base := openaimodel.Config{APIKey: env.APIKey}
		if effective[openaimodel.AgentLuna] == contracts.ProviderLive {
			client, err := openaimodel.NewLunaClient(base)
			if err != nil {
				return modelBundle{}, fmt.Errorf("compose live Luna client: %w", err)
			}
			liveLuna = client
		}
		if effective[openaimodel.AgentTerra] == contracts.ProviderLive {
			terraPrompt, err = loadVersionedPrompt(assetRoot, openaimodel.AgentTerra, "v1.0.0")
			if err != nil {
				return modelBundle{}, fmt.Errorf("load live Terra prompt: %w", err)
			}
			client, err := openaimodel.NewTerraClient(openaimodel.Config{
				APIKey:       base.APIKey,
				Instructions: terraPrompt.Instructions,
			})
			if err != nil {
				return modelBundle{}, fmt.Errorf("compose live Terra client: %w", err)
			}
			liveTerra = client
		}
		if effective[openaimodel.AgentSol] == contracts.ProviderLive {
			solPrompt, err = loadVersionedPrompt(assetRoot, openaimodel.AgentSol, "v1.0.0")
			if err != nil {
				return modelBundle{}, fmt.Errorf("load live Sol prompt: %w", err)
			}
			client, err := openaimodel.NewSolClient(openaimodel.Config{
				APIKey:       base.APIKey,
				Instructions: solPrompt.Instructions,
			})
			if err != nil {
				return modelBundle{}, fmt.Errorf("compose live Sol client: %w", err)
			}
			liveSol = client
		}
	}

	// Luna structured clients: fixture path uses FixtureLuna as the adapter
	// directly; live path wraps the OpenAI structured client.
	selected, err := openaimodel.Select(openaimodel.SelectConfig{
		Selection:    effective,
		APIKey:       env.APIKey,
		LiveLuna:     liveLuna,
		LiveTerra:    liveTerra,
		LiveSol:      liveSol,
		FixtureLuna:  lunaStructuredShim{adapter: fixtureLuna},
		FixtureTerra: fixtureTerraClient,
		FixtureSol:   fixtureSolClient,
	})
	if err != nil {
		return modelBundle{}, fmt.Errorf("select model clients: %w", err)
	}

	var luna contracts.LunaAdapter
	if effective[openaimodel.AgentLuna] == contracts.ProviderLive {
		luna = &liveLunaAdapter{client: selected.Luna, records: database, clock: time.Now}
	} else {
		luna = fixtureLuna
	}

	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		return modelBundle{}, fmt.Errorf("read advisory history for Terra lifecycle: %w", err)
	}

	terraValidator, err := terra.LoadSchemaValidator(schemaDir)
	if err != nil {
		return modelBundle{}, fmt.Errorf("load Terra schemas: %w", err)
	}
	solValidator, err := sol.LoadSchemaValidator(schemaDir)
	if err != nil {
		return modelBundle{}, fmt.Errorf("load Sol schemas: %w", err)
	}

	terraProvider, terraModel := providerLabels(effective[openaimodel.AgentTerra])
	solProvider, solModel := providerLabels(effective[openaimodel.AgentSol])
	terraPromptVersion := fixtureInteractivePromptVersion
	if effective[openaimodel.AgentTerra] == contracts.ProviderLive {
		terraPromptVersion = terraPrompt.Provenance
	}
	solPromptVersion := fixtureInteractivePromptVersion
	if effective[openaimodel.AgentSol] == contracts.ProviderLive {
		solPromptVersion = solPrompt.Provenance
	}

	terraService, err := terra.New(terra.Config{
		Client:           selected.Terra,
		EvidenceResolver: permissiveEvidence{},
		Records:          database,
		Validator:        terraValidator,
		PromptVersion:    terraPromptVersion,
		Provider:         terraProvider,
		Model:            terraModel,
		ExistingInsights: history.Insights,
	})
	if err != nil {
		return modelBundle{}, fmt.Errorf("compose Terra service: %w", err)
	}

	briefingRequester := strings.TrimSpace(supervisorIdentity)
	if briefingRequester == "" {
		briefingRequester = "supervisor-demo"
	}
	solService, err := sol.New(sol.Config{
		Client:            selected.Sol,
		RequiredRequester: briefingRequester,
		Resolver:          permissiveEvidence{},
		Records:           database,
		Validator:         solValidator,
		PromptVersion:     solPromptVersion,
		Provider:          solProvider,
		Model:             solModel,
	})
	if err != nil {
		return modelBundle{}, fmt.Errorf("compose Sol service: %w", err)
	}

	return modelBundle{
		Luna:              luna,
		Terra:             terraService,
		Sol:               solService,
		ProviderSelection: effective,
		BriefingRequester: briefingRequester,
	}, nil
}

func providerLabels(provider contracts.ModelProvider) (string, string) {
	if provider == contracts.ProviderLive {
		return "openai", openaimodel.DefaultTerraModel
	}
	return "mosaic-fixture", "mosaic-fixture-interactive-v1"
}

// refuseTerraClient is the deterministic interactive fixture path for Terra.
type refuseTerraClient struct {
	detail string
}

func (c refuseTerraClient) Assess(_ context.Context, _ terra.Request) (terra.Response, error) {
	return terra.Response{ResponseID: "fixture-terra-refuse", RefusalDetail: c.detail}, nil
}

// refuseSolClient is the deterministic interactive fixture path for Sol.
type refuseSolClient struct {
	detail string
}

func (c refuseSolClient) Brief(_ context.Context, _ sol.Request) (sol.Response, error) {
	return sol.Response{ResponseID: "fixture-sol-refuse", RefusalDetail: c.detail}, nil
}

// permissiveEvidence satisfies Terra/Sol resolver seams without inventing
// operational data. The services still enforce schema, revision, and evidence
// matching on candidate outputs.
type permissiveEvidence struct{}

func (permissiveEvidence) ResolveEvidence(context.Context, int64, []gen.Evidence) error {
	return nil
}

func (permissiveEvidence) ResolveInsights(context.Context, int64, []gen.Insight) error {
	return nil
}

// lunaStructuredShim adapts FixtureLuna (contracts.LunaAdapter) to the
// openaimodel.Select LunaStructuredClient slot so Select can still route.
// The live LunaAdapter path does not use this shim for real work.
type lunaStructuredShim struct {
	adapter contracts.LunaAdapter
}

func (s lunaStructuredShim) Normalize(ctx context.Context, request openaimodel.LunaRequest) (openaimodel.LunaResponse, error) {
	if s.adapter == nil {
		return openaimodel.LunaResponse{}, fmt.Errorf("fixture Luna adapter is not configured")
	}
	// Select only needs a non-nil fixture Luna when routing; interactive live
	// uses liveLunaAdapter. For fixture selection, FixtureLuna is used directly
	// as contracts.LunaAdapter. This shim exists solely so Select accepts a
	// non-nil FixtureLuna interface value.
	_ = ctx
	_ = request
	return openaimodel.LunaResponse{RefusalDetail: "fixture Luna structured shim is not invoked for API Normalize"}, nil
}

// liveLunaAdapter maps openaimodel structured output to contracts.LunaAdapter.
// Transport failures and refusals produce a failed/refused ModelRun with no
// CanonicalEvent and no state mutation beyond the returned artifacts.
type liveLunaAdapter struct {
	client  openaimodel.LunaStructuredClient
	records contracts.ImmutableRecordRepository
	clock   func() time.Time
}

func (a *liveLunaAdapter) Normalize(ctx context.Context, raw gen.RawEvent) (contracts.LunaOutput, error) {
	if a == nil || a.client == nil {
		return contracts.LunaOutput{}, fmt.Errorf("live Luna client is not configured")
	}
	if a.clock == nil {
		a.clock = time.Now
	}
	started := a.clock().UTC()
	encoded, err := json.Marshal(raw)
	if err != nil {
		return contracts.LunaOutput{}, err
	}
	response, callErr := a.client.Normalize(ctx, openaimodel.LunaRequest{RawEventJSON: encoded})
	completed := a.clock().UTC()
	if callErr != nil {
		run := lunaFailureRun(raw, started, completed, "failed", callErr.Error(), "")
		return contracts.LunaOutput{ModelRun: run, Result: quarantinedLunaResult(raw, started)}, nil
	}
	if strings.TrimSpace(response.RefusalDetail) != "" {
		run := lunaFailureRun(raw, started, completed, "refused", response.RefusalDetail, response.ResponseID)
		return contracts.LunaOutput{ModelRun: run, Result: quarantinedLunaResult(raw, started)}, nil
	}
	var result gen.LunaResult
	if err := json.Unmarshal(response.ResultJSON, &result); err != nil {
		run := lunaFailureRun(raw, started, completed, "invalid", err.Error(), response.ResponseID)
		return contracts.LunaOutput{ModelRun: run, Result: quarantinedLunaResult(raw, started)}, nil
	}
	run := gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          "modelrun-live-luna-" + raw.RawEventID,
		Agent:               "luna",
		Provider:            "openai",
		Model:               openaimodel.DefaultLunaModel,
		PromptVersion:       "mosaicdemo-interactive-v1",
		OutputSchemaVersion: "1.0.0",
		InputEventIds:       []any{raw.RawEventID},
		OutputIds:           []any{result.LunaResultID},
		ValidationStatus:    "valid",
		ResponseID:          response.ResponseID,
		StartedAt:           started.Format(time.RFC3339Nano),
		CompletedAt:         completed.Format(time.RFC3339Nano),
	}
	out := contracts.LunaOutput{Result: result, ModelRun: run}
	if len(response.CanonicalEventJSON) > 0 {
		var canonical gen.CanonicalEvent
		if err := json.Unmarshal(response.CanonicalEventJSON, &canonical); err == nil {
			out.CanonicalEvent = &canonical
			out.ModelRun.OutputIds = append(out.ModelRun.OutputIds, canonical.CanonicalEventID)
		}
	}
	_ = a.records // ModelRun persistence is owned by ingestion/operator callers
	return out, nil
}

func lunaFailureRun(raw gen.RawEvent, started, completed time.Time, status, detail, responseID string) gen.ModelRun {
	return gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          "modelrun-live-luna-" + raw.RawEventID + "-" + status,
		Agent:               "luna",
		Provider:            "openai",
		Model:               openaimodel.DefaultLunaModel,
		PromptVersion:       "mosaicdemo-interactive-v1",
		OutputSchemaVersion: "1.0.0",
		InputEventIds:       []any{raw.RawEventID},
		ValidationStatus:    status,
		ResponseID:          responseID,
		FailureDetail:       detail,
		StartedAt:           started.Format(time.RFC3339Nano),
		CompletedAt:         completed.Format(time.RFC3339Nano),
	}
}

func quarantinedLunaResult(raw gen.RawEvent, at time.Time) gen.LunaResult {
	return gen.LunaResult{
		SchemaVersion: "1.0.0",
		LunaResultID:  "luna-live-quarantine-" + raw.RawEventID,
		RawEventID:    raw.RawEventID,
		Status:        "quarantined",
		CreatedAt:     at.Format(time.RFC3339Nano),
	}
}
