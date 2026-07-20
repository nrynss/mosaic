package openaimodel

import (
	"context"
	"encoding/json"
	"fmt"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
)

const solInstructions = `You are Sol, Mosaic's structured briefing agent.
Given a committed COP serialization, its state revision, active Insights,
permitted evidence, and the fixed requester identity, return one Recommendation
JSON object (schema_version 1.0.0). You inform operators only on explicit request;
you never issue operational actions or mutate the projection. Respond with a
single Recommendation JSON object only.`

// SolClient implements sol.StructuredClient over the OpenAI Responses API.
type SolClient struct {
	transport *transport
}

// NewSolClient constructs a live Sol client. APIKey is required.
func NewSolClient(cfg Config) (*SolClient, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultSolModel
	}
	t, err := newTransport(cfg, DefaultSolModel)
	if err != nil {
		return nil, err
	}
	return &SolClient{transport: t}, nil
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
		Instructions: solInstructions,
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
