package openaimodel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
)

// SolClient implements sol.StructuredClient over the OpenAI Responses API.
type SolClient struct {
	transport    *transport
	instructions string
}

// NewSolClient constructs a live Sol client. APIKey and versioned prompt
// content are required; composition loads the prompt from the asset root.
func NewSolClient(cfg Config) (*SolClient, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultSolModel
	}
	t, err := newTransport(cfg, DefaultSolModel)
	if err != nil {
		return nil, err
	}
	instructions := strings.TrimSpace(cfg.Instructions)
	if instructions == "" {
		return nil, fmt.Errorf("sol instructions are required")
	}
	return &SolClient{transport: t, instructions: instructions}, nil
}

// Brief performs one Responses API call and maps structured Recommendation JSON or refusal.
func (c *SolClient) Brief(ctx context.Context, request sol.Request) (sol.Response, error) {
	if c == nil || c.transport == nil {
		return sol.Response{}, fmt.Errorf("sol client is not configured")
	}

	input, err := marshalInput("sol", solWireInput{
		StateRevision: request.StateRevision,
		SerializedCOP: rawOrNull(request.SerializedCOP),
		Insights:      request.Insights,
		Evidence:      request.Evidence,
		RequestedBy:   request.RequestedBy,
	})
	if err != nil {
		return sol.Response{}, err
	}

	result, err := c.transport.call(ctx, structuredCall{
		Instructions: c.instructions,
		SchemaName:   "recommendation",
		UserInput:    input,
	})
	if err != nil {
		return sol.Response{}, err
	}
	if result.Refusal != "" {
		return sol.Response{ResponseID: result.ResponseID, RefusalDetail: result.Refusal}, nil
	}
	return sol.Response{
		RecommendationJSON: result.JSON,
		ResponseID:         result.ResponseID,
	}, nil
}

type solWireInput struct {
	StateRevision int64          `json:"state_revision"`
	SerializedCOP any            `json:"serialized_cop"`
	Insights      []gen.Insight  `json:"insights"`
	Evidence      []gen.Evidence `json:"evidence"`
	RequestedBy   string         `json:"requested_by"`
}

func rawOrNull(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return json.RawMessage(raw)
}
