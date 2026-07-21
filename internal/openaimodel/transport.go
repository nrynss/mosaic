package openaimodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"mosaic.local/mosaic/internal/usage"
)

const (
	// DefaultEndpoint is the OpenAI Responses API create endpoint.
	DefaultEndpoint = "https://api.openai.com/v1/responses"

	// Default model family for the interactive demo (RFC-0005).
	DefaultLunaModel  = "gpt-5.6"
	DefaultTerraModel = "gpt-5.6"
	DefaultSolModel   = "gpt-5.6"

	envAPIKey = "OPENAI_API_KEY"

	// maxHTTPResponseBytes bounds the decoded HTTP body (payload + envelope).
	maxHTTPResponseBytes = 4*1024*1024 + 64*1024
)

// APIKeyFromEnv returns the server-only runtime secret from OPENAI_API_KEY.
// The value is never logged by this package.
func APIKeyFromEnv() string {
	return strings.TrimSpace(os.Getenv(envAPIKey))
}

// Config is shared transport configuration for live structured clients.
// APIKey is supplied by the composition root (typically from APIKeyFromEnv).
// Endpoint is overridable for tests; the default is DefaultEndpoint.
// HTTPClient is optional; when nil a client that refuses redirects is used.
type Config struct {
	APIKey   string
	Endpoint string
	Model    string
	// Instructions is the versioned prompt content supplied by composition.
	// Terra and Sol require it; Luna retains its temporary inline prompt until H2.
	Instructions string
	HTTPClient   *http.Client
}

type transport struct {
	apiKey   string
	endpoint string
	model    string
	client   *http.Client
}

func newTransport(cfg Config, defaultModel string) (*transport, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("missing API key")
	}
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultModel
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &transport{
		apiKey:   cfg.APIKey,
		endpoint: endpoint,
		model:    model,
		client:   httpClient,
	}, nil
}

func normalizeEndpoint(endpoint string) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return DefaultEndpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("openai endpoint must be an absolute URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("openai endpoint must not include query or fragment")
	}
	return parsed.String(), nil
}

// structuredCall is one-shot: no retries on rate limit or other HTTP failures.
type structuredCall struct {
	Instructions string
	SchemaName   string
	UserInput    string
}

type structuredResult struct {
	ResponseID   string
	JSON         json.RawMessage
	Refusal      string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

func (t *transport) call(ctx context.Context, call structuredCall) (structuredResult, error) {
	if t == nil {
		return structuredResult{}, errors.New("openai transport is not configured")
	}
	if err := ctx.Err(); err != nil {
		return structuredResult{}, err
	}

	payload := responsesRequest{
		Model:        t.model,
		Instructions: call.Instructions,
		Input:        call.UserInput,
		Store:        false,
		Text: textConfig{
			Format: textFormat{
				Type:   "json_schema",
				Name:   call.SchemaName,
				Strict: false,
				Schema: map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return structuredResult{}, fmt.Errorf("encode openai request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return structuredResult{}, fmt.Errorf("create openai request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+t.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := t.client.Do(httpRequest)
	if err != nil {
		return structuredResult{}, fmt.Errorf("openai request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusTooManyRequests {
		return structuredResult{}, fmt.Errorf("openai rate limited (retry-after %q); not retrying", response.Header.Get("Retry-After"))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return structuredResult{}, fmt.Errorf("openai request failed with HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPResponseBytes+1))
	if err != nil {
		return structuredResult{}, fmt.Errorf("read openai response: %w", err)
	}
	if len(body) > maxHTTPResponseBytes {
		return structuredResult{}, fmt.Errorf("openai response exceeds %d bytes", maxHTTPResponseBytes)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return structuredResult{}, errors.New("openai response body is empty")
	}

	var decoded responsesAPIResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return structuredResult{}, fmt.Errorf("decode openai response: %w", err)
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return structuredResult{}, fmt.Errorf("openai API error: %s", sanitizedErrorMessage(decoded.Error.Message))
	}

	responseID := strings.TrimSpace(decoded.ID)
	var inputTokens, outputTokens, totalTokens int
	if decoded.Usage != nil {
		inputTokens = decoded.Usage.InputTokens
		outputTokens = decoded.Usage.OutputTokens
		totalTokens = decoded.Usage.TotalTokens
	}
	text, refusal := extractOutput(decoded)
	if strings.TrimSpace(refusal) != "" {
		// A refusal still consumes tokens, so it still counts toward the
		// process-local spend estimate.
		usage.Global.Record(t.model, inputTokens, outputTokens)
		return structuredResult{
			ResponseID:   responseID,
			Refusal:      strings.TrimSpace(refusal),
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  totalTokens,
		}, nil
	}
	if strings.TrimSpace(text) == "" {
		return structuredResult{}, errors.New("openai response has no structured text output")
	}
	raw := json.RawMessage(strings.TrimSpace(text))
	if !json.Valid(raw) {
		return structuredResult{}, errors.New("openai response text is not valid JSON")
	}
	// Ensure the payload is a JSON object (object schemas for Insight/Recommendation/LunaResult).
	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return structuredResult{}, fmt.Errorf("decode openai structured output: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return structuredResult{}, errors.New("openai structured output must be a JSON object")
	}
	usage.Global.Record(t.model, inputTokens, outputTokens)
	return structuredResult{
		ResponseID:   responseID,
		JSON:         append(json.RawMessage(nil), raw...),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
	}, nil
}

type responsesRequest struct {
	Model        string     `json:"model"`
	Instructions string     `json:"instructions,omitempty"`
	Input        string     `json:"input"`
	Store        bool       `json:"store"`
	Text         textConfig `json:"text"`
}

type textConfig struct {
	Format textFormat `json:"format"`
}

type textFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Strict bool           `json:"strict,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
}

type responsesAPIResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Output []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func extractOutput(decoded responsesAPIResponse) (text string, refusal string) {
	var texts []string
	for _, item := range decoded.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			switch part.Type {
			case "refusal":
				if strings.TrimSpace(part.Refusal) != "" {
					refusal = part.Refusal
				}
			case "output_text", "text":
				if strings.TrimSpace(part.Text) != "" {
					texts = append(texts, part.Text)
				}
			default:
				// Some gateways omit type and only populate text/refusal.
				if strings.TrimSpace(part.Refusal) != "" {
					refusal = part.Refusal
				} else if strings.TrimSpace(part.Text) != "" {
					texts = append(texts, part.Text)
				}
			}
		}
	}
	if refusal != "" {
		return "", refusal
	}
	return strings.Join(texts, ""), ""
}

// sanitizedErrorMessage strips accidental credential-shaped tokens from upstream messages.
func sanitizedErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "unknown error"
	}
	// Never echo bearer material if a provider mirrors it into an error body.
	// Build the prefix at runtime so package sources stay free of key-shaped literals.
	keyPrefix := "sk" + string('-')
	if strings.Contains(strings.ToLower(message), keyPrefix) {
		return "request rejected"
	}
	return message
}

func marshalInput(label string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s input: %w", label, err)
	}
	return string(encoded), nil
}
