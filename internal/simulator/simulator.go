// Package simulator assembles the deterministic v0.1 demo spine around the
// frozen domestic-disturbance dataset. It deliberately contains no live model
// call: FixtureLuna maps only the checked-in synthetic fixture identifiers to
// their validated, structured normalization artifacts.
package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ingestion"
	"mosaic.local/mosaic/internal/luna"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/replay"
	"mosaic.local/mosaic/internal/state"
	"mosaic.local/mosaic/internal/store"
)

const (
	// DomesticDisturbance is the only executable v0.1 fixture scenario.
	DomesticDisturbance  = "domestic-disturbance"
	fixtureModel         = "mosaic-fixture-luna-v1"
	fixturePromptVersion = "domestic-disturbance-fixture-v1"
)

var (
	// ErrUnknownFixtureRaw prevents the demo normalizer from pretending it can
	// normalize source records outside the frozen synthetic scenario.
	ErrUnknownFixtureRaw = errors.New("raw event is not part of the fixture scenario")
)

// Beat is one ordered raw delivery in a frozen scenario.
type Beat struct {
	BeatID     string `json:"beat_id"`
	Order      int    `json:"order"`
	RawEventID string `json:"raw_event_id"`
	DelayMS    int    `json:"delay_ms,omitempty"`
}

// TerraObsolescenceExpectation is intentionally an expectation only. P07
// proves that the fixture has the evidence needed for P10; it does not create
// an Insight or otherwise implement Terra.
type TerraObsolescenceExpectation struct {
	ActiveInsightID       string `json:"active_insight_id"`
	ObsoleteInsightID     string `json:"obsolete_insight_id"`
	AfterCanonicalEventID string `json:"after_canonical_event_id"`
}

// FixtureExpectation captures the fixture checks that P07 can verify without
// taking ownership of Terra or Sol artifacts.
type FixtureExpectation struct {
	TerraObsolescence TerraObsolescenceExpectation `json:"terra_obsolescence"`
}

// Fixture is the decoded, immutable domestic-disturbance test scenario.
// Maps are private so callers cannot accidentally edit the deterministic
// records used by FixtureLuna.
type Fixture struct {
	ScenarioID   string
	Beats        []Beat
	Expectation  FixtureExpectation
	rawByID      map[string]gen.RawEvent
	outputByRaw  map[string]fixtureOutput
	canonicalIDs map[string]struct{}
	insights     map[string]gen.Insight
}

// RawEvent returns a copy of the frozen envelope associated with id.
func (f *Fixture) RawEvent(id string) (gen.RawEvent, bool) {
	raw, ok := f.rawByID[id]
	return raw, ok
}

type fixtureOutput struct {
	result    gen.LunaResult
	canonical *gen.CanonicalEvent
}

type scenarioDocument struct {
	SchemaVersion     string `json:"schema_version"`
	ScenarioID        string `json:"scenario_id"`
	Version           string `json:"version"`
	Title             string `json:"title"`
	DatasetManifestID string `json:"dataset_manifest_id"`
	Beats             []Beat `json:"beats"`
}

type rawEventsDocument struct {
	Version           string         `json:"version"`
	DatasetManifestID string         `json:"dataset_manifest_id"`
	RawEvents         []gen.RawEvent `json:"raw_events"`
}

type expectedOutcomesDocument struct {
	Version         string                `json:"version"`
	ScenarioID      string                `json:"scenario_id"`
	LunaResults     []gen.LunaResult      `json:"luna_results"`
	CanonicalEvents []gen.CanonicalEvent  `json:"canonical_events"`
	Insights        []gen.Insight         `json:"insights"`
	Recommendations []gen.Recommendation  `json:"recommendations"`
	AuditRecords    []gen.AuditRecord     `json:"audit_records"`
	Checks          fixtureChecksDocument `json:"checks"`
}

type fixtureChecksDocument struct {
	BaselineRawEventIDs []string          `json:"baseline_raw_event_ids"`
	Repaired            fixtureEventCheck `json:"repaired"`
	Quarantined         fixtureEventCheck `json:"quarantined"`
	LateDelivery        fixtureEventCheck `json:"late_delivery"`
	RoadCorrection      struct {
		CanonicalEventID  string `json:"canonical_event_id"`
		SupersedesEventID string `json:"supersedes_event_id"`
	} `json:"road_correction"`
	TerraObsolescence TerraObsolescenceExpectation `json:"terra_obsolescence"`
	SolRequest        struct {
		AuditRecordID    string `json:"audit_record_id"`
		BriefingID       string `json:"briefing_id"`
		RecommendationID string `json:"recommendation_id"`
		RequestedBy      string `json:"requested_by"`
	} `json:"sol_request"`
	SupervisorAction struct {
		AuditRecordID    string `json:"audit_record_id"`
		RecommendationID string `json:"recommendation_id"`
		ActorID          string `json:"actor_id"`
		Action           string `json:"action"`
	} `json:"supervisor_action"`
}

type fixtureEventCheck struct {
	RawEventID       string `json:"raw_event_id"`
	LunaResultID     string `json:"luna_result_id"`
	CanonicalEventID string `json:"canonical_event_id"`
}

// LoadFixture decodes one P04 dataset directory. The P04 validator remains
// the authoritative artifact validator; these checks make the simulator's
// fixture mapping explicit and fail closed if its required relationships are
// absent.
func LoadFixture(dir string) (*Fixture, error) {
	var scenario scenarioDocument
	if err := decodeFile(filepath.Join(dir, "scenario.json"), &scenario); err != nil {
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	if scenario.ScenarioID == "" || len(scenario.Beats) == 0 {
		return nil, errors.New("scenario requires an identifier and at least one beat")
	}

	var rawDocument rawEventsDocument
	if err := decodeFile(filepath.Join(dir, "raw-events.json"), &rawDocument); err != nil {
		return nil, fmt.Errorf("decode raw events: %w", err)
	}
	var outcomes expectedOutcomesDocument
	if err := decodeFile(filepath.Join(dir, "expected-outcomes.json"), &outcomes); err != nil {
		return nil, fmt.Errorf("decode expected outcomes: %w", err)
	}
	if outcomes.ScenarioID != scenario.ScenarioID {
		return nil, fmt.Errorf("expected outcomes scenario %q does not match %q", outcomes.ScenarioID, scenario.ScenarioID)
	}

	fixture := &Fixture{
		ScenarioID:   scenario.ScenarioID,
		Beats:        append([]Beat(nil), scenario.Beats...),
		Expectation:  FixtureExpectation{TerraObsolescence: outcomes.Checks.TerraObsolescence},
		rawByID:      make(map[string]gen.RawEvent, len(rawDocument.RawEvents)),
		outputByRaw:  make(map[string]fixtureOutput, len(outcomes.LunaResults)),
		canonicalIDs: make(map[string]struct{}, len(outcomes.CanonicalEvents)),
		insights:     make(map[string]gen.Insight, len(outcomes.Insights)),
	}
	for _, raw := range rawDocument.RawEvents {
		if raw.RawEventID == "" {
			return nil, errors.New("raw event is missing raw_event_id")
		}
		if _, exists := fixture.rawByID[raw.RawEventID]; exists {
			return nil, fmt.Errorf("duplicate raw event %q", raw.RawEventID)
		}
		fixture.rawByID[raw.RawEventID] = raw
	}

	canonicalByID := make(map[string]gen.CanonicalEvent, len(outcomes.CanonicalEvents))
	for _, event := range outcomes.CanonicalEvents {
		if event.CanonicalEventID == "" || event.RawEventID == "" {
			return nil, errors.New("canonical event requires identifiers")
		}
		if _, exists := canonicalByID[event.CanonicalEventID]; exists {
			return nil, fmt.Errorf("duplicate canonical event %q", event.CanonicalEventID)
		}
		canonicalByID[event.CanonicalEventID] = event
		fixture.canonicalIDs[event.CanonicalEventID] = struct{}{}
	}
	for _, result := range outcomes.LunaResults {
		if result.RawEventID == "" || result.LunaResultID == "" {
			return nil, errors.New("Luna result requires identifiers")
		}
		if _, exists := fixture.rawByID[result.RawEventID]; !exists {
			return nil, fmt.Errorf("Luna result %q references unknown raw event %q", result.LunaResultID, result.RawEventID)
		}
		if _, exists := fixture.outputByRaw[result.RawEventID]; exists {
			return nil, fmt.Errorf("raw event %q has more than one Luna result", result.RawEventID)
		}
		output := fixtureOutput{result: result}
		if result.CanonicalEventID != "" {
			canonical, exists := canonicalByID[result.CanonicalEventID]
			if !exists || canonical.RawEventID != result.RawEventID {
				return nil, fmt.Errorf("Luna result %q has invalid canonical event %q", result.LunaResultID, result.CanonicalEventID)
			}
			canonicalCopy := canonical
			output.canonical = &canonicalCopy
		}
		fixture.outputByRaw[result.RawEventID] = output
	}
	for rawID := range fixture.rawByID {
		if _, exists := fixture.outputByRaw[rawID]; !exists {
			return nil, fmt.Errorf("raw event %q has no fixture Luna result", rawID)
		}
	}
	for _, insight := range outcomes.Insights {
		if insight.InsightID == "" {
			return nil, errors.New("fixture insight is missing insight_id")
		}
		fixture.insights[insight.InsightID] = insight
	}

	sort.Slice(fixture.Beats, func(i, j int) bool { return fixture.Beats[i].Order < fixture.Beats[j].Order })
	for index, beat := range fixture.Beats {
		if beat.Order != index+1 || beat.BeatID == "" {
			return nil, fmt.Errorf("scenario beats must be contiguous; beat %q has order %d", beat.BeatID, beat.Order)
		}
		if _, exists := fixture.rawByID[beat.RawEventID]; !exists {
			return nil, fmt.Errorf("beat %q references unknown raw event %q", beat.BeatID, beat.RawEventID)
		}
	}
	return fixture, nil
}

func decodeFile(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("contains more than one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// FixtureLuna implements contracts.LunaAdapter without any model invocation.
// Each output is a copy of the frozen outcome, augmented only with deterministic
// runtime ModelRun provenance required by P05's relationship checks.
type FixtureLuna struct {
	fixture *Fixture
	mu      sync.Mutex
	calls   int
}

var _ contracts.LunaAdapter = (*FixtureLuna)(nil)

// NewFixtureLuna constructs a normalizer restricted to one loaded fixture.
func NewFixtureLuna(fixture *Fixture) (*FixtureLuna, error) {
	if fixture == nil {
		return nil, errors.New("fixture is required")
	}
	return &FixtureLuna{fixture: fixture}, nil
}

// Normalize returns the declared fixture lifecycle for one raw-event ID.
func (l *FixtureLuna) Normalize(_ context.Context, raw gen.RawEvent) (contracts.LunaOutput, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++

	fixtureOutput, exists := l.fixture.outputByRaw[raw.RawEventID]
	if !exists {
		return contracts.LunaOutput{}, fmt.Errorf("%w: %q", ErrUnknownFixtureRaw, raw.RawEventID)
	}
	result := fixtureOutput.result
	runID := "modelrun-" + result.LunaResultID
	run := gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          runID,
		Agent:               "luna",
		Provider:            "mosaic-fixture",
		Model:               fixtureModel,
		PromptVersion:       fixturePromptVersion,
		OutputSchemaVersion: "1.0.0",
		InputEventIds:       []any{raw.RawEventID},
		OutputIds:           []any{result.LunaResultID},
		ValidationStatus:    "valid",
		StartedAt:           raw.ReceivedAt,
		CompletedAt:         result.CreatedAt,
	}

	output := contracts.LunaOutput{Result: result, ModelRun: run}
	if fixtureOutput.canonical == nil {
		return output, nil
	}
	canonical := *fixtureOutput.canonical
	provenance := map[string]any{}
	if err := json.Unmarshal(canonical.Provenance, &provenance); err != nil {
		return contracts.LunaOutput{}, fmt.Errorf("decode fixture canonical provenance: %w", err)
	}
	provenance["raw_event_id"] = raw.RawEventID
	provenance["model_run_id"] = runID
	encoded, err := json.Marshal(provenance)
	if err != nil {
		return contracts.LunaOutput{}, fmt.Errorf("encode fixture canonical provenance: %w", err)
	}
	canonical.Provenance = encoded
	output.CanonicalEvent = &canonical
	output.ModelRun.OutputIds = append(output.ModelRun.OutputIds, canonical.CanonicalEventID)
	return output, nil
}

// Calls returns the number of actual normalizer invocations. It supports the
// exact-delivery idempotency proof without exposing mutable adapter state.
func (l *FixtureLuna) Calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// Config supplies the already-open local P03 store and the checked-in P02/P04
// inputs needed to compose the deterministic simulator.
type Config struct {
	Store      *store.Store
	SchemaDir  string
	FixtureDir string
}

// Service is the executable v0.1 spine: fixture source, P05 ingestion, P06
// projection, and P06 replay recovery over a P03 SQLite store.
type Service struct {
	store      *store.Store
	fixture    *Fixture
	validator  *luna.SchemaValidator
	normalizer *FixtureLuna
	ingestion  *ingestion.Service
}

// New composes the P03/P05/P06 implementations. The caller retains ownership
// of Store.Close so an explicit database path can survive a process restart.
func New(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("store is required")
	}
	if strings.TrimSpace(config.SchemaDir) == "" || strings.TrimSpace(config.FixtureDir) == "" {
		return nil, errors.New("schema and fixture directories are required")
	}
	fixture, err := LoadFixture(config.FixtureDir)
	if err != nil {
		return nil, err
	}
	validator, err := luna.LoadSchemaValidator(config.SchemaDir)
	if err != nil {
		return nil, err
	}
	normalizer, err := NewFixtureLuna(fixture)
	if err != nil {
		return nil, err
	}
	projector, err := state.NewProjector(config.Store, config.Store, config.Store)
	if err != nil {
		return nil, err
	}
	dispatcher := canonicalDispatcher{canonical: config.Store, projector: projector}
	service, err := ingestion.New(ingestion.Config{
		RawEvents:       config.Store,
		CanonicalEvents: config.Store,
		Records:         config.Store,
		Transactions:    config.Store,
		Luna:            normalizer,
		Dispatcher:      dispatcher,
		Validator:       validator,
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		store:      config.Store,
		fixture:    fixture,
		validator:  validator,
		normalizer: normalizer,
		ingestion:  service,
	}, nil
}

type canonicalDispatcher struct {
	canonical contracts.CanonicalEventRepository
	projector contracts.Projector
}

var _ contracts.ProjectorDispatcher = canonicalDispatcher{}

// DispatchCanonicalEvent resolves the just-committed durable record before
// applying it. It is intentionally called by P05 only after its canonical
// transaction commits.
func (d canonicalDispatcher) DispatchCanonicalEvent(ctx context.Context, canonicalEventID string) error {
	events, err := d.canonical.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		return fmt.Errorf("list canonical events for dispatch: %w", err)
	}
	for _, event := range events {
		if event.CanonicalEventID == canonicalEventID {
			if _, err := d.projector.ApplyCanonicalEvent(ctx, event); err != nil {
				return fmt.Errorf("project canonical event %q: %w", canonicalEventID, err)
			}
			return nil
		}
	}
	return fmt.Errorf("committed canonical event %q was not found", canonicalEventID)
}

// TimelineEntry is the structured result after one declared scenario beat.
type TimelineEntry struct {
	Beat             Beat           `json:"beat"`
	RawEventID       string         `json:"raw_event_id"`
	LifecycleStatus  string         `json:"lifecycle_status"`
	Duplicate        bool           `json:"duplicate"`
	LunaResultID     string         `json:"luna_result_id,omitempty"`
	CanonicalEventID string         `json:"canonical_event_id,omitempty"`
	StateRevision    int64          `json:"state_revision"`
	COP              map[string]any `json:"cop"`
}

// RunResult is a replayable, JSON-ready scenario execution result.
type RunResult struct {
	ScenarioID    string          `json:"scenario_id"`
	Timeline      []TimelineEntry `json:"timeline"`
	StateRevision int64           `json:"state_revision"`
	COP           map[string]any  `json:"cop"`
	Verification  Verification    `json:"verification"`
}

// Ingest delivers one raw event through the composed P05 lifecycle. It is
// public for CLI and focused idempotency checks; normal demo operation uses
// Run so events always follow declared beat order.
func (s *Service) Ingest(ctx context.Context, raw gen.RawEvent) (ingestion.Outcome, error) {
	return s.ingestion.Ingest(ctx, raw)
}

// Run publishes one structured timeline entry after every declared beat, then
// verifies the persisted expected outcome and checkpoint recovery boundary.
func (s *Service) Run(ctx context.Context) (RunResult, error) {
	result := RunResult{ScenarioID: s.fixture.ScenarioID, Timeline: make([]TimelineEntry, 0, len(s.fixture.Beats))}
	for _, beat := range s.fixture.Beats {
		raw, exists := s.fixture.RawEvent(beat.RawEventID)
		if !exists {
			return RunResult{}, fmt.Errorf("fixture beat %q is missing raw event %q", beat.BeatID, beat.RawEventID)
		}
		outcome, err := s.Ingest(ctx, raw)
		if err != nil {
			return RunResult{}, fmt.Errorf("ingest beat %q: %w", beat.BeatID, err)
		}
		if outcome.DispatchError != nil {
			return RunResult{}, fmt.Errorf("dispatch beat %q: %w", beat.BeatID, outcome.DispatchError)
		}
		projected, err := s.Recover(ctx)
		if err != nil {
			return RunResult{}, fmt.Errorf("recover after beat %q: %w", beat.BeatID, err)
		}
		result.Timeline = append(result.Timeline, TimelineEntry{
			Beat:             beat,
			RawEventID:       outcome.RawEventID,
			LifecycleStatus:  outcome.Status,
			Duplicate:        outcome.Duplicate,
			LunaResultID:     outcome.LunaResultID,
			CanonicalEventID: outcome.CanonicalEventID,
			StateRevision:    projected.StateRevision,
			COP:              cloneCOP(projected.COP),
		})
		result.StateRevision = projected.StateRevision
		result.COP = cloneCOP(projected.COP)
	}
	verification, err := s.Verify(ctx)
	if err != nil {
		return RunResult{}, err
	}
	result.Verification = verification
	return result, nil
}

// Recover creates a fresh P06 projector and rebuilds the COP from the latest
// P03 checkpoint plus later canonical events. This models a process restart.
func (s *Service) Recover(ctx context.Context) (contracts.ProjectionResult, error) {
	projector, err := state.NewProjector(s.store, s.store, s.store)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	return (replay.Runner{Canonical: s.store, Checkpoints: s.store, Projector: projector}).Recover(ctx)
}

// Verification records the expected-outcome proofs P07 can make without
// implementing later Terra or Sol parcels.
type Verification struct {
	RawEventsRetained                bool  `json:"raw_events_retained"`
	LunaLifecyclesMatch              bool  `json:"luna_lifecycles_match"`
	ModelRunProvenanceValid          bool  `json:"model_run_provenance_valid"`
	CanonicalTimelineMatch           bool  `json:"canonical_timeline_match"`
	LateDeliverySequence             int64 `json:"late_delivery_sequence"`
	RoadCorrectionApplied            bool  `json:"road_correction_applied"`
	TerraObsolescenceFixtureExpected bool  `json:"terra_obsolescence_fixture_expected"`
	CheckpointRecoveryMatches        bool  `json:"checkpoint_recovery_matches"`
}

// Verify inspects the durable P03 artifacts, independently recovers state,
// and checks every P04 behavior that belongs to the P07 execution spine.
func (s *Service) Verify(ctx context.Context) (Verification, error) {
	verification := Verification{}
	for rawID, expected := range s.fixture.rawByID {
		stored, err := s.store.FindRawEvent(ctx, rawID)
		if err != nil {
			return verification, fmt.Errorf("raw retention %q: %w", rawID, err)
		}
		if !reflect.DeepEqual(stored, expected) {
			return verification, fmt.Errorf("raw retention %q differs from fixture", rawID)
		}
	}
	verification.RawEventsRetained = true

	if err := s.verifyStoredLuna(ctx); err != nil {
		return verification, err
	}
	verification.LunaLifecyclesMatch = true
	if err := s.verifyModelRuns(ctx); err != nil {
		return verification, err
	}
	verification.ModelRunProvenanceValid = true

	events, err := s.store.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		return verification, err
	}
	if err := s.verifyCanonicalTimeline(events); err != nil {
		return verification, err
	}
	verification.CanonicalTimelineMatch = true
	for _, event := range events {
		if event.RawEventID == "raw-domestic-009-late-ems" {
			verification.LateDeliverySequence = event.CanonicalSeq
		}
	}
	if verification.LateDeliverySequence != 8 {
		return verification, fmt.Errorf("late delivery sequence = %d, want 8", verification.LateDeliverySequence)
	}

	recovered, err := s.Recover(ctx)
	if err != nil {
		return verification, fmt.Errorf("recover checkpoint: %w", err)
	}
	freshProjector, err := state.NewProjector(s.store, s.store, s.store)
	if err != nil {
		return verification, err
	}
	fresh, err := freshProjector.Replay(ctx, gen.Checkpoint{}, events)
	if err != nil {
		return verification, fmt.Errorf("fresh replay: %w", err)
	}
	if !sameCOP(recovered.COP, fresh.COP) || recovered.StateRevision != fresh.StateRevision {
		return verification, errors.New("checkpoint recovery does not match a fresh replay")
	}
	verification.CheckpointRecoveryMatches = true

	if !roadStatus(recovered.COP, "road-brook-lane", "open") {
		return verification, errors.New("Brook Lane correction was not applied")
	}
	verification.RoadCorrectionApplied = true
	if err := s.verifyTerraFixtureExpectation(); err != nil {
		return verification, err
	}
	verification.TerraObsolescenceFixtureExpected = true
	return verification, nil
}

func (s *Service) verifyStoredLuna(ctx context.Context) error {
	rows, err := s.store.SQLDB().QueryContext(ctx, "SELECT record_json FROM luna_results")
	if err != nil {
		return fmt.Errorf("read persisted Luna results: %w", err)
	}
	defer rows.Close()
	results := map[string]gen.LunaResult{}
	for rows.Next() {
		var record string
		if err := rows.Scan(&record); err != nil {
			return err
		}
		var result gen.LunaResult
		if err := json.Unmarshal([]byte(record), &result); err != nil {
			return fmt.Errorf("decode persisted Luna result: %w", err)
		}
		results[result.RawEventID] = result
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(results) != len(s.fixture.outputByRaw) {
		return fmt.Errorf("persisted Luna result count = %d, want %d", len(results), len(s.fixture.outputByRaw))
	}
	for rawID, expected := range s.fixture.outputByRaw {
		actual, exists := results[rawID]
		if !exists || actual.LunaResultID != expected.result.LunaResultID || actual.Status != expected.result.Status || actual.CanonicalEventID != expected.result.CanonicalEventID {
			return fmt.Errorf("persisted Luna result for raw event %q does not match fixture", rawID)
		}
	}
	return nil
}

func (s *Service) verifyModelRuns(ctx context.Context) error {
	rows, err := s.store.SQLDB().QueryContext(ctx, "SELECT record_json FROM model_runs")
	if err != nil {
		return fmt.Errorf("read model runs: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var record string
		if err := rows.Scan(&record); err != nil {
			return err
		}
		var run gen.ModelRun
		if err := json.Unmarshal([]byte(record), &run); err != nil {
			return fmt.Errorf("decode model run: %w", err)
		}
		if err := s.validator.ValidateModelRun(run); err != nil {
			return fmt.Errorf("validate persisted model run %q: %w", run.ModelRunID, err)
		}
		if run.Agent != "luna" || run.Provider != "mosaic-fixture" || run.Model != fixtureModel || run.ValidationStatus != "valid" {
			return fmt.Errorf("persisted model run %q has unexpected fixture provenance", run.ModelRunID)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(s.fixture.outputByRaw) {
		return fmt.Errorf("persisted model run count = %d, want %d", count, len(s.fixture.outputByRaw))
	}
	return nil
}

func (s *Service) verifyCanonicalTimeline(events []gen.CanonicalEvent) error {
	expected := make([]gen.CanonicalEvent, 0, len(s.fixture.outputByRaw))
	for _, output := range s.fixture.outputByRaw {
		if output.canonical != nil {
			expected = append(expected, *output.canonical)
		}
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].CanonicalSeq < expected[j].CanonicalSeq })
	if len(events) != len(expected) {
		return fmt.Errorf("canonical event count = %d, want %d", len(events), len(expected))
	}
	for index, actual := range events {
		want := expected[index]
		if actual.CanonicalSeq != int64(index+1) || actual.CanonicalEventID != want.CanonicalEventID || actual.RawEventID != want.RawEventID || actual.SupersedesEventID != want.SupersedesEventID {
			return fmt.Errorf("canonical timeline entry %d does not match fixture", index+1)
		}
	}
	return nil
}

func (s *Service) verifyTerraFixtureExpectation() error {
	expectation := s.fixture.Expectation.TerraObsolescence
	active, activeExists := s.fixture.insights[expectation.ActiveInsightID]
	obsolete, obsoleteExists := s.fixture.insights[expectation.ObsoleteInsightID]
	if expectation.AfterCanonicalEventID == "" || !activeExists || !obsoleteExists || active.LifecycleStatus != "active" || obsolete.LifecycleStatus != "obsolete" || obsolete.SupersedesInsightID != active.InsightID {
		return errors.New("fixture Terra obsolescence expectation is invalid")
	}
	if _, exists := s.fixture.canonicalIDs[expectation.AfterCanonicalEventID]; !exists {
		return errors.New("fixture Terra obsolescence references an unknown correction")
	}
	return nil
}

func roadStatus(cop map[string]any, roadID, status string) bool {
	roads, _ := cop["roads"].([]any)
	for _, value := range roads {
		road, _ := value.(map[string]any)
		if road["road_id"] == roadID && road["status"] == status {
			return true
		}
	}
	return false
}

func sameCOP(left, right map[string]any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func cloneCOP(cop map[string]any) map[string]any {
	encoded, err := json.Marshal(cop)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil
	}
	return clone
}

// NormalizerCalls returns the number of non-duplicate FixtureLuna invocations.
func (s *Service) NormalizerCalls() int {
	return s.normalizer.Calls()
}

// DefaultDBPath keeps the interactive demo database outside the repository.
func DefaultDBPath() string {
	return filepath.Join(os.TempDir(), "mosaic-v0.1-demo.db")
}

// RepositoryRoot walks upward from start until it finds Mosaic's immutable
// project markers. It lets the CLI locate schemas and fixtures from any child.
func RepositoryRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if isDirectory(filepath.Join(dir, "ontology")) && isFile(filepath.Join(dir, "AGENTS.md")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("Mosaic repository root was not found from %q", start)
		}
		dir = parent
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
