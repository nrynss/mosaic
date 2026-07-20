package contracts

import (
	"context"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/ontology/gen"
)

type contractFixture struct{}

func (contractFixture) Normalize(context.Context, gen.RawEvent) (LunaOutput, error) {
	return LunaOutput{}, nil
}
func (contractFixture) Assess(context.Context, TerraInput) (TerraOutput, error) {
	return TerraOutput{}, nil
}
func (contractFixture) Brief(context.Context, SolInput) (SolOutput, error) { return SolOutput{}, nil }

type advisoryHistoryFixture struct{}

func (advisoryHistoryFixture) ReadAdvisoryHistory(context.Context) (AdvisoryHistory, error) {
	return AdvisoryHistory{}, nil
}

func TestAgentContractsRemainStructured(t *testing.T) {
	var _ LunaAdapter = contractFixture{}
	var _ TerraAdapter = contractFixture{}
	var _ SolAdapter = contractFixture{}

	if (SolInput{COP: map[string]any{"state_revision": int64(1)}}).COP == nil {
		t.Fatal("structured COP input must remain available to Sol")
	}
}

func TestAdvisoryHistoryReaderRemainsBoundedDomainSnapshot(t *testing.T) {
	var _ AdvisoryHistoryReader = advisoryHistoryFixture{}

	history := AdvisoryHistory{
		Insights:        []gen.Insight{{InsightID: "insight-001"}},
		Recommendations: []gen.Recommendation{{RecommendationID: "recommendation-001"}},
		ModelRuns:       []gen.ModelRun{{ModelRunID: "model-run-001"}},
		AuditRecords:    []gen.AuditRecord{{AuditRecordID: "audit-record-001"}},
	}

	if len(history.Insights) != 1 || len(history.Recommendations) != 1 || len(history.ModelRuns) != 1 || len(history.AuditRecords) != 1 {
		t.Fatal("advisory history must retain each persisted advisory record class")
	}
}

type simulationScheduleFixture struct{}

func (simulationScheduleFixture) Beats() []ScheduledBeat {
	return []ScheduledBeat{
		{
			BeatID:     "beat-001",
			Order:      1,
			RawEventID: "raw-001",
			Delay:      100 * time.Millisecond,
		},
	}
}

func TestSimulationContracts(t *testing.T) {
	var _ SimulationSchedule = simulationScheduleFixture{}

	session := SimulationSession{
		SessionID: "session-001",
		Status:    SessionRunning,
		Beats: []ScheduledBeat{
			{
				BeatID:     "beat-001",
				Order:      1,
				RawEventID: "raw-001",
				Delay:      100 * time.Millisecond,
			},
		},
	}

	if session.Status != "running" {
		t.Errorf("session status mismatch: got %v, want running", session.Status)
	}

	event := SimulationStreamEvent{
		SessionID: "session-001",
		Sequence:  1,
		Timestamp: time.Now(),
		Type:      StreamEventBeat,
	}

	if event.Type != "beat" {
		t.Errorf("event type mismatch: got %s, want beat", event.Type)
	}

	providerFixture := ProviderFixture
	providerLive := ProviderLive

	if providerFixture != "fixture" || providerLive != "live" {
		t.Errorf("provider value mismatch: fixture=%s, live=%s", providerFixture, providerLive)
	}

	selection := AgentProviderSelection{
		"luna":  ProviderFixture,
		"terra": ProviderLive,
	}

	if selection["luna"] != ProviderFixture || selection["terra"] != ProviderLive {
		t.Errorf("AgentProviderSelection mismatch")
	}

	if StreamEventBeat != "beat" || StreamEventStatusChange != "status_change" || StreamEventWorkspaceClear != "workspace_clear" {
		t.Errorf("StreamEventType value mismatch")
	}
}
