package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/terra"
)

func TestFixtureAdvisoryReplayFreshSequence(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	beforeRevision := run.StateRevision
	beforeCOP := canonicalJSON(t, run.COP)
	beforeCounts := durableAdvisoryCounts(t, database)

	replay, err := NewAdvisoryReplay(AdvisoryReplayConfig{
		Store:      database,
		SchemaDir:  filepath.Join("..", "..", "ontology"),
		FixtureDir: filepath.Join("..", "..", "datasets", DomesticDisturbance),
	})
	if err != nil {
		t.Fatalf("new advisory replay: %v", err)
	}
	result, err := replay.Replay(ctx, run.Timeline)
	if err != nil {
		t.Fatalf("replay fixture advisories: %v", err)
	}
	if result.IntactRestart || len(result.StagesRun) != 5 || len(result.StagesSkipped) != 0 {
		t.Fatalf("fresh replay result = %#v", result)
	}

	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read advisory history: %v", err)
	}
	assertFixtureAdvisoryHistory(t, history)

	after, err := service.Recover(ctx)
	if err != nil {
		t.Fatalf("recover after advisory replay: %v", err)
	}
	if after.StateRevision != beforeRevision || canonicalJSON(t, after.COP) != beforeCOP {
		t.Fatalf("advisory replay mutated COP: revision %d/%d", after.StateRevision, beforeRevision)
	}
	afterCounts := durableAdvisoryCounts(t, database)
	if afterCounts.insights != beforeCounts.insights+2 ||
		afterCounts.recommendations != beforeCounts.recommendations+1 ||
		afterCounts.auditRecords != beforeCounts.auditRecords+2 ||
		afterCounts.terraSolRuns != beforeCounts.terraSolRuns+3 {
		t.Fatalf("advisory counts before=%#v after=%#v", beforeCounts, afterCounts)
	}
}

func TestFixtureAdvisoryReplayIntactRestartIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	replay := mustAdvisoryReplay(t, database)

	if _, err := replay.Replay(ctx, run.Timeline); err != nil {
		t.Fatalf("first advisory replay: %v", err)
	}
	before := durableAdvisoryCounts(t, database)
	beforeHistory, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history after first replay: %v", err)
	}

	restarted := mustAdvisoryReplay(t, database)
	result, err := restarted.Replay(ctx, run.Timeline)
	if err != nil {
		t.Fatalf("intact restart replay: %v", err)
	}
	if !result.IntactRestart || len(result.StagesRun) != 0 || len(result.StagesSkipped) != 5 {
		t.Fatalf("intact restart result = %#v", result)
	}
	after := durableAdvisoryCounts(t, database)
	if after != before {
		t.Fatalf("restart appended advisory records: before=%#v after=%#v", before, after)
	}
	afterHistory, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history after restart: %v", err)
	}
	if canonicalJSON(t, afterHistory) != canonicalJSON(t, beforeHistory) {
		t.Fatalf("restart changed advisory history\nbefore=%s\nafter=%s", canonicalJSON(t, beforeHistory), canonicalJSON(t, afterHistory))
	}
}

func TestFixtureAdvisoryReplayUsesHistoricalRevisionSnapshots(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}

	terraProbe := &recordingTerraClient{}
	solProbe := &recordingSolClient{}
	replay, err := NewAdvisoryReplay(AdvisoryReplayConfig{
		Store:       database,
		SchemaDir:   filepath.Join("..", "..", "ontology"),
		FixtureDir:  filepath.Join("..", "..", "datasets", DomesticDisturbance),
		TerraClient: terraProbe,
		SolClient:   solProbe,
	})
	if err != nil {
		t.Fatalf("new advisory replay: %v", err)
	}
	// Seed fixture responses after load so probes can return exact artifacts.
	active := replay.fixture.insights["insight-domestic-access-001"]
	obsolete := replay.fixture.insights["insight-domestic-access-001-obsolete"]
	recommendation := replay.fixture.recommendations["recommendation-domestic-001"]
	terraProbe.active = active
	terraProbe.obsolete = obsolete
	solProbe.recommendation = recommendation

	if _, err := replay.Replay(ctx, run.Timeline); err != nil {
		t.Fatalf("replay with probes: %v", err)
	}
	if len(terraProbe.requests) != 2 {
		t.Fatalf("terra assessments = %d, want 2", len(terraProbe.requests))
	}
	if terraProbe.requests[0].StateRevision != 7 {
		t.Fatalf("first terra revision = %d, want 7", terraProbe.requests[0].StateRevision)
	}
	if !roadStatus(decodeCOP(t, terraProbe.requests[0].SerializedCOP), "road-brook-lane", "blocked") {
		t.Fatalf("rev-7 terra COP did not use the historical blocked Brook Lane snapshot: %s", terraProbe.requests[0].SerializedCOP)
	}
	if terraProbe.requests[1].StateRevision != 9 {
		t.Fatalf("second terra revision = %d, want 9", terraProbe.requests[1].StateRevision)
	}
	if !roadStatus(decodeCOP(t, terraProbe.requests[1].SerializedCOP), "road-brook-lane", "open") {
		t.Fatalf("rev-9 terra COP did not use the historical open Brook Lane snapshot: %s", terraProbe.requests[1].SerializedCOP)
	}
	if len(solProbe.requests) != 1 || solProbe.requests[0].StateRevision != 7 || solProbe.requests[0].RequestedBy != sol.SupervisorIdentity {
		t.Fatalf("sol request = %#v", solProbe.requests)
	}
	if !roadStatus(decodeCOP(t, solProbe.requests[0].SerializedCOP), "road-brook-lane", "blocked") {
		t.Fatalf("sol briefing used a non-rev-7 COP snapshot: %s", solProbe.requests[0].SerializedCOP)
	}

	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	assertFixtureAdvisoryHistory(t, history)
}

func TestFixtureAdvisoryReplayRejectsPartialStage(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	// A Model Run without its Insight is a partial stage and must fail closed.
	if err := database.AppendModelRun(ctx, gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          fixtureTerraActiveRunID,
		Agent:               "terra",
		Provider:            fixtureAdvisoryProvider,
		Model:               fixtureTerraModel,
		PromptVersion:       fixtureAdvisoryPrompt,
		OutputSchemaVersion: "1.0.0",
		ValidationStatus:    "valid",
		StateRevision:       7,
		StartedAt:           "2026-07-18T10:05:32Z",
		CompletedAt:         "2026-07-18T10:05:32Z",
	}); err != nil {
		t.Fatalf("seed partial model run: %v", err)
	}
	before := durableAdvisoryCounts(t, database)

	replay := mustAdvisoryReplay(t, database)
	_, err = replay.Replay(ctx, run.Timeline)
	if !errors.Is(err, ErrPartialAdvisoryStage) {
		t.Fatalf("partial stage error = %v, want %v", err, ErrPartialAdvisoryStage)
	}
	after := durableAdvisoryCounts(t, database)
	if after != before {
		t.Fatalf("partial failure mutated advisory storage: before=%#v after=%#v", before, after)
	}
}

func TestFixtureAdvisoryReplayRejectsGappedStageSequence(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	// Audit for stage 2 without stage 1 is an integrity gap.
	if err := database.AppendAuditRecord(ctx, gen.AuditRecord{
		SchemaVersion: "1.0.0",
		AuditRecordID: "audit-domestic-001-briefing-request",
		ActorID:       "supervisor-demo",
		ActorRole:     "supervisor",
		Action:        "briefing_requested",
		TargetKind:    "briefing",
		TargetID:      "briefing-domestic-001",
		Note:          "orphaned fixture audit",
		CreatedAt:     "2026-07-18T10:05:33Z",
	}); err != nil {
		t.Fatalf("seed gapped audit: %v", err)
	}

	replay := mustAdvisoryReplay(t, database)
	if _, err := replay.Replay(ctx, run.Timeline); !errors.Is(err, ErrPartialAdvisoryStage) {
		t.Fatalf("gapped sequence error = %v, want %v", err, ErrPartialAdvisoryStage)
	}
}

func TestFixtureAdvisoryReplayRefusalRecordsModelRunWithoutInsight(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	beforeRevision := run.StateRevision
	beforeCOP := canonicalJSON(t, run.COP)

	replay, err := NewAdvisoryReplay(AdvisoryReplayConfig{
		Store:       database,
		SchemaDir:   filepath.Join("..", "..", "ontology"),
		FixtureDir:  filepath.Join("..", "..", "datasets", DomesticDisturbance),
		TerraClient: fixtureTerraClient{responseID: fixtureTerraActiveRespID, refuse: "fixture refused assessment"},
	})
	if err != nil {
		t.Fatalf("new refusing replay: %v", err)
	}
	_, err = replay.Replay(ctx, run.Timeline)
	if !errors.Is(err, terra.ErrAssessmentRefused) {
		t.Fatalf("refusal error = %v, want %v", err, terra.ErrAssessmentRefused)
	}

	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history after refusal: %v", err)
	}
	if len(history.Insights) != 0 || len(history.Recommendations) != 0 || len(history.AuditRecords) != 0 {
		t.Fatalf("refusal created advisory outputs: %#v", history)
	}
	if len(history.ModelRuns) != 1 || history.ModelRuns[0].ModelRunID != fixtureTerraActiveRunID || history.ModelRuns[0].ValidationStatus != "refused" {
		t.Fatalf("refusal model runs = %#v", history.ModelRuns)
	}

	after, err := service.Recover(ctx)
	if err != nil {
		t.Fatalf("recover after refusal: %v", err)
	}
	if after.StateRevision != beforeRevision || canonicalJSON(t, after.COP) != beforeCOP {
		t.Fatal("refusal path mutated COP state")
	}

	// A refused stage leaves a durable Model Run without its Insight: restart
	// must fail closed rather than rewrite or continue the sequence.
	if _, err := mustAdvisoryReplay(t, database).Replay(ctx, run.Timeline); !errors.Is(err, ErrPartialAdvisoryStage) {
		t.Fatalf("post-refusal restart error = %v, want partial stage", err)
	}
}

func TestFixtureAdvisoryReplayInvalidResponseRecordsModelRunWithoutInsight(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}

	replay, err := NewAdvisoryReplay(AdvisoryReplayConfig{
		Store:       database,
		SchemaDir:   filepath.Join("..", "..", "ontology"),
		FixtureDir:  filepath.Join("..", "..", "datasets", DomesticDisturbance),
		TerraClient: fixtureTerraClient{responseID: fixtureTerraActiveRespID, invalid: true},
	})
	if err != nil {
		t.Fatalf("new invalid replay: %v", err)
	}
	_, err = replay.Replay(ctx, run.Timeline)
	if !errors.Is(err, terra.ErrInvalidAssessment) {
		t.Fatalf("invalid error = %v, want %v", err, terra.ErrInvalidAssessment)
	}
	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history after invalid response: %v", err)
	}
	if len(history.Insights) != 0 || len(history.Recommendations) != 0 {
		t.Fatalf("invalid response created outputs: %#v", history)
	}
	if len(history.ModelRuns) != 1 || history.ModelRuns[0].ValidationStatus != "invalid" {
		t.Fatalf("invalid model runs = %#v", history.ModelRuns)
	}
}

func TestFixtureAdvisoryReplayRequiresTimelineSnapshots(t *testing.T) {
	ctx := context.Background()
	_, database := newTestService(t, ctx)
	replay := mustAdvisoryReplay(t, database)
	if _, err := replay.Replay(ctx, nil); !errors.Is(err, ErrAdvisoryTimeline) {
		t.Fatalf("missing timeline error = %v, want %v", err, ErrAdvisoryTimeline)
	}
}

func TestFixtureAdvisoryReplayRollsBackInsightPairOnWriteFailure(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	before := durableAdvisoryCounts(t, database)
	beforeRevision := run.StateRevision
	beforeCOP := canonicalJSON(t, run.COP)

	replay := mustAdvisoryReplay(t, database)
	injected := errors.New("injected insight persistence failure")
	replay.records.failInsight = injected

	_, err = replay.Replay(ctx, run.Timeline)
	if !errors.Is(err, injected) {
		t.Fatalf("insight write failure error = %v, want %v", err, injected)
	}

	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history after insight rollback: %v", err)
	}
	if len(history.Insights) != 0 || len(history.Recommendations) != 0 || len(history.AuditRecords) != 0 || len(history.ModelRuns) != 0 {
		t.Fatalf("rolled-back insight pair left advisory records: %#v", history)
	}
	if durableAdvisoryCounts(t, database) != before {
		t.Fatalf("insight pair rollback changed counts: before=%#v after=%#v", before, durableAdvisoryCounts(t, database))
	}
	after, err := service.Recover(ctx)
	if err != nil {
		t.Fatalf("recover after insight rollback: %v", err)
	}
	if after.StateRevision != beforeRevision || canonicalJSON(t, after.COP) != beforeCOP {
		t.Fatal("insight pair rollback mutated COP state")
	}

	// Clear the injection and complete the sequence to prove storage stayed clean.
	replay.records.failInsight = nil
	if _, err := replay.Replay(ctx, run.Timeline); err != nil {
		t.Fatalf("replay after cleared injection: %v", err)
	}
	assertFixtureAdvisoryHistory(t, mustReadHistory(t, database))
}

func TestFixtureAdvisoryReplayRollsBackRecommendationPairOnWriteFailure(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}

	replay := mustAdvisoryReplay(t, database)
	// Advance through Terra active + briefing audit, then fail the Sol pair write.
	active := replay.fixture.insights["insight-domestic-access-001"]
	if err := replay.runTerraActive(ctx, copAtRevisionOrFatal(t, run.Timeline, 7), active); err != nil {
		t.Fatalf("seed terra active stage: %v", err)
	}
	briefing := replay.fixture.auditRecords[replay.fixture.Expectation.SolRequest.AuditRecordID]
	if err := replay.appendAudit(ctx, briefing); err != nil {
		t.Fatalf("seed briefing audit: %v", err)
	}
	before := durableAdvisoryCounts(t, database)
	if before.insights != 1 || before.auditRecords != 1 || before.terraSolRuns != 1 || before.recommendations != 0 {
		t.Fatalf("seed counts = %#v", before)
	}

	injected := errors.New("injected recommendation persistence failure")
	replay.records.failRecommendation = injected
	recommendation := replay.fixture.recommendations["recommendation-domestic-001"]
	err = replay.runSolRecommendation(ctx, copAtRevisionOrFatal(t, run.Timeline, 7), active, recommendation)
	if !errors.Is(err, injected) {
		t.Fatalf("recommendation write failure error = %v, want %v", err, injected)
	}

	history, err := database.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("read history after recommendation rollback: %v", err)
	}
	if len(history.Recommendations) != 0 {
		t.Fatalf("recommendation survived rollback: %#v", history.Recommendations)
	}
	if hasModelRun(history, fixtureSolRunID) {
		t.Fatalf("paired Sol Model Run survived recommendation write failure: %#v", history.ModelRuns)
	}
	after := durableAdvisoryCounts(t, database)
	if after != before {
		t.Fatalf("recommendation pair rollback changed earlier stages: before=%#v after=%#v", before, after)
	}
}

func mustReadHistory(t *testing.T, database *store.Store) contracts.AdvisoryHistory {
	t.Helper()
	history, err := database.ReadAdvisoryHistory(context.Background())
	if err != nil {
		t.Fatalf("read advisory history: %v", err)
	}
	return history
}

func copAtRevisionOrFatal(t *testing.T, timeline []TimelineEntry, revision int64) map[string]any {
	t.Helper()
	cop, err := copAtRevision(timeline, revision)
	if err != nil {
		t.Fatalf("cop at revision %d: %v", revision, err)
	}
	return cop
}

func mustAdvisoryReplay(t *testing.T, database *store.Store) *AdvisoryReplay {
	t.Helper()
	replay, err := NewAdvisoryReplay(AdvisoryReplayConfig{
		Store:      database,
		SchemaDir:  filepath.Join("..", "..", "ontology"),
		FixtureDir: filepath.Join("..", "..", "datasets", DomesticDisturbance),
	})
	if err != nil {
		t.Fatalf("new advisory replay: %v", err)
	}
	return replay
}

func assertFixtureAdvisoryHistory(t *testing.T, history contracts.AdvisoryHistory) {
	t.Helper()
	if len(history.Insights) != 2 {
		t.Fatalf("insights = %d, want 2", len(history.Insights))
	}
	if history.Insights[0].InsightID != "insight-domestic-access-001" || history.Insights[0].LifecycleStatus != "active" || history.Insights[0].StateRevision != 7 {
		t.Fatalf("active insight = %#v", history.Insights[0])
	}
	if history.Insights[1].InsightID != "insight-domestic-access-001-obsolete" || history.Insights[1].LifecycleStatus != "obsolete" || history.Insights[1].StateRevision != 9 {
		t.Fatalf("obsolete insight = %#v", history.Insights[1])
	}
	if history.Insights[1].SupersedesInsightID != history.Insights[0].InsightID {
		t.Fatalf("obsolescence link = %q, want %q", history.Insights[1].SupersedesInsightID, history.Insights[0].InsightID)
	}
	if len(history.Recommendations) != 1 || history.Recommendations[0].RecommendationID != "recommendation-domestic-001" || history.Recommendations[0].StateRevision != 7 {
		t.Fatalf("recommendations = %#v", history.Recommendations)
	}
	if len(history.AuditRecords) != 2 {
		t.Fatalf("audit records = %#v", history.AuditRecords)
	}
	if history.AuditRecords[0].Action != "briefing_requested" || history.AuditRecords[0].ActorID != "supervisor-demo" {
		t.Fatalf("briefing audit = %#v", history.AuditRecords[0])
	}
	if history.AuditRecords[1].Action != "acknowledged" || history.AuditRecords[1].TargetID != "recommendation-domestic-001" {
		t.Fatalf("acknowledgement audit = %#v", history.AuditRecords[1])
	}
	if len(history.ModelRuns) != 3 {
		t.Fatalf("terra/sol model runs = %#v", history.ModelRuns)
	}
	byID := map[string]gen.ModelRun{}
	for _, run := range history.ModelRuns {
		byID[run.ModelRunID] = run
		if run.Provider != fixtureAdvisoryProvider || run.ValidationStatus != "valid" {
			t.Fatalf("unexpected model run provenance: %#v", run)
		}
	}
	if byID[fixtureTerraActiveRunID].Agent != "terra" || byID[fixtureTerraActiveRunID].StateRevision != 7 {
		t.Fatalf("active terra run = %#v", byID[fixtureTerraActiveRunID])
	}
	if byID[fixtureSolRunID].Agent != "sol" || byID[fixtureSolRunID].StateRevision != 7 {
		t.Fatalf("sol run = %#v", byID[fixtureSolRunID])
	}
	if byID[fixtureTerraObsoleteRunID].Agent != "terra" || byID[fixtureTerraObsoleteRunID].StateRevision != 9 {
		t.Fatalf("obsolete terra run = %#v", byID[fixtureTerraObsoleteRunID])
	}
}

type advisoryCounts struct {
	insights        int
	recommendations int
	auditRecords    int
	terraSolRuns    int
}

func durableAdvisoryCounts(t *testing.T, database *store.Store) advisoryCounts {
	t.Helper()
	history, err := database.ReadAdvisoryHistory(context.Background())
	if err != nil {
		t.Fatalf("read advisory history counts: %v", err)
	}
	return advisoryCounts{
		insights:        len(history.Insights),
		recommendations: len(history.Recommendations),
		auditRecords:    len(history.AuditRecords),
		terraSolRuns:    len(history.ModelRuns),
	}
}

func canonicalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	return string(encoded)
}

func decodeCOP(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var cop map[string]any
	if err := json.Unmarshal(raw, &cop); err != nil {
		t.Fatalf("decode COP: %v", err)
	}
	return cop
}

type recordingTerraClient struct {
	active    gen.Insight
	obsolete  gen.Insight
	requests  []terra.Request
	callCount int
}

func (c *recordingTerraClient) Assess(_ context.Context, request terra.Request) (terra.Response, error) {
	c.requests = append(c.requests, cloneTerraRequest(request))
	c.callCount++
	var insight gen.Insight
	switch c.callCount {
	case 1:
		insight = c.active
	default:
		insight = c.obsolete
	}
	encoded, err := json.Marshal(insight)
	if err != nil {
		return terra.Response{}, err
	}
	return terra.Response{InsightJSON: encoded, ResponseID: "probe-terra"}, nil
}

type recordingSolClient struct {
	recommendation gen.Recommendation
	requests       []sol.Request
}

func (c *recordingSolClient) Brief(_ context.Context, request sol.Request) (sol.Response, error) {
	c.requests = append(c.requests, cloneSolRequest(request))
	encoded, err := json.Marshal(c.recommendation)
	if err != nil {
		return sol.Response{}, err
	}
	return sol.Response{RecommendationJSON: encoded, ResponseID: "probe-sol"}, nil
}

func cloneTerraRequest(request terra.Request) terra.Request {
	return terra.Request{
		StateRevision: request.StateRevision,
		SerializedCOP: append(json.RawMessage(nil), request.SerializedCOP...),
		Evidence:      append([]gen.Evidence(nil), request.Evidence...),
	}
}

func cloneSolRequest(request sol.Request) sol.Request {
	return sol.Request{
		StateRevision: request.StateRevision,
		SerializedCOP: append(json.RawMessage(nil), request.SerializedCOP...),
		Insights:      append([]gen.Insight(nil), request.Insights...),
		Evidence:      append([]gen.Evidence(nil), request.Evidence...),
		RequestedBy:   request.RequestedBy,
	}
}
