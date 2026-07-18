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

func TestAgentContractsRemainStructured(t *testing.T) {
	var _ LunaAdapter = contractFixture{}
	var _ TerraAdapter = contractFixture{}
	var _ SolAdapter = contractFixture{}

	if (SolInput{COP: map[string]any{"state_revision": int64(1)}}).COP == nil {
		t.Fatal("structured COP input must remain available to Sol")
	}
}
