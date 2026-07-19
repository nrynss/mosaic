package contracts

import (
	"context"
	"testing"

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
