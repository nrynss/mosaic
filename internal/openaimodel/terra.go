package openaimodel

import (
	"context"
	"fmt"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/terra"
)

const terraInstructions = `You are Terra, Mosaic's structured assessment agent.
Given a committed COP serialization, its state revision, and permitted evidence,
return one Insight JSON object (schema_version 1.0.0) with assertions that cite
only the supplied evidence. You inform operators; you never issue operational
actions or mutate the projection. Respond with a single Insight JSON object only.`

// TerraClient implements terra.StructuredClient over the OpenAI Responses API.
type TerraClient struct {
	transport *transport
}

// NewTerraClient constructs a live Terra client. APIKey is required.
func NewTerraClient(cfg Config) (*TerraClient, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultTerraModel
	}
	t, err := newTransport(cfg, DefaultTerraModel)
	if err != nil {
		return nil, err
	}
	return &TerraClient{transport: t}, nil
}

// Assess performs one Responses API call and maps structured Insight JSON or refusal.
func (c *TerraClient) Assess(ctx context.Context, request terra.Request) (terra.Response, error) {
	if c == nil || c.transport == nil {
		return terra.Response{}, fmt.Errorf("terra client is not configured")
	}

	input, err := marshalInput("terra", terraWireInput{
		StateRevision: request.StateRevision,
		SerializedCOP: rawOrNull(request.SerializedCOP),
		Evidence:      request.Evidence,
	})
	if err != nil {
		return terra.Response{}, err
	}

	result, err := c.transport.call(ctx, structuredCall{
		Instructions: terraInstructions,
		SchemaName:   "insight",
		UserInput:    input,
	})
	if err != nil {
		return terra.Response{}, err
	}
	if result.Refusal != "" {
		return terra.Response{ResponseID: result.ResponseID, RefusalDetail: result.Refusal}, nil
	}
	return terra.Response{
		InsightJSON: result.JSON,
		ResponseID:  result.ResponseID,
	}, nil
}

type terraWireInput struct {
	StateRevision int64          `json:"state_revision"`
	SerializedCOP any            `json:"serialized_cop"`
	Evidence      []gen.Evidence `json:"evidence"`
}
