package datasetgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// CerebrasGemmaModel is the sole remote model approved for the P29 candidate.
	CerebrasGemmaModel = "gemma-4-31b"

	defaultCerebrasEndpoint      = "https://api.cerebras.ai/v1/chat/completions"
	cerebrasTemperature          = 0.2
	cerebrasCompletionTokenLimit = 12_288
	cerebrasGenerationTimeout    = 90 * time.Second
	maxCerebrasHTTPResponseBytes = maxModelResponseBytes + 64*1024
	remoteProvenanceVersion      = "1.1.0"
)

// CerebrasGenerateConfig describes one rate-bounded remote candidate request.
// APIKey is runtime-only and is never included in staged provenance.
type CerebrasGenerateConfig struct {
	APIKey     string
	Model      string
	Endpoint   string
	PromptPath string
	StageDir   string
	ScenarioID string
	Seed       int64

	Client  CerebrasClient
	Now     func() time.Time
	Timeout time.Duration
}

// CerebrasClient is the one-shot request seam used by the remote generator.
// Implementations must not retry requests.
type CerebrasClient interface {
	Complete(context.Context, CerebrasCompletionRequest) (CerebrasCompletionResponse, error)
}

type CerebrasCompletionRequest struct {
	Model               string
	Prompt              string
	Seed                int64
	MaxCompletionTokens int
}

type CerebrasCompletionResponse struct {
	Content string
}

// RemoteModelIdentity records the provider/model/API endpoint without treating
// a hosted model as a locally hashed file.
type RemoteModelIdentity struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
	Endpoint string `json:"endpoint"`
}

// CerebrasHTTPClient calls Cerebras Chat Completions once. It never retries,
// follows no redirects, and does not retain request or response bodies.
type CerebrasHTTPClient struct {
	APIKey   string
	Endpoint string
	Client   *http.Client
}

func (client CerebrasHTTPClient) Complete(ctx context.Context, request CerebrasCompletionRequest) (CerebrasCompletionResponse, error) {
	if strings.TrimSpace(client.APIKey) == "" {
		return CerebrasCompletionResponse{}, errors.New("cerebras API key is required")
	}
	endpoint, err := normalizedCerebrasEndpoint(client.Endpoint)
	if err != nil {
		return CerebrasCompletionResponse{}, err
	}
	payload := struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature         float64 `json:"temperature"`
		Seed                int64   `json:"seed"`
		MaxCompletionTokens int     `json:"max_completion_tokens"`
	}{
		Model:               request.Model,
		Stream:              false,
		Temperature:         cerebrasTemperature,
		Seed:                request.Seed,
		MaxCompletionTokens: request.MaxCompletionTokens,
	}
	payload.Messages = append(payload.Messages, struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: request.Prompt})
	encoded, err := json.Marshal(payload)
	if err != nil {
		return CerebrasCompletionResponse{}, fmt.Errorf("encode request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return CerebrasCompletionResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	httpClient := client.Client
	if httpClient == nil {
		httpClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return CerebrasCompletionResponse{}, fmt.Errorf("cerebras request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return CerebrasCompletionResponse{}, fmt.Errorf("cerebras rate limited (retry-after %q); not retrying", response.Header.Get("Retry-After"))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return CerebrasCompletionResponse{}, fmt.Errorf("cerebras request failed with HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCerebrasHTTPResponseBytes+1))
	if err != nil {
		return CerebrasCompletionResponse{}, fmt.Errorf("read Cerebras response: %w", err)
	}
	if len(body) > maxCerebrasHTTPResponseBytes {
		return CerebrasCompletionResponse{}, fmt.Errorf("Cerebras response exceeds %d bytes", maxCerebrasHTTPResponseBytes)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return CerebrasCompletionResponse{}, fmt.Errorf("decode Cerebras response: %w", err)
	}
	if len(decoded.Choices) != 1 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return CerebrasCompletionResponse{}, errors.New("Cerebras response has no single text completion")
	}
	return CerebrasCompletionResponse{Content: decoded.Choices[0].Message.Content}, nil
}

// GenerateCerebras sends exactly one candidate request and stages only a valid
// candidate bundle. It never retries and never writes credentials to disk.
func GenerateCerebras(root string, config CerebrasGenerateConfig) (Provenance, error) {
	if err := validateScenarioID(config.ScenarioID); err != nil {
		return Provenance{}, err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return Provenance{}, errors.New("generate-cerebras: CEREBRAS_API_KEY is required")
	}
	if config.Model == "" {
		config.Model = CerebrasGemmaModel
	}
	if config.Model != CerebrasGemmaModel {
		return Provenance{}, fmt.Errorf("generate-cerebras: model must be %q", CerebrasGemmaModel)
	}
	if config.PromptPath == "" {
		config.PromptPath = DefaultPromptPath
	}
	if config.StageDir == "" {
		return Provenance{}, errors.New("generate-cerebras: --stage is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Timeout == 0 {
		config.Timeout = cerebrasGenerationTimeout
	}
	if config.Timeout <= 0 {
		return Provenance{}, errors.New("generate-cerebras: timeout must be positive")
	}
	endpoint, err := normalizedCerebrasEndpoint(config.Endpoint)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: endpoint: %w", err)
	}
	if config.Client == nil {
		config.Client = CerebrasHTTPClient{APIKey: config.APIKey, Endpoint: endpoint}
	}

	root, err = absolutePath(root)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: resolve repository root: %w", err)
	}
	promptPath, err := resolvePath(root, config.PromptPath)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: resolve prompt: %w", err)
	}
	stageDir, err := resolvePath(root, config.StageDir)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: resolve staging directory: %w", err)
	}
	promptContent, promptIdentity, err := readPrompt(promptPath)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: prompt: %w", err)
	}
	schemas, schemaVersions, err := compileSchemas(root)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: compile schemas: %w", err)
	}
	_ = schemas
	promptInput, err := boundedPromptInput(root, promptContent, config.ScenarioID)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: build bounded prompt: %w", err)
	}
	if err := prepareEmptyStage(stageDir); err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: staging directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()
	completion, err := config.Client.Complete(ctx, CerebrasCompletionRequest{
		Model:               config.Model,
		Prompt:              string(promptInput),
		Seed:                config.Seed,
		MaxCompletionTokens: cerebrasCompletionTokenLimit,
	})
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: one-shot request: %w", err)
	}
	response, err := extractJSONObject([]byte(completion.Content))
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: model output: %w", err)
	}
	bundle, err := parseArtifactBundle(response)
	if err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: model output: %w", err)
	}
	if bundle.ScenarioID != config.ScenarioID {
		return Provenance{}, fmt.Errorf("generate-cerebras: model output scenario_id %q does not match requested %q", bundle.ScenarioID, config.ScenarioID)
	}
	if err := bundle.validateScenarioArtifact(); err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: model output: %w", err)
	}

	provenance := Provenance{
		Version:     remoteProvenanceVersion,
		GeneratedAt: config.Now().UTC().Format(time.RFC3339Nano),
		RemoteModel: &RemoteModelIdentity{
			Provider: "cerebras",
			ModelID:  config.Model,
			Endpoint: endpoint,
		},
		Prompt:            promptIdentity,
		ScenarioID:        config.ScenarioID,
		Seed:              config.Seed,
		RequestParameters: cerebrasRequestParameters(config.Model, endpoint, config.Seed, promptInput),
		PromptInputSHA256: sha256Hex(promptInput),
		RawResponseSHA256: sha256Hex(response),
		SchemaVersions:    schemaVersions,
	}
	if err := writeStage(stageDir, response, bundle, provenance); err != nil {
		return Provenance{}, fmt.Errorf("generate-cerebras: write staged candidate: %w", err)
	}
	return provenance, nil
}

func normalizedCerebrasEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		endpoint = defaultCerebrasEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "api.cerebras.ai" || parsed.Path != "/v1/chat/completions" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("must be https://api.cerebras.ai/v1/chat/completions")
	}
	return parsed.String(), nil
}

func cerebrasRequestParameters(model, endpoint string, seed int64, prompt []byte) []string {
	return []string{
		"provider=cerebras",
		"endpoint=" + endpoint,
		"model=" + model,
		fmt.Sprintf("temperature=%.1f", cerebrasTemperature),
		fmt.Sprintf("seed=%d", seed),
		fmt.Sprintf("max_completion_tokens=%d", cerebrasCompletionTokenLimit),
		"prompt=sha256:" + sha256Hex(prompt),
		"retry=false",
	}
}
