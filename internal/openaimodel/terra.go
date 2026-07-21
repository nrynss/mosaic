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
	schema       structuredOutputSchema
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
	schema, err := loadStructuredOutputSchema(cfg.SchemaDir, insightSchemaRoute)
	if err != nil {
		return nil, fmt.Errorf("load Terra output schema: %w", err)
	}
	return &TerraClient{transport: t, instructions: instructions, schema: schema}, nil
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
		SchemaName:   c.schema.name,
		Schema:       c.schema.document,
		UserInput:    input,
	})
	if err != nil {
		return terra.Response{}, err
	}
	if result.Refusal != "" {
		return terra.Response{ResponseID: result.ResponseID, RefusalDetail: result.Refusal}, nil
	}
	insightJSON, err := withoutNullObjectProperties(result.JSON)
	if err != nil {
		return terra.Response{}, err
	}
	return terra.Response{
		InsightJSON: insightJSON,
		ResponseID:  result.ResponseID,
	}, nil
}

type terraWireInput struct {
	StateRevision int64          `json:"state_revision"`
	SerializedCOP any            `json:"serialized_cop"`
	Evidence      []gen.Evidence `json:"evidence"`
}
