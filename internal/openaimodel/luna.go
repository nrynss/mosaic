package openaimodel

import (
	"context"
	"encoding/json"
	"fmt"
)

// LunaStructuredClient is the package-local structured normalization seam.
// Luna has no StructuredClient in the luna package; composition roots wire this
// client (or a fixture) and own ModelRun recording themselves.
type LunaStructuredClient interface {
	Normalize(context.Context, LunaRequest) (LunaResponse, error)
}

// LunaRequest is the least-privilege envelope allowed across the Luna model
// boundary. RawEventJSON is envelope-only structured fields; the transport never
// claims an operational write.
type LunaRequest struct {
	RawEventJSON json.RawMessage
}

// LunaResponse is transport metadata plus JSON representations of Luna artifacts.
// A refusal is explicit and has empty JSON payloads. Callers record ModelRuns.
type LunaResponse struct {
	ResultJSON         json.RawMessage
	CanonicalEventJSON json.RawMessage
	ResponseID         string
	RefusalDetail      string
}

const lunaInstructions = `You are Luna, Mosaic's structured event normalizer.
Given a Raw Event envelope (no operational authority), return JSON with:
- "result": a LunaResult object (schema_version 1.0.0) describing accept/refuse/fail status
- "canonical_event": optional CanonicalEvent when status is accepted
Never claim to mutate operational state. Cite evidence against the raw event only.
Respond with a single JSON object only.`

// LunaClient implements LunaStructuredClient over the OpenAI Responses API.
type LunaClient struct {
	transport *transport
}

// NewLunaClient constructs a live Luna client. APIKey is required.
func NewLunaClient(cfg Config) (*LunaClient, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultLunaModel
	}
	t, err := newTransport(cfg, DefaultLunaModel)
	if err != nil {
		return nil, err
	}
	return &LunaClient{transport: t}, nil
}

// Normalize performs one Responses API call and maps structured output or refusal.
func (c *LunaClient) Normalize(ctx context.Context, request LunaRequest) (LunaResponse, error) {
	if c == nil || c.transport == nil {
		return LunaResponse{}, fmt.Errorf("luna client is not configured")
	}
	if len(request.RawEventJSON) == 0 {
		return LunaResponse{}, fmt.Errorf("luna request requires RawEventJSON")
	}
	if !json.Valid(request.RawEventJSON) {
		return LunaResponse{}, fmt.Errorf("luna RawEventJSON is not valid JSON")
	}

	result, err := c.transport.call(ctx, structuredCall{
		Instructions: lunaInstructions,
		SchemaName:   "luna_result",
		UserInput:    string(request.RawEventJSON),
	})
	if err != nil {
		return LunaResponse{}, err
	}
	if result.Refusal != "" {
		return LunaResponse{ResponseID: result.ResponseID, RefusalDetail: result.Refusal}, nil
	}
	mapped, err := mapLunaPayload(result.JSON)
	if err != nil {
		return LunaResponse{}, err
	}
	mapped.ResponseID = result.ResponseID
	return mapped, nil
}

func mapLunaPayload(payload json.RawMessage) (LunaResponse, error) {
	var wrapper struct {
		Result         json.RawMessage `json:"result"`
		LunaResult     json.RawMessage `json:"luna_result"`
		CanonicalEvent json.RawMessage `json:"canonical_event"`
	}
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		return LunaResponse{}, fmt.Errorf("decode luna payload: %w", err)
	}
	result := wrapper.Result
	if len(result) == 0 {
		result = wrapper.LunaResult
	}
	if len(result) > 0 {
		if !json.Valid(result) {
			return LunaResponse{}, fmt.Errorf("luna result is not valid JSON")
		}
		out := LunaResponse{ResultJSON: append(json.RawMessage(nil), result...)}
		if len(wrapper.CanonicalEvent) > 0 && json.Valid(wrapper.CanonicalEvent) {
			out.CanonicalEventJSON = append(json.RawMessage(nil), wrapper.CanonicalEvent...)
		}
		return out, nil
	}
	// Whole payload is the LunaResult object.
	return LunaResponse{ResultJSON: append(json.RawMessage(nil), payload...)}, nil
}
