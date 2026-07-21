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
	"mosaic.local/mosaic/internal/simulation/cassette"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

// defaultCassetteDirName is the directory leaf used under the process temp
// dir when MOSAIC_CASSETTE_DIR is unset. Using os.TempDir keeps Record mode
// working in the read-only Compose image (only /tmp is writable) and still
// lands outside the repo; /recordings remains gitignored for local overrides.
const defaultCassetteDirName = "mosaic-recordings"

// modelEnv holds server-only runtime inputs for agent provider selection and
// simulation cassette mode. The OpenAI key never comes from flags or the UI —
// only process environment.
//
// Provider / cassette mapping (C5):
//
//	MOSAIC_SIM_MODE=fixture|passthrough|off  → ModePassthrough + fixture path (default / CI)
//	MOSAIC_SIM_MODE=live|record              → ModeRecord + live Terra/Sol when key+provider say live
//	MOSAIC_SIM_MODE=replay|recorded          → ModeReplay + FileStore; OPENAI_API_KEY not required
//
// MOSAIC_CASSETTE_MODE is accepted as an alias of MOSAIC_SIM_MODE (SIM wins if both set).
// Per-agent MOSAIC_*_PROVIDER still gates which agents are live when mode is live/record.
// Live/record without a key falls back to fixture providers (existing effectiveSelection)
// and skips ModeRecord wrapping so refusals are not banked.
type modelEnv struct {
	APIKey string
	// Per-agent selections: "fixture" (default) or "live".
	Luna  contracts.ModelProvider
	Terra contracts.ModelProvider
	Sol   contracts.ModelProvider

	// CassetteModeRaw is the unparsed MOSAIC_SIM_MODE / MOSAIC_CASSETTE_MODE value.
	// Empty means fixture/passthrough (cassette.ParseMode default).
	CassetteModeRaw string
	// CassetteDir is MOSAIC_CASSETTE_DIR; empty means defaultCassetteDir.
	CassetteDir string

	// CassetteStore, when non-nil, overrides FileStore construction (tests).
	CassetteStore cassette.Store
	// testLiveTerra / testLiveSol, when non-nil, replace OpenAI client construction
	// for the matching live agent (tests inject counting stubs).
	testLiveTerra terra.StructuredClient
	testLiveSol   sol.StructuredClient
}

// parseModelEnv reads OPENAI_API_KEY, optional MOSAIC_*_PROVIDER values, and
// cassette mode/dir. Unknown or empty provider values default to fixture.
// Cassette mode is validated later in composeModels via cassette.ParseMode.
func parseModelEnv(getenv func(string) string) modelEnv {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	simMode := strings.TrimSpace(getenv("MOSAIC_SIM_MODE"))
	if simMode == "" {
		simMode = strings.TrimSpace(getenv("MOSAIC_CASSETTE_MODE"))
	}
	return modelEnv{
		APIKey:          strings.TrimSpace(getenv("OPENAI_API_KEY")),
		Luna:            parseProvider(getenv("MOSAIC_LUNA_PROVIDER")),
		Terra:           parseProvider(getenv("MOSAIC_TERRA_PROVIDER")),
		Sol:             parseProvider(getenv("MOSAIC_SOL_PROVIDER")),
		CassetteModeRaw: strings.ToLower(simMode),
		CassetteDir:     strings.TrimSpace(getenv("MOSAIC_CASSETTE_DIR")),
	}
}

// parseCassetteMode resolves the simulation inference mode from modelEnv.
func parseCassetteMode(env modelEnv) (cassette.Mode, error) {
	return cassette.ParseMode(env.CassetteModeRaw)
}

// resolveCassetteDir returns the FileStore directory.
func resolveCassetteDir(env modelEnv) string {
	if dir := strings.TrimSpace(env.CassetteDir); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), defaultCassetteDirName)
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
	// CassetteMode is the effective cassette decorator mode string
	// (passthrough, record, or replay) for capability/status surfaces (D2).
	CassetteMode string
	// CassetteDir is the FileStore directory used when mode is record/replay
	// (empty when unused / passthrough without a store).
	CassetteDir string
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

type composeStore interface {
	contracts.ImmutableRecordRepository
	contracts.AdvisoryHistoryReader
}

// composeModels wires fixture/live structured clients behind Terra/Sol services
// and a Luna adapter, then applies the C4 cassette decorator for the configured
// simulation inference mode. Live OpenAI clients are constructed only when the
// effective selection is live (explicit + key) and mode is not replay.
// The key is never logged.
func composeModels(
	ctx context.Context,
	database composeStore,
	assetRoot string,
	fixtureDir string,
	schemaDir string,
	supervisorIdentity string,
	env modelEnv,
) (modelBundle, error) {
	if database == nil {
		return modelBundle{}, fmt.Errorf("store is required for model composition")
	}

	requestedMode, err := parseCassetteMode(env)
	if err != nil {
		return modelBundle{}, fmt.Errorf("parse cassette mode: %w", err)
	}

	requested := selectionFromEnv(env)
	effective := effectiveSelection(requested, env.APIKey)

	// Replay never needs a network client for Terra/Sol: force fixture
	// construction selection so OPENAI_API_KEY is optional, then wrap with
	// ModeReplay after Select. Luna is independent of the cassette.
	construct := cloneSelection(effective)
	if requestedMode == cassette.ModeReplay {
		construct[openaimodel.AgentTerra] = contracts.ProviderFixture
		construct[openaimodel.AgentSol] = contracts.ProviderFixture
	}

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
	var lunaPrompt, terraPrompt, solPrompt versionedPrompt
	if construct[openaimodel.AgentLuna] == contracts.ProviderLive ||
		construct[openaimodel.AgentTerra] == contracts.ProviderLive ||
		construct[openaimodel.AgentSol] == contracts.ProviderLive {
		// Construct only the live clients that are actually selected. Terra and
		// Sol prompts are immutable assets, loaded only for a live invocation;
		// fixture mode remains prompt-independent.
		base := openaimodel.Config{APIKey: env.APIKey, SchemaDir: schemaDir}
		if construct[openaimodel.AgentLuna] == contracts.ProviderLive {
			lunaPrompt, err = loadVersionedPrompt(assetRoot, openaimodel.AgentLuna, "v1.0.0")
			if err != nil {
				return modelBundle{}, fmt.Errorf("load live Luna prompt: %w", err)
			}
			client, err := openaimodel.NewLunaClient(openaimodel.Config{
				APIKey:       base.APIKey,
				SchemaDir:    base.SchemaDir,
				Instructions: lunaPrompt.Instructions,
			})
			if err != nil {
				return modelBundle{}, fmt.Errorf("compose live Luna client: %w", err)
			}
			liveLuna = client
		}
		if construct[openaimodel.AgentTerra] == contracts.ProviderLive {
			if env.testLiveTerra != nil {
				liveTerra = env.testLiveTerra
				// Still load prompt when available so cassette provenance is set.
				if p, pErr := loadVersionedPrompt(assetRoot, openaimodel.AgentTerra, "v1.0.0"); pErr == nil {
					terraPrompt = p
				}
			} else {
				terraPrompt, err = loadVersionedPrompt(assetRoot, openaimodel.AgentTerra, "v1.0.0")
				if err != nil {
					return modelBundle{}, fmt.Errorf("load live Terra prompt: %w", err)
				}
				client, err := openaimodel.NewTerraClient(openaimodel.Config{
					APIKey:       base.APIKey,
					SchemaDir:    base.SchemaDir,
					Instructions: terraPrompt.Instructions,
				})
				if err != nil {
					return modelBundle{}, fmt.Errorf("compose live Terra client: %w", err)
				}
				liveTerra = client
			}
		}
		if construct[openaimodel.AgentSol] == contracts.ProviderLive {
			if env.testLiveSol != nil {
				liveSol = env.testLiveSol
				if p, pErr := loadVersionedPrompt(assetRoot, openaimodel.AgentSol, "v1.0.0"); pErr == nil {
					solPrompt = p
				}
			} else {
				solPrompt, err = loadVersionedPrompt(assetRoot, openaimodel.AgentSol, "v1.0.0")
				if err != nil {
					return modelBundle{}, fmt.Errorf("load live Sol prompt: %w", err)
				}
				client, err := openaimodel.NewSolClient(openaimodel.Config{
					APIKey:       base.APIKey,
					SchemaDir:    base.SchemaDir,
					Instructions: solPrompt.Instructions,
				})
				if err != nil {
					return modelBundle{}, fmt.Errorf("compose live Sol client: %w", err)
				}
				liveSol = client
			}
		}
	}

	// Luna structured clients: fixture path uses FixtureLuna as the adapter
	// directly; live path wraps the OpenAI structured client.
	// Select uses construct (replay-forced fixture for Terra/Sol). Luna may
	// still be live and needs the real API key; Terra/Sol stay fixture inners
	// under ModeReplay so no key is required for those agents.
	selected, err := openaimodel.Select(openaimodel.SelectConfig{
		Selection:    construct,
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

	// Apply cassette decorator to Terra/Sol StructuredClients (C5).
	// Effective mode may demote record→passthrough when no live Terra/Sol.
	terraClient, solClient, effectiveMode, cassetteDir, err := applyCassette(
		requestedMode,
		env,
		selected.Terra,
		selected.Sol,
		construct,
		terraPrompt,
		solPrompt,
	)
	if err != nil {
		return modelBundle{}, err
	}

	var luna contracts.LunaAdapter
	if construct[openaimodel.AgentLuna] == contracts.ProviderLive {
		luna = &liveLunaAdapter{client: selected.Luna, records: database, clock: time.Now, promptVersion: lunaPrompt.Provenance}
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

	// Provider labels and prompt versions follow construct (what clients were
	// built for). Replay surfaces as fixture providers with CassetteMode=replay
	// for D2 status; no silent live path.
	terraProvider, terraModel := providerLabels(construct[openaimodel.AgentTerra])
	solProvider, solModel := providerLabels(construct[openaimodel.AgentSol])
	terraPromptVersion := fixtureInteractivePromptVersion
	if construct[openaimodel.AgentTerra] == contracts.ProviderLive && terraPrompt.Provenance != "" {
		terraPromptVersion = terraPrompt.Provenance
	}
	solPromptVersion := fixtureInteractivePromptVersion
	if construct[openaimodel.AgentSol] == contracts.ProviderLive && solPrompt.Provenance != "" {
		solPromptVersion = solPrompt.Provenance
	}
	if effectiveMode == cassette.ModeReplay {
		// Prefer banked recording provenance (version+sha256:hash) over a generic
		// opaque id so ModelRun remains attributable to the recorded prompt (H6).
		terraPromptVersion, solPromptVersion = replayPromptVersions(ctx, env)
		solProvider, solModel = providerLabels(contracts.ProviderFixture)
		terraProvider, terraModel = providerLabels(contracts.ProviderFixture)
	}

	terraService, err := terra.New(terra.Config{
		Client:           terraClient,
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
		Client:            solClient,
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

	// Report provider selection: for replay, keep original effective so UI can
	// still show requested agents; CassetteMode is the honest inference path.
	reported := effective
	if effectiveMode == cassette.ModeReplay {
		// Terra/Sol are store-backed, not live network; report fixture.
		reported = cloneSelection(effective)
		reported[openaimodel.AgentTerra] = contracts.ProviderFixture
		reported[openaimodel.AgentSol] = contracts.ProviderFixture
	}

	return modelBundle{
		Luna:              luna,
		Terra:             terraService,
		Sol:               solService,
		ProviderSelection: reported,
		BriefingRequester: briefingRequester,
		CassetteMode:      effectiveMode.String(),
		CassetteDir:       cassetteDir,
	}, nil
}

// applyCassette wraps Terra/Sol clients with the C4 cassette decorator.
//
//	passthrough/fixture → leave clients as-is (no decorator)
//	record/live         → ModeRecord + FileStore around selected inners when at
//	                      least one of Terra/Sol is live; otherwise demote to
//	                      passthrough (no key / all fixture → safe CI path)
//	replay/recorded     → ModeReplay + FileStore; inner is nil (miss = ErrReplayMiss)
func applyCassette(
	mode cassette.Mode,
	env modelEnv,
	terraInner terra.StructuredClient,
	solInner sol.StructuredClient,
	construct contracts.AgentProviderSelection,
	terraPrompt, solPrompt versionedPrompt,
) (terra.StructuredClient, sol.StructuredClient, cassette.Mode, string, error) {
	switch mode {
	case cassette.ModePassthrough:
		return terraInner, solInner, cassette.ModePassthrough, "", nil

	case cassette.ModeRecord:
		terraLive := construct[openaimodel.AgentTerra] == contracts.ProviderLive
		solLive := construct[openaimodel.AgentSol] == contracts.ProviderLive
		if !terraLive && !solLive {
			// Match effectiveSelection fallback: mode=live without key/providers
			// stays on the fixture path; do not bank refuse responses.
			return terraInner, solInner, cassette.ModePassthrough, "", nil
		}
		store, dir, err := openCassetteStore(env)
		if err != nil {
			return nil, nil, 0, "", err
		}
		terraOut := terraInner
		solOut := solInner
		if terraLive {
			wrapped := cassette.NewTerra(cassette.ModeRecord, store, terraInner)
			wrapped.PromptVersion, wrapped.PromptHash = splitPromptProvenance(terraPrompt.Provenance)
			terraOut = wrapped
		}
		if solLive {
			wrapped := cassette.NewSol(cassette.ModeRecord, store, solInner)
			wrapped.PromptVersion, wrapped.PromptHash = splitPromptProvenance(solPrompt.Provenance)
			solOut = wrapped
		}
		return terraOut, solOut, cassette.ModeRecord, dir, nil

	case cassette.ModeReplay:
		store, dir, err := openCassetteStore(env)
		if err != nil {
			return nil, nil, 0, "", err
		}
		// Inner is nil: ModeReplay must never fall through to the network.
		return cassette.NewTerra(cassette.ModeReplay, store, nil),
			cassette.NewSol(cassette.ModeReplay, store, nil),
			cassette.ModeReplay, dir, nil

	default:
		return nil, nil, 0, "", fmt.Errorf("unsupported cassette mode %s", mode)
	}
}

// openCassetteStore returns an injectable Store or a FileStore under CassetteDir.
func openCassetteStore(env modelEnv) (cassette.Store, string, error) {
	dir := resolveCassetteDir(env)
	if env.CassetteStore != nil {
		return env.CassetteStore, dir, nil
	}
	store, err := cassette.NewFileStore(dir)
	if err != nil {
		return nil, "", fmt.Errorf("compose cassette file store: %w", err)
	}
	return store, dir, nil
}

// splitPromptProvenance splits "v1.0.0+sha256:hex" into version and hash for
// cassette Recording provenance fields.
func splitPromptProvenance(provenance string) (version, hash string) {
	provenance = strings.TrimSpace(provenance)
	const marker = "+sha256:"
	if i := strings.Index(provenance, marker); i >= 0 {
		return provenance[:i], provenance[i+len(marker):]
	}
	return provenance, ""
}

// cassetteReplayPromptVersion is the fallback ModelRun.PromptVersion when
// ModeReplay has no banked prompt_version/prompt_hash on any recording.
const cassetteReplayPromptVersion = "mosaic-cassette-replay-v1"

// replayPromptVersions resolves honest Terra/Sol PromptVersion strings for
// ModeReplay from banked cassette recordings. Falls back to
// cassetteReplayPromptVersion when provenance was not recorded (legacy banks).
// Store open/List failures are logged once so a misconfigured cassette dir is
// not silent until the first Assess miss.
func replayPromptVersions(ctx context.Context, env modelEnv) (terraPV, solPV string) {
	terraPV = cassetteReplayPromptVersion
	solPV = cassetteReplayPromptVersion
	store, dir, err := openCassetteStore(env)
	if err != nil || store == nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "mosaicdemo: cassette provenance scan skipped (open store %q): %v\n", dir, err)
		}
		return terraPV, solPV
	}
	recs, err := store.List(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mosaicdemo: cassette provenance scan failed (list %q): %v\n", dir, err)
		return terraPV, solPV
	}
	if p := cassette.BankedPromptProvenance(recs, cassette.AgentTerra); p != "" {
		terraPV = p
	}
	if p := cassette.BankedPromptProvenance(recs, cassette.AgentSol); p != "" {
		solPV = p
	}
	return terraPV, solPV
}

func cloneSelection(in contracts.AgentProviderSelection) contracts.AgentProviderSelection {
	out := contracts.AgentProviderSelection{}
	for k, v := range in {
		out[k] = v
	}
	return out
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
	client        openaimodel.LunaStructuredClient
	records       contracts.ImmutableRecordRepository
	clock         func() time.Time
	promptVersion string
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
		run := lunaFailureRun(raw, started, completed, "failed", callErr.Error(), "", a.promptVersion)
		return contracts.LunaOutput{ModelRun: run, Result: quarantinedLunaResult(raw, started)}, nil
	}
	if strings.TrimSpace(response.RefusalDetail) != "" {
		run := lunaFailureRun(raw, started, completed, "refused", response.RefusalDetail, response.ResponseID, a.promptVersion)
		return contracts.LunaOutput{ModelRun: run, Result: quarantinedLunaResult(raw, started)}, nil
	}
	var result gen.LunaResult
	if err := json.Unmarshal(response.ResultJSON, &result); err != nil {
		run := lunaFailureRun(raw, started, completed, "invalid", err.Error(), response.ResponseID, a.promptVersion)
		return contracts.LunaOutput{ModelRun: run, Result: quarantinedLunaResult(raw, started)}, nil
	}
	run := gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          "modelrun-live-luna-" + raw.RawEventID,
		Agent:               "luna",
		Provider:            "openai",
		Model:               openaimodel.DefaultLunaModel,
		PromptVersion:       a.promptVersion,
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

func lunaFailureRun(raw gen.RawEvent, started, completed time.Time, status, detail, responseID, promptVersion string) gen.ModelRun {
	return gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          "modelrun-live-luna-" + raw.RawEventID + "-" + status,
		Agent:               "luna",
		Provider:            "openai",
		Model:               openaimodel.DefaultLunaModel,
		PromptVersion:       promptVersion,
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
