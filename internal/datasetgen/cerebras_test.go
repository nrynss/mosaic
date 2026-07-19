package datasetgen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fakeCerebrasClient struct {
	response CerebrasCompletionResponse
	err      error
	calls    int
	request  CerebrasCompletionRequest
}

func (client *fakeCerebrasClient) Complete(_ context.Context, request CerebrasCompletionRequest) (CerebrasCompletionResponse, error) {
	client.calls++
	client.request = request
	return client.response, client.err
}
func hasValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestGenerateCerebrasStagesInspectableRemoteCandidate(t *testing.T) {
	root := newTestRoot(t)
	stage := root + "/localmodels/staging/cerebras-candidate"
	local := generationConfig(t, root, stage, &fakeRunner{})
	bundle := frozenBundle(t, repositoryRoot(t), nil)
	client := &fakeCerebrasClient{response: CerebrasCompletionResponse{Content: string(bundle)}}

	provenance, err := GenerateCerebras(root, CerebrasGenerateConfig{
		APIKey:     "test-api-key",
		PromptPath: local.PromptPath,
		StageDir:   stage,
		ScenarioID: "domestic-disturbance",
		Seed:       42,
		Client:     client,
		Now:        func() time.Time { return time.Date(2026, 7, 20, 10, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
	if client.request.Model != CerebrasGemmaModel || client.request.Seed != 42 || client.request.MaxCompletionTokens != cerebrasCompletionTokenLimit {
		t.Fatalf("unexpected remote request: %#v", client.request)
	}
	if !strings.Contains(client.request.Prompt, "scenario_id: domestic-disturbance") || !strings.Contains(client.request.Prompt, "raw-event.schema.json") {
		t.Fatal("remote request omitted the bounded prompt input")
	}
	if provenance.Version != remoteProvenanceVersion || provenance.RemoteModel == nil || provenance.RemoteModel.Provider != "cerebras" || provenance.RemoteModel.ModelID != CerebrasGemmaModel {
		t.Fatalf("remote provenance identity is incomplete: %#v", provenance)
	}
	if provenance.Model != nil || provenance.LlamaExecutable != nil || len(provenance.CommandArgs) != 0 {
		t.Fatalf("remote provenance leaked local identity fields: %#v", provenance)
	}
	if !hasValue(provenance.RequestParameters, "retry=false") || !hasValue(provenance.RequestParameters, "prompt=sha256:"+sha256Hex([]byte(client.request.Prompt))) {
		t.Fatalf("remote request provenance is incomplete: %#v", provenance.RequestParameters)
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "test-api-key") {
		t.Fatal("remote provenance contains the API key")
	}
}

func TestGenerateCerebrasStopsAfterOneFailedRequest(t *testing.T) {
	root := newTestRoot(t)
	stage := root + "/localmodels/staging/cerebras-failure"
	local := generationConfig(t, root, stage, &fakeRunner{})
	client := &fakeCerebrasClient{err: errors.New("rate limit")}

	if _, err := GenerateCerebras(root, CerebrasGenerateConfig{
		APIKey:     "test-api-key",
		PromptPath: local.PromptPath,
		StageDir:   stage,
		ScenarioID: "domestic-disturbance",
		Seed:       42,
		Client:     client,
	}); err == nil || !strings.Contains(err.Error(), "one-shot request") {
		t.Fatalf("GenerateCerebras error = %v, want one-shot request failure", err)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
	if entries, err := os.ReadDir(stage); err != nil || len(entries) != 0 {
		t.Fatalf("failed remote request wrote staged artifacts: entries=%v err=%v", entries, err)
	}
}

func TestCerebrasHTTPClientDoesNotRetryRateLimit(t *testing.T) {
	calls := 0
	client := CerebrasHTTPClient{
		APIKey: "test-api-key",
		Client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.URL.String() != defaultCerebrasEndpoint {
				t.Fatalf("request URL = %q", request.URL)
			}
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"30"}},
				Body:       io.NopCloser(strings.NewReader("{}")),
				Request:    request,
			}, nil
		})},
	}
	_, err := client.Complete(context.Background(), CerebrasCompletionRequest{Model: CerebrasGemmaModel, Prompt: "synthetic only", MaxCompletionTokens: 1})
	if err == nil || !strings.Contains(err.Error(), "rate limited") || !strings.Contains(err.Error(), "not retrying") {
		t.Fatalf("Complete error = %v, want rate-limit no-retry error", err)
	}
	if calls != 1 {
		t.Fatalf("HTTP calls = %d, want 1", calls)
	}
}
