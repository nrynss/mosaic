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
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ingestion"
	"mosaic.local/mosaic/internal/luna"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/state"
	"mosaic.local/mosaic/internal/replay"
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

// SolRequestExpectation is the fixture's supervisor briefing-request identity.
type SolRequestExpectation struct {
	AuditRecordID    string `json:"audit_record_id"`
	BriefingID       string `json:"briefing_id"`
	RecommendationID string `json:"recommendation_id"`
	RequestedBy      string `json:"requested_by"`
}

// SupervisorActionExpectation is the fixture's non-operational acknowledgement.
type SupervisorActionExpectation struct {
	AuditRecordID    string `json:"audit_record_id"`
	RecommendationID string `json:"recommendation_id"`
	ActorID          string `json:"actor_id"`
	Action           string `json:"action"`
}

// FixtureExpectation captures the fixture checks that P07 can verify without
// taking ownership of Terra or Sol artifacts, plus the advisory identities
// required by the P24 fixture-advisory replay.
type FixtureExpectation struct {
	TerraObsolescence TerraObsolescenceExpectation `json:"terra_obsolescence"`
	SolRequest        SolRequestExpectation        `json:"sol_request"`
	SupervisorAction  SupervisorActionExpectation  `json:"supervisor_action"`
}

// Fixture is the decoded, immutable domestic-disturbance test scenario.
// Maps are private so callers cannot accidentally edit the deterministic
// records used by FixtureLuna and fixture advisory replay.
type Fixture struct {
	ScenarioID      string
	Beats           []Beat
	Expectation     FixtureExpectation
	rawByID         map[string]gen.RawEvent
	outputByRaw     map[string]fixtureOutput
	canonicalIDs    map[string]struct{}
	insights        map[string]gen.Insight
	recommendations map[string]gen.Recommendation
	auditRecords    map[string]gen.AuditRecord
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
	SolRequest        SolRequestExpectation        `json:"sol_request"`
	SupervisorAction  SupervisorActionExpectation  `json:"supervisor_action"`
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
		ScenarioID: scenario.ScenarioID,
		Beats:      append([]Beat(nil), scenario.Beats...),
		Expectation: FixtureExpectation{
			TerraObsolescence: outcomes.Checks.TerraObsolescence,
			SolRequest:        outcomes.Checks.SolRequest,
			SupervisorAction:  outcomes.Checks.SupervisorAction,
		},
		rawByID:         make(map[string]gen.RawEvent, len(rawDocument.RawEvents)),
		outputByRaw:     make(map[string]fixtureOutput, len(outcomes.LunaResults)),
		canonicalIDs:    make(map[string]struct{}, len(outcomes.CanonicalEvents)),
		insights:        make(map[string]gen.Insight, len(outcomes.Insights)),
		recommendations: make(map[string]gen.Recommendation, len(outcomes.Recommendations)),
		auditRecords:    make(map[string]gen.AuditRecord, len(outcomes.AuditRecords)),
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
	for _, recommendation := range outcomes.Recommendations {
		if recommendation.RecommendationID == "" {
			return nil, errors.New("fixture recommendation is missing recommendation_id")
		}
		fixture.recommendations[recommendation.RecommendationID] = recommendation
	}
	for _, audit := range outcomes.AuditRecords {
		if audit.AuditRecordID == "" {
			return nil, errors.New("fixture audit record is missing audit_record_id")
		}
		fixture.auditRecords[audit.AuditRecordID] = audit
	}
	if err := validateAdvisoryFixtureExpectation(fixture); err != nil {
		return nil, err
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

// DomainStore is the durable seam the simulator needs. Both *store.Store and
// *pgstore.Store satisfy it so seed, recover, and fixture advisory replay share
// one backend with API records/advisories (no split-brain dual store).
type DomainStore interface {
	contracts.RawEventRepository
	contracts.CanonicalEventRepository
	contracts.ImmutableRecordRepository
	contracts.AdvisoryHistoryReader
	contracts.CheckpointRepository
	contracts.TransactionRunner
	// ListLunaResults / ListModelRuns support deterministic Verify without
	// backend-specific SQLDB access (ReadAdvisoryHistory omits Luna runs).
	ListLunaResults(ctx context.Context) ([]gen.LunaResult, error)
	ListModelRuns(ctx context.Context) ([]gen.ModelRun, error)
}

// Config supplies the already-open durable store and the checked-in P02/P04
// inputs needed to compose the deterministic simulator.
type Config struct {
	Store      DomainStore
	SchemaDir  string
	FixtureDir string
	// WrapProjector optionally decorates the domain projector after construction
	// (e.g. pgstore.MaterializingProjector so COP materialization stays warm).
	WrapProjector func(contracts.Projector) contracts.Projector
}

// Service is the executable v0.1 spine: fixture source, P05 ingestion, P06
// projection, and P06 replay recovery over a durable DomainStore backend.
type Service struct {
	store         DomainStore
	fixture       *Fixture
	validator     *luna.SchemaValidator
	normalizer    *FixtureLuna
	ingestion     *ingestion.Service
	wrapProjector func(contracts.Projector) contracts.Projector
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
	var asProjector contracts.Projector = projector
	if config.WrapProjector != nil {
		asProjector = config.WrapProjector(asProjector)
	}
	dispatcher := canonicalDispatcher{canonical: config.Store, projector: asProjector}
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
		store:         config.Store,
		fixture:       fixture,
		validator:     validator,
		normalizer:    normalizer,
		ingestion:     service,
		wrapProjector: config.WrapProjector,
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

// IngestBeat looks up one declared fixture beat by id, delivers its raw event
// through P05, fails closed on dispatch errors, and returns the full-store
// recovered COP snapshot after that beat. Used by bulk Run. The interactive
// progressive path uses DeliverBeat + ProgressiveCOPFromBeatIDs instead so a
// second Play on a durable store does not jump the session board to final rev.
func (s *Service) IngestBeat(ctx context.Context, beatID string) (TimelineEntry, error) {
	entry, err := s.DeliverBeat(ctx, beatID)
	if err != nil {
		return TimelineEntry{}, err
	}
	projected, err := s.Recover(ctx)
	if err != nil {
		return TimelineEntry{}, fmt.Errorf("recover after beat %q: %w", beatID, err)
	}
	entry.StateRevision = projected.StateRevision
	entry.COP = cloneCOP(projected.COP)
	return entry, nil
}

// DeliverBeat looks up one declared fixture beat by id and delivers its raw
// event through P05 without full-store Recover. StateRevision and COP are left
// zero; callers that need a session-progressive board must follow with
// ProgressiveCOPFromBeatIDs over the beats processed this session.
func (s *Service) DeliverBeat(ctx context.Context, beatID string) (TimelineEntry, error) {
	if s == nil || s.fixture == nil {
		return TimelineEntry{}, errors.New("simulator is not configured")
	}
	beatID = strings.TrimSpace(beatID)
	if beatID == "" {
		return TimelineEntry{}, errors.New("beat id is required")
	}
	var beat Beat
	found := false
	for _, candidate := range s.fixture.Beats {
		if candidate.BeatID == beatID {
			beat = candidate
			found = true
			break
		}
	}
	if !found {
		return TimelineEntry{}, fmt.Errorf("unknown fixture beat %q", beatID)
	}
	raw, exists := s.fixture.RawEvent(beat.RawEventID)
	if !exists {
		return TimelineEntry{}, fmt.Errorf("fixture beat %q is missing raw event %q", beat.BeatID, beat.RawEventID)
	}
	outcome, err := s.Ingest(ctx, raw)
	if err != nil {
		return TimelineEntry{}, fmt.Errorf("ingest beat %q: %w", beat.BeatID, err)
	}
	if outcome.DispatchError != nil {
		return TimelineEntry{}, fmt.Errorf("dispatch beat %q: %w", beat.BeatID, outcome.DispatchError)
	}
	// On first delivery, CanonicalEventID comes from the ingest outcome. On
	// exact P05 duplicates the outcome omits it; resolve from the fixture map
	// so progressive session projection can still select durable events.
	canonicalID := outcome.CanonicalEventID
	if canonicalID == "" {
		if out, ok := s.fixture.outputByRaw[beat.RawEventID]; ok && out.canonical != nil {
			canonicalID = out.canonical.CanonicalEventID
		}
	}
	return TimelineEntry{
		Beat:             beat,
		RawEventID:       outcome.RawEventID,
		LifecycleStatus:  outcome.Status,
		Duplicate:        outcome.Duplicate,
		LunaResultID:     outcome.LunaResultID,
		CanonicalEventID: canonicalID,
	}, nil
}

// ProgressiveCOPFromBeatIDs rebuilds a session-progressive COP by replaying only
// the durable canonical events that correspond to the given fixture beat IDs
// (in durable sequence order). Result is materialised via WrapProjector when
// configured (session-scoped COP key). Empty beat sets or only non-projectable
// beats yield a zero ProjectionResult without materialising.
//
// This is the progressive board source for interactive Play: full-store Recover
// is NOT used, so a second session on an already-seeded durable log still
// advances revision 1→9 across beats instead of jumping to final rev on beat 1.
func (s *Service) ProgressiveCOPFromBeatIDs(ctx context.Context, beatIDs []string) (contracts.ProjectionResult, error) {
	if s == nil || s.fixture == nil {
		return contracts.ProjectionResult{}, errors.New("simulator is not configured")
	}
	want := make(map[string]struct{}, len(beatIDs))
	for _, beatID := range beatIDs {
		beatID = strings.TrimSpace(beatID)
		if beatID == "" {
			continue
		}
		for _, candidate := range s.fixture.Beats {
			if candidate.BeatID != beatID {
				continue
			}
			if out, ok := s.fixture.outputByRaw[candidate.RawEventID]; ok && out.canonical != nil {
				id := strings.TrimSpace(out.canonical.CanonicalEventID)
				if id != "" {
					want[id] = struct{}{}
				}
			}
			break
		}
	}
	if len(want) == 0 {
		return contracts.ProjectionResult{}, nil
	}
	all, err := s.store.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("list canonical events for progressive COP: %w", err)
	}
	filtered := make([]gen.CanonicalEvent, 0, len(want))
	for _, event := range all {
		if _, ok := want[event.CanonicalEventID]; ok {
			filtered = append(filtered, event)
		}
	}
	if len(filtered) == 0 {
		// Beats resolved to fixture canonical ids that are not yet durable
		// (should not happen after DeliverBeat on a known fixture).
		return contracts.ProjectionResult{}, nil
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].CanonicalSeq < filtered[j].CanonicalSeq
	})

	projector, err := state.NewProjector(s.store, s.store, s.store)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	var asProjector contracts.Projector = projector
	if s.wrapProjector != nil {
		asProjector = s.wrapProjector(asProjector)
	}
	return asProjector.Replay(ctx, gen.Checkpoint{}, filtered)
}

// RawEventPayload returns the JSON-serialized fixture raw event for Append to
// the EventLog seam. Composition loads payloads by raw_event_id without reading
// domain store rows that may not exist yet.
func (s *Service) RawEventPayload(rawEventID string) ([]byte, error) {
	if s == nil || s.fixture == nil {
		return nil, errors.New("simulator is not configured")
	}
	raw, exists := s.fixture.RawEvent(strings.TrimSpace(rawEventID))
	if !exists {
		return nil, fmt.Errorf("unknown raw event %q", rawEventID)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode raw event %q: %w", rawEventID, err)
	}
	return encoded, nil
}

// Run publishes one structured timeline entry after every declared beat, then
// verifies the persisted expected outcome and checkpoint recovery boundary.
func (s *Service) Run(ctx context.Context) (RunResult, error) {
	result := RunResult{ScenarioID: s.fixture.ScenarioID, Timeline: make([]TimelineEntry, 0, len(s.fixture.Beats))}
	for _, beat := range s.fixture.Beats {
		entry, err := s.IngestBeat(ctx, beat.BeatID)
		if err != nil {
			return RunResult{}, err
		}
		result.Timeline = append(result.Timeline, entry)
		result.StateRevision = entry.StateRevision
		result.COP = cloneCOP(entry.COP)
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
	var asProjector contracts.Projector = projector
	if s.wrapProjector != nil {
		asProjector = s.wrapProjector(asProjector)
	}
	return (replay.Runner{Canonical: s.store, Checkpoints: s.store, Projector: asProjector}).Recover(ctx)
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
	// Compare bare domain replay (no materialization wrap) so COP equality is
	// independent of read-model side effects.
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
	listed, err := s.store.ListLunaResults(ctx)
	if err != nil {
		return fmt.Errorf("read persisted Luna results: %w", err)
	}
	results := map[string]gen.LunaResult{}
	for _, result := range listed {
		results[result.RawEventID] = result
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
	runs, err := s.store.ListModelRuns(ctx)
	if err != nil {
		return fmt.Errorf("read model runs: %w", err)
	}
	lunaCount := 0
	for _, run := range runs {
		if err := s.validator.ValidateModelRun(run); err != nil {
			return fmt.Errorf("validate persisted model run %q: %w", run.ModelRunID, err)
		}
		switch run.Agent {
		case "luna":
			if run.Provider != "mosaic-fixture" || run.Model != fixtureModel || run.ValidationStatus != "valid" {
				return fmt.Errorf("persisted model run %q has unexpected fixture provenance", run.ModelRunID)
			}
			lunaCount++
		case "terra", "sol":
			// Terra/Sol Model Runs are owned by the fixture-advisory replay path.
		default:
			return fmt.Errorf("persisted model run %q has unexpected agent %q", run.ModelRunID, run.Agent)
		}
	}
	if lunaCount != len(s.fixture.outputByRaw) {
		return fmt.Errorf("persisted Luna model run count = %d, want %d", lunaCount, len(s.fixture.outputByRaw))
	}
	return nil
}

func validateAdvisoryFixtureExpectation(fixture *Fixture) error {
	expectation := fixture.Expectation
	active, activeOK := fixture.insights[expectation.TerraObsolescence.ActiveInsightID]
	obsolete, obsoleteOK := fixture.insights[expectation.TerraObsolescence.ObsoleteInsightID]
	if !activeOK || !obsoleteOK {
		return errors.New("fixture Terra obsolescence references missing Insights")
	}
	if active.LifecycleStatus != "active" || active.StateRevision != 7 {
		return fmt.Errorf("fixture active Insight %q is not the rev-7 active assessment", active.InsightID)
	}
	if obsolete.LifecycleStatus != "obsolete" || obsolete.StateRevision != 9 || obsolete.SupersedesInsightID != active.InsightID {
		return fmt.Errorf("fixture obsolete Insight %q is not the rev-9 superseding assessment", obsolete.InsightID)
	}
	recommendation, recommendationOK := fixture.recommendations[expectation.SolRequest.RecommendationID]
	if !recommendationOK || recommendation.StateRevision != 7 {
		return errors.New("fixture Sol request references a missing rev-7 Recommendation")
	}
	if expectation.SolRequest.RequestedBy == "" || expectation.SolRequest.BriefingID == "" {
		return errors.New("fixture Sol request requires requested_by and briefing_id")
	}
	if _, ok := fixture.auditRecords[expectation.SolRequest.AuditRecordID]; !ok {
		return fmt.Errorf("fixture Sol request audit %q is missing", expectation.SolRequest.AuditRecordID)
	}
	if expectation.SupervisorAction.RecommendationID != recommendation.RecommendationID {
		return errors.New("fixture supervisor action targets a different Recommendation")
	}
	if _, ok := fixture.auditRecords[expectation.SupervisorAction.AuditRecordID]; !ok {
		return fmt.Errorf("fixture supervisor audit %q is missing", expectation.SupervisorAction.AuditRecordID)
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

// Beats returns the scheduled beats for the simulation.
func (s *Service) Beats() []contracts.ScheduledBeat {
	scheduledBeats := make([]contracts.ScheduledBeat, len(s.fixture.Beats))
	for i, beat := range s.fixture.Beats {
		scheduledBeats[i] = contracts.ScheduledBeat{
			BeatID:     beat.BeatID,
			Order:      beat.Order,
			RawEventID: beat.RawEventID,
			Delay:      time.Duration(beat.DelayMS) * time.Millisecond,
		}
	}
	return scheduledBeats
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
