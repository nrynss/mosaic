package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/terra"
)

const (
	fixtureTerraModel         = "mosaic-fixture-terra-v1"
	fixtureSolModel           = "mosaic-fixture-sol-v1"
	fixtureAdvisoryProvider   = "mosaic-fixture"
	fixtureAdvisoryPrompt     = "domestic-disturbance-fixture-v1"
	fixtureTerraActiveRunID   = "modelrun-fixture-terra-insight-domestic-access-001"
	fixtureSolRunID           = "modelrun-fixture-sol-recommendation-domestic-001"
	fixtureTerraObsoleteRunID = "modelrun-fixture-terra-insight-domestic-access-001-obsolete"
	fixtureTerraActiveRespID  = "fixture-terra-rev7"
	fixtureSolRespID          = "fixture-sol-rev7"
	fixtureTerraObsoleteResp  = "fixture-terra-rev9"
)

var (
	// ErrPartialAdvisoryStage means a fixture advisory stage is incomplete in
	// durable storage. The sequence must stop without rewriting history.
	ErrPartialAdvisoryStage = errors.New("partial fixture advisory stage")

	// ErrAdvisoryTimeline means the scenario timeline does not supply the
	// historical COP revisions required by the fixture advisory sequence.
	ErrAdvisoryTimeline = errors.New("fixture advisory timeline is incomplete")
)

// AdvisoryReplayConfig wires the fixture-only Terra/Sol composition used by
// the local demo. Optional client overrides exist only for focused tests of
// refusal and invalid-response paths.
type AdvisoryReplayConfig struct {
	Store       *store.Store
	SchemaDir   string
	FixtureDir  string
	TerraClient terra.StructuredClient
	SolClient   sol.StructuredClient
}

// AdvisoryReplayResult reports which fixture advisory stages executed.
type AdvisoryReplayResult struct {
	ScenarioID    string   `json:"scenario_id"`
	StagesRun     []string `json:"stages_run"`
	StagesSkipped []string `json:"stages_skipped"`
	IntactRestart bool     `json:"intact_restart"`
}

// AdvisoryReplay replays the frozen Terra/Sol advisory lifecycle against an
// already-projected scenario timeline. It is not a projector and cannot issue
// an operational action or open a network/model transport.
type AdvisoryReplay struct {
	store       *store.Store
	records     *advisoryRecords
	fixture     *Fixture
	schemaDir   string
	terraClient terra.StructuredClient
	solClient   sol.StructuredClient
	resolver    *durableEvidenceResolver
}

// NewAdvisoryReplay loads the P04 fixture and prepares the fixture-only
// Terra/Sol composition. Callers retain ownership of Store.Close.
func NewAdvisoryReplay(config AdvisoryReplayConfig) (*AdvisoryReplay, error) {
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
	records := &advisoryRecords{store: config.Store}
	return &AdvisoryReplay{
		store:       config.Store,
		records:     records,
		fixture:     fixture,
		schemaDir:   config.SchemaDir,
		terraClient: config.TerraClient,
		solClient:   config.SolClient,
		resolver:    &durableEvidenceResolver{store: config.Store},
	}, nil
}

// Replay applies the five-stage fixture advisory sequence using only the
// historical rev-7 and rev-9 COP snapshots from the scenario timeline.
// An intact sequence is a no-op; a partial stage is an integrity error.
func (r *AdvisoryReplay) Replay(ctx context.Context, timeline []TimelineEntry) (AdvisoryReplayResult, error) {
	if r == nil || r.store == nil || r.fixture == nil {
		return AdvisoryReplayResult{}, errors.New("advisory replay is not configured")
	}
	result := AdvisoryReplayResult{
		ScenarioID:    r.fixture.ScenarioID,
		StagesRun:     make([]string, 0, 5),
		StagesSkipped: make([]string, 0, 5),
	}

	cop7, err := copAtRevision(timeline, 7)
	if err != nil {
		return result, err
	}
	cop9, err := copAtRevision(timeline, 9)
	if err != nil {
		return result, err
	}

	history, err := r.store.ReadAdvisoryHistory(ctx)
	if err != nil {
		return result, fmt.Errorf("read advisory history: %w", err)
	}
	statuses, err := r.classifyStages(history)
	if err != nil {
		return result, err
	}
	if err := validateStagePrefix(statuses); err != nil {
		return result, err
	}

	active, obsolete, recommendation, briefingAudit, ackAudit, err := r.fixtureArtifacts()
	if err != nil {
		return result, err
	}

	stageNames := []string{
		"terra_active_rev7",
		"briefing_requested",
		"sol_recommendation_rev7",
		"terra_obsolete_rev9",
		"recommendation_acknowledged",
	}
	for index, status := range statuses {
		if status == stageIntact {
			result.StagesSkipped = append(result.StagesSkipped, stageNames[index])
		}
	}
	if allIntact(statuses) {
		result.IntactRestart = true
		return result, nil
	}

	if statuses[0] == stageAbsent {
		if err := r.runTerraActive(ctx, cop7, active); err != nil {
			return result, err
		}
		result.StagesRun = append(result.StagesRun, stageNames[0])
	}
	if statuses[1] == stageAbsent {
		if err := r.appendAudit(ctx, briefingAudit); err != nil {
			return result, err
		}
		result.StagesRun = append(result.StagesRun, stageNames[1])
	}
	if statuses[2] == stageAbsent {
		if err := r.runSolRecommendation(ctx, cop7, active, recommendation); err != nil {
			return result, err
		}
		result.StagesRun = append(result.StagesRun, stageNames[2])
	}
	if statuses[3] == stageAbsent {
		if err := r.runTerraObsolete(ctx, cop9, active, obsolete); err != nil {
			return result, err
		}
		result.StagesRun = append(result.StagesRun, stageNames[3])
	}
	if statuses[4] == stageAbsent {
		if err := r.appendAudit(ctx, ackAudit); err != nil {
			return result, err
		}
		result.StagesRun = append(result.StagesRun, stageNames[4])
	}
	return result, nil
}

type stageStatus int

const (
	stageAbsent stageStatus = iota
	stageIntact
	stagePartial
)

func (r *AdvisoryReplay) classifyStages(history contracts.AdvisoryHistory) ([5]stageStatus, error) {
	var statuses [5]stageStatus
	activeID := r.fixture.Expectation.TerraObsolescence.ActiveInsightID
	obsoleteID := r.fixture.Expectation.TerraObsolescence.ObsoleteInsightID
	recommendationID := r.fixture.Expectation.SolRequest.RecommendationID
	briefingID := r.fixture.Expectation.SolRequest.AuditRecordID
	ackID := r.fixture.Expectation.SupervisorAction.AuditRecordID

	statuses[0] = classifyPair(
		hasInsight(history, activeID),
		hasValidModelRun(history, fixtureTerraActiveRunID, "terra", activeID),
		hasModelRun(history, fixtureTerraActiveRunID),
	)
	statuses[1] = classifySingle(hasAudit(history, briefingID))
	statuses[2] = classifyPair(
		hasRecommendation(history, recommendationID),
		hasValidModelRun(history, fixtureSolRunID, "sol", recommendationID),
		hasModelRun(history, fixtureSolRunID),
	)
	statuses[3] = classifyPair(
		hasInsight(history, obsoleteID),
		hasValidModelRun(history, fixtureTerraObsoleteRunID, "terra", obsoleteID),
		hasModelRun(history, fixtureTerraObsoleteRunID),
	)
	statuses[4] = classifySingle(hasAudit(history, ackID))
	return statuses, nil
}

func classifyPair(hasOutput, hasValidPair, hasAnyRun bool) stageStatus {
	switch {
	case hasOutput && hasValidPair:
		return stageIntact
	case !hasOutput && !hasAnyRun:
		return stageAbsent
	default:
		return stagePartial
	}
}

func classifySingle(present bool) stageStatus {
	if present {
		return stageIntact
	}
	return stageAbsent
}

func validateStagePrefix(statuses [5]stageStatus) error {
	for _, status := range statuses {
		if status == stagePartial {
			return fmt.Errorf("%w: a fixture advisory stage is incomplete", ErrPartialAdvisoryStage)
		}
	}
	seenAbsent := false
	for index, status := range statuses {
		if status == stageAbsent {
			seenAbsent = true
			continue
		}
		if seenAbsent && status == stageIntact {
			return fmt.Errorf("%w: stage %d is present after an absent earlier stage", ErrPartialAdvisoryStage, index+1)
		}
	}
	return nil
}

func allIntact(statuses [5]stageStatus) bool {
	for _, status := range statuses {
		if status != stageIntact {
			return false
		}
	}
	return true
}

func (r *AdvisoryReplay) fixtureArtifacts() (gen.Insight, gen.Insight, gen.Recommendation, gen.AuditRecord, gen.AuditRecord, error) {
	active, ok := r.fixture.insights[r.fixture.Expectation.TerraObsolescence.ActiveInsightID]
	if !ok {
		return gen.Insight{}, gen.Insight{}, gen.Recommendation{}, gen.AuditRecord{}, gen.AuditRecord{}, errors.New("fixture active Insight is missing")
	}
	obsolete, ok := r.fixture.insights[r.fixture.Expectation.TerraObsolescence.ObsoleteInsightID]
	if !ok {
		return gen.Insight{}, gen.Insight{}, gen.Recommendation{}, gen.AuditRecord{}, gen.AuditRecord{}, errors.New("fixture obsolete Insight is missing")
	}
	recommendation, ok := r.fixture.recommendations[r.fixture.Expectation.SolRequest.RecommendationID]
	if !ok {
		return gen.Insight{}, gen.Insight{}, gen.Recommendation{}, gen.AuditRecord{}, gen.AuditRecord{}, errors.New("fixture Recommendation is missing")
	}
	briefing, ok := r.fixture.auditRecords[r.fixture.Expectation.SolRequest.AuditRecordID]
	if !ok {
		return gen.Insight{}, gen.Insight{}, gen.Recommendation{}, gen.AuditRecord{}, gen.AuditRecord{}, errors.New("fixture briefing audit is missing")
	}
	ack, ok := r.fixture.auditRecords[r.fixture.Expectation.SupervisorAction.AuditRecordID]
	if !ok {
		return gen.Insight{}, gen.Insight{}, gen.Recommendation{}, gen.AuditRecord{}, gen.AuditRecord{}, errors.New("fixture acknowledgement audit is missing")
	}
	return active, obsolete, recommendation, briefing, ack, nil
}

func (r *AdvisoryReplay) runTerraActive(ctx context.Context, cop map[string]any, insight gen.Insight) error {
	evidence, err := permittedEvidenceFromInsight(insight)
	if err != nil {
		return err
	}
	client := r.terraClient
	if client == nil {
		client = fixtureTerraClient{insight: insight, responseID: fixtureTerraActiveRespID}
	}
	clock, err := parseRFC3339(insight.CreatedAt)
	if err != nil {
		return fmt.Errorf("active Insight clock: %w", err)
	}
	service, err := r.newTerraService(client, nil, fixtureTerraActiveRunID, clock)
	if err != nil {
		return err
	}
	return r.commitTerra(ctx, service, contracts.TerraInput{
		StateRevision: 7,
		COP:           cloneCOP(cop),
		Evidence:      evidence,
	})
}

func (r *AdvisoryReplay) runTerraObsolete(ctx context.Context, cop map[string]any, active, obsolete gen.Insight) error {
	evidence, err := permittedEvidenceFromInsight(obsolete)
	if err != nil {
		return err
	}
	client := r.terraClient
	if client == nil {
		client = fixtureTerraClient{insight: obsolete, responseID: fixtureTerraObsoleteResp}
	}
	clock, err := parseRFC3339(obsolete.CreatedAt)
	if err != nil {
		return fmt.Errorf("obsolete Insight clock: %w", err)
	}
	service, err := r.newTerraService(client, []gen.Insight{active}, fixtureTerraObsoleteRunID, clock)
	if err != nil {
		return err
	}
	return r.commitTerra(ctx, service, contracts.TerraInput{
		StateRevision: 9,
		COP:           cloneCOP(cop),
		Evidence:      evidence,
	})
}

func (r *AdvisoryReplay) runSolRecommendation(ctx context.Context, cop map[string]any, active gen.Insight, recommendation gen.Recommendation) error {
	evidence, err := permittedEvidenceFromRecommendation(recommendation)
	if err != nil {
		return err
	}
	client := r.solClient
	if client == nil {
		client = fixtureSolClient{recommendation: recommendation, responseID: fixtureSolRespID}
	}
	clock, err := parseRFC3339(recommendation.CreatedAt)
	if err != nil {
		return fmt.Errorf("Recommendation clock: %w", err)
	}
	service, err := r.newSolService(client, fixtureSolRunID, clock)
	if err != nil {
		return err
	}
	return r.commitSol(ctx, service, contracts.SolInput{
		StateRevision: 7,
		COP:           cloneCOP(cop),
		Insights:      []gen.Insight{active},
		Evidence:      evidence,
		RequestedBy:   r.fixture.Expectation.SolRequest.RequestedBy,
	})
}

func (r *AdvisoryReplay) appendAudit(ctx context.Context, audit gen.AuditRecord) error {
	return r.store.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := r.store.AppendAuditRecord(txCtx, audit); err != nil {
			return fmt.Errorf("persist fixture audit %q: %w", audit.AuditRecordID, err)
		}
		return nil
	})
}

func (r *AdvisoryReplay) newTerraService(client terra.StructuredClient, existing []gen.Insight, modelRunID string, clock time.Time) (*terra.Service, error) {
	validator, err := terra.LoadSchemaValidator(r.schemaDir)
	if err != nil {
		return nil, err
	}
	return terra.New(terra.Config{
		Client:           client,
		EvidenceResolver: r.resolver,
		Records:          r.records,
		Validator:        validator,
		PromptVersion:    fixtureAdvisoryPrompt,
		Provider:         fixtureAdvisoryProvider,
		Model:            fixtureTerraModel,
		Clock:            func() time.Time { return clock },
		NewModelRunID:    func() string { return modelRunID },
		ExistingInsights: existing,
	})
}

func (r *AdvisoryReplay) newSolService(client sol.StructuredClient, modelRunID string, clock time.Time) (*sol.Service, error) {
	validator, err := sol.LoadSchemaValidator(r.schemaDir)
	if err != nil {
		return nil, err
	}
	return sol.New(sol.Config{
		Client:        client,
		Resolver:      r.resolver,
		Records:       r.records,
		Validator:     validator,
		PromptVersion: fixtureAdvisoryPrompt,
		Provider:      fixtureAdvisoryProvider,
		Model:         fixtureSolModel,
		Clock:         func() time.Time { return clock },
		NewModelRunID: func() string { return modelRunID },
	})
}

// commitTerra runs one Terra assessment inside Store.WithinTransaction so a
// successful Model Run/Insight pair is atomic. Failure and refusal Model Runs
// are committed; a failed valid-pair write rolls back both appends.
func (r *AdvisoryReplay) commitTerra(ctx context.Context, service *terra.Service, input contracts.TerraInput) error {
	var (
		output    contracts.TerraOutput
		assessErr error
	)
	if err := r.store.WithinTransaction(ctx, func(txCtx context.Context) error {
		output, assessErr = service.Assess(txCtx, input)
		return pairTransactionDecision(output.ModelRun, assessErr)
	}); err != nil {
		return err
	}
	return assessErr
}

// commitSol runs one Sol briefing inside Store.WithinTransaction with the same
// commit-or-rollback policy as Terra.
func (r *AdvisoryReplay) commitSol(ctx context.Context, service *sol.Service, input contracts.SolInput) error {
	var (
		output   contracts.SolOutput
		briefErr error
	)
	if err := r.store.WithinTransaction(ctx, func(txCtx context.Context) error {
		output, briefErr = service.Brief(txCtx, input)
		return pairTransactionDecision(output.ModelRun, briefErr)
	}); err != nil {
		return err
	}
	return briefErr
}

// pairTransactionDecision keeps durable non-valid Model Runs (refused,
// invalid, failed, timed_out) while rolling back any incomplete valid pair.
func pairTransactionDecision(run gen.ModelRun, serviceErr error) error {
	if serviceErr == nil {
		return nil
	}
	if run.ModelRunID == "" {
		return serviceErr
	}
	if run.ValidationStatus == "valid" {
		// Model Run was prepared as a successful pair; if the service still
		// returned an error the paired Insight/Recommendation write failed.
		return serviceErr
	}
	// Commit the failure/refusal Model Run, then surface the service error.
	return nil
}

// advisoryRecords is the append seam used by P10/P11 during fixture replay.
// Focused tests can inject Insight/Recommendation write failures to prove
// transactional rollback of the paired Model Run.
type advisoryRecords struct {
	store              *store.Store
	failInsight        error
	failRecommendation error
}

func (r *advisoryRecords) AppendLunaResult(ctx context.Context, result gen.LunaResult) error {
	return r.store.AppendLunaResult(ctx, result)
}

func (r *advisoryRecords) AppendInsight(ctx context.Context, insight gen.Insight) error {
	if r.failInsight != nil {
		return r.failInsight
	}
	return r.store.AppendInsight(ctx, insight)
}

func (r *advisoryRecords) AppendRecommendation(ctx context.Context, recommendation gen.Recommendation) error {
	if r.failRecommendation != nil {
		return r.failRecommendation
	}
	return r.store.AppendRecommendation(ctx, recommendation)
}

func (r *advisoryRecords) AppendModelRun(ctx context.Context, run gen.ModelRun) error {
	return r.store.AppendModelRun(ctx, run)
}

func (r *advisoryRecords) AppendAuditRecord(ctx context.Context, audit gen.AuditRecord) error {
	return r.store.AppendAuditRecord(ctx, audit)
}

var _ contracts.ImmutableRecordRepository = (*advisoryRecords)(nil)

type fixtureTerraClient struct {
	insight    gen.Insight
	responseID string
	refuse     string
	invalid    bool
}

func (c fixtureTerraClient) Assess(_ context.Context, _ terra.Request) (terra.Response, error) {
	if strings.TrimSpace(c.refuse) != "" {
		return terra.Response{ResponseID: c.responseID, RefusalDetail: c.refuse}, nil
	}
	if c.invalid {
		return terra.Response{ResponseID: c.responseID, InsightJSON: json.RawMessage(`{"not":"an-insight"}`)}, nil
	}
	encoded, err := json.Marshal(c.insight)
	if err != nil {
		return terra.Response{}, err
	}
	return terra.Response{InsightJSON: encoded, ResponseID: c.responseID}, nil
}

type fixtureSolClient struct {
	recommendation gen.Recommendation
	responseID     string
	refuse         string
	invalid        bool
}

func (c fixtureSolClient) Brief(_ context.Context, _ sol.Request) (sol.Response, error) {
	if strings.TrimSpace(c.refuse) != "" {
		return sol.Response{ResponseID: c.responseID, RefusalDetail: c.refuse}, nil
	}
	if c.invalid {
		return sol.Response{ResponseID: c.responseID, RecommendationJSON: json.RawMessage(`{"not":"a-recommendation"}`)}, nil
	}
	encoded, err := json.Marshal(c.recommendation)
	if err != nil {
		return sol.Response{}, err
	}
	return sol.Response{RecommendationJSON: encoded, ResponseID: c.responseID}, nil
}

// durableEvidenceResolver confirms only that cited artifacts already exist in
// the append-only store. It uses store methods so evidence reads join an open
// Store.WithinTransaction and never take a second SQLite connection.
// It never reads raw payloads into the model path.
type durableEvidenceResolver struct {
	store *store.Store
}

func (r *durableEvidenceResolver) ResolveEvidence(ctx context.Context, _ int64, evidence []gen.Evidence) error {
	for _, item := range evidence {
		if err := r.requireArtifact(ctx, item.TargetKind, item.TargetID); err != nil {
			return err
		}
	}
	return nil
}

func (r *durableEvidenceResolver) ResolveInsights(ctx context.Context, _ int64, insights []gen.Insight) error {
	for _, insight := range insights {
		if err := r.requireArtifact(ctx, "insight", insight.InsightID); err != nil {
			return err
		}
	}
	return nil
}

func (r *durableEvidenceResolver) requireArtifact(ctx context.Context, kind, id string) error {
	if r == nil || r.store == nil {
		return errors.New("durable evidence resolver is not configured")
	}
	switch kind {
	case "raw_event":
		if _, err := r.store.FindRawEvent(ctx, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("evidence %s/%s is not durable", kind, id)
			}
			return fmt.Errorf("resolve evidence %s/%s: %w", kind, id, err)
		}
		return nil
	case "canonical_event":
		events, err := r.store.ListCanonicalEventsAfter(ctx, 0)
		if err != nil {
			return fmt.Errorf("resolve evidence %s/%s: %w", kind, id, err)
		}
		for _, event := range events {
			if event.CanonicalEventID == id {
				return nil
			}
		}
		return fmt.Errorf("evidence %s/%s is not durable", kind, id)
	case "insight", "recommendation":
		history, err := r.store.ReadAdvisoryHistory(ctx)
		if err != nil {
			return fmt.Errorf("resolve evidence %s/%s: %w", kind, id, err)
		}
		if kind == "insight" {
			for _, insight := range history.Insights {
				if insight.InsightID == id {
					return nil
				}
			}
		} else {
			for _, recommendation := range history.Recommendations {
				if recommendation.RecommendationID == id {
					return nil
				}
			}
		}
		return fmt.Errorf("evidence %s/%s is not durable", kind, id)
	default:
		return fmt.Errorf("unsupported evidence kind %q", kind)
	}
}

func copAtRevision(timeline []TimelineEntry, revision int64) (map[string]any, error) {
	var found map[string]any
	for _, entry := range timeline {
		if entry.StateRevision == revision {
			found = entry.COP
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: missing COP snapshot for revision %d", ErrAdvisoryTimeline, revision)
	}
	return cloneCOP(found), nil
}

func permittedEvidenceFromInsight(insight gen.Insight) ([]gen.Evidence, error) {
	return permittedEvidence(insight.Evidence, insight.CreatedAt, "insight", insight.InsightID)
}

func permittedEvidenceFromRecommendation(recommendation gen.Recommendation) ([]gen.Evidence, error) {
	return permittedEvidence(recommendation.Evidence, recommendation.CreatedAt, "recommendation", recommendation.RecommendationID)
}

func permittedEvidence(refs []any, createdAt, ownerKind, ownerID string) ([]gen.Evidence, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("%s %q has no evidence references", ownerKind, ownerID)
	}
	evidence := make([]gen.Evidence, 0, len(refs))
	for index, value := range refs {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode %s %q evidence %d: %w", ownerKind, ownerID, index, err)
		}
		var reference struct {
			TargetKind  string `json:"target_kind"`
			TargetID    string `json:"target_id"`
			JSONPointer string `json:"json_pointer"`
			Explanation string `json:"explanation"`
		}
		if err := json.Unmarshal(encoded, &reference); err != nil {
			return nil, fmt.Errorf("decode %s %q evidence %d: %w", ownerKind, ownerID, index, err)
		}
		if reference.TargetKind == "" || reference.TargetID == "" || reference.Explanation == "" {
			return nil, fmt.Errorf("%s %q evidence %d is incomplete", ownerKind, ownerID, index)
		}
		item := gen.Evidence{
			SchemaVersion: "1.0.0",
			EvidenceID:    fmt.Sprintf("evidence-fixture-%s-%s", ownerID, reference.TargetID),
			TargetKind:    reference.TargetKind,
			TargetID:      reference.TargetID,
			JsonPointer:   reference.JSONPointer,
			Explanation:   reference.Explanation,
			CreatedAt:     createdAt,
		}
		evidence = append(evidence, item)
	}
	return evidence, nil
}

func hasInsight(history contracts.AdvisoryHistory, id string) bool {
	for _, insight := range history.Insights {
		if insight.InsightID == id {
			return true
		}
	}
	return false
}

func hasRecommendation(history contracts.AdvisoryHistory, id string) bool {
	for _, recommendation := range history.Recommendations {
		if recommendation.RecommendationID == id {
			return true
		}
	}
	return false
}

func hasAudit(history contracts.AdvisoryHistory, id string) bool {
	for _, audit := range history.AuditRecords {
		if audit.AuditRecordID == id {
			return true
		}
	}
	return false
}

func hasModelRun(history contracts.AdvisoryHistory, id string) bool {
	for _, run := range history.ModelRuns {
		if run.ModelRunID == id {
			return true
		}
	}
	return false
}

func hasValidModelRun(history contracts.AdvisoryHistory, id, agent, outputID string) bool {
	for _, run := range history.ModelRuns {
		if run.ModelRunID != id {
			continue
		}
		if run.Agent != agent || run.ValidationStatus != "valid" {
			return false
		}
		for _, output := range run.OutputIds {
			if output == outputID {
				return true
			}
		}
		return false
	}
	return false
}

func parseRFC3339(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
