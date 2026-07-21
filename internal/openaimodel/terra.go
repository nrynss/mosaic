package openaimodel

import (
	"context"
	"fmt"
	"strings"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/terra"
)

// TerraClient implements terra.StructuredClient over the OpenAI Responses API.
type TerraClient struct {
	transport    *transport
	instructions string
}

// NewTerraClient constructs a live Terra client. APIKey and versioned prompt
// content are required; composition loads the prompt from the asset root.
func NewTerraClient(cfg Config) (*TerraClient, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultTerraModel
	}
	t, err := newTransport(cfg, DefaultTerraModel)
	if err != nil {
		return nil, err
	}
	instructions := strings.TrimSpace(cfg.Instructions)
	if instructions == "" {
		return nil, fmt.Errorf("terra instructions are required")
	}
	return &TerraClient{transport: t, instructions: instructions}, nil
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
		Instructions: c.instructions,
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
