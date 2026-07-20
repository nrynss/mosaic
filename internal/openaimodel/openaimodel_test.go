package openaimodel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testHTTPClient(fn roundTripperFunc) *http.Client {
	return &http.Client{
		Transport: fn,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func successEnvelope(id, text string) string {
	payload := map[string]any{
		"id": id,
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{"type": "output_text", "text": text},
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func refusalEnvelope(id, detail string) string {
	payload := map[string]any{
		"id": id,
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{"type": "refusal", "refusal": detail},
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestTerraAssessMapsInsightAndShapesRequest(t *testing.T) {
	const insightJSON = `{"schema_version":"1.0.0","insight_id":"insight-001","state_revision":7,"lifecycle_status":"active","assertions":["access constrained"],"evidence":[{"target_kind":"canonical_event","target_id":"canon-001","explanation":"report"}],"confidence":{"source_quality":"medium","transformation_certainty":"high","reasoning_support":"medium","basis":"fixture"},"created_at":"2026-07-18T10:00:03Z"}`

	var captured *http.Request
	var capturedBody []byte
	client, err := NewTerraClient(Config{
		APIKey:   "test-key",
		Endpoint: "https://api.openai.com/v1/responses",
		Model:    "gpt-5.6",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			captured = request
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			capturedBody = body
			resp := jsonResponse(http.StatusOK, successEnvelope("resp_terra_1", insightJSON))
			resp.Request = request
			return resp, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Assess(context.Background(), terra.Request{
		StateRevision: 7,
		SerializedCOP: json.RawMessage(`{"status":"open"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ResponseID != "resp_terra_1" {
		t.Fatalf("ResponseID = %q", response.ResponseID)
	}
	if string(response.InsightJSON) != insightJSON {
		t.Fatalf("InsightJSON = %s", response.InsightJSON)
	}
	if response.RefusalDetail != "" {
		t.Fatalf("unexpected refusal: %q", response.RefusalDetail)
	}

	if captured == nil {
		t.Fatal("no HTTP request captured")
	}
	if captured.Method != http.MethodPost {
		t.Fatalf("method = %s", captured.Method)
	}
	if captured.URL.String() != DefaultEndpoint {
		t.Fatalf("URL = %q", captured.URL)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := captured.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-5.6" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["store"] != false {
		t.Fatalf("store = %v, want false", body["store"])
	}
	input, _ := body["input"].(string)
	if !strings.Contains(input, `"state_revision":7`) {
		t.Fatalf("input missing state_revision: %s", input)
	}
	if !strings.Contains(input, `"serialized_cop"`) {
		t.Fatalf("input missing serialized_cop: %s", input)
	}
	if !strings.Contains(input, `"evidence"`) {
		t.Fatalf("input missing evidence: %s", input)
	}
	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "insight" {
		t.Fatalf("text.format = %#v", format)
	}
}

func TestSolBriefMapsRecommendationAndShapesRequest(t *testing.T) {
	const recJSON = `{"schema_version":"1.0.0","recommendation_id":"recommendation-001","state_revision":7,"text":"review access","evidence":[{"target_kind":"insight","target_id":"insight-001","explanation":"assessment"}],"created_at":"2026-07-18T10:00:04Z"}`

	var capturedBody []byte
	client, err := NewSolClient(Config{
		APIKey: "test-key",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			capturedBody = body
			resp := jsonResponse(http.StatusOK, successEnvelope("resp_sol_1", recJSON))
			resp.Request = request
			return resp, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Brief(context.Background(), sol.Request{
		StateRevision: 7,
		SerializedCOP: json.RawMessage(`{"status":"open"}`),
		RequestedBy:   "operator-public",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ResponseID != "resp_sol_1" || string(response.RecommendationJSON) != recJSON {
		t.Fatalf("unexpected response: %#v", response)
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatal(err)
	}
	input, _ := body["input"].(string)
	if !strings.Contains(input, `"state_revision":7`) || !strings.Contains(input, `"requested_by":"operator-public"`) {
		t.Fatalf("sol input incomplete: %s", input)
	}
	if !strings.Contains(input, `"evidence"`) || !strings.Contains(input, `"insights"`) {
		t.Fatalf("sol input missing evidence/insights: %s", input)
	}
	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["name"] != "recommendation" {
		t.Fatalf("schema name = %v", format["name"])
	}
}

func TestLunaNormalizeMapsResultAndOptionalCanonical(t *testing.T) {
	const payload = `{"result":{"schema_version":"1.0.0","luna_result_id":"luna-001","raw_event_id":"raw-001","status":"accepted","canonical_event_id":"canon-001","evidence":[{"target_kind":"raw_event","target_id":"raw-001","explanation":"envelope"}],"created_at":"2026-07-18T10:00:02Z"},"canonical_event":{"schema_version":"1.0.0","canonical_event_id":"canon-001","raw_event_id":"raw-001","event_type":"note","occurred_at":"2026-07-18T10:00:00Z","ingested_at":"2026-07-18T10:00:01Z","payload":{"summary":"synthetic"}}}`

	client, err := NewLunaClient(Config{
		APIKey: "test-key",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
			}
			resp := jsonResponse(http.StatusOK, successEnvelope("resp_luna_1", payload))
			resp.Request = request
			return resp, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Normalize(context.Background(), LunaRequest{
		RawEventJSON: json.RawMessage(`{"schema_version":"1.0.0","raw_event_id":"raw-001"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ResponseID != "resp_luna_1" {
		t.Fatalf("ResponseID = %q", response.ResponseID)
	}
	if !strings.Contains(string(response.ResultJSON), `"luna_result_id":"luna-001"`) {
		t.Fatalf("ResultJSON = %s", response.ResultJSON)
	}
	if !strings.Contains(string(response.CanonicalEventJSON), `"canonical_event_id":"canon-001"`) {
		t.Fatalf("CanonicalEventJSON = %s", response.CanonicalEventJSON)
	}
}

func TestRefusalDetailPath(t *testing.T) {
	t.Run("terra", func(t *testing.T) {
		client, err := NewTerraClient(Config{
			APIKey: "test-key",
			HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
				resp := jsonResponse(http.StatusOK, refusalEnvelope("resp_refuse", "policy declined assessment"))
				resp.Request = request
				return resp, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Assess(context.Background(), terra.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if response.RefusalDetail != "policy declined assessment" || len(response.InsightJSON) != 0 || response.ResponseID != "resp_refuse" {
			t.Fatalf("unexpected refusal response: %#v", response)
		}
	})
	t.Run("sol", func(t *testing.T) {
		client, err := NewSolClient(Config{
			APIKey: "test-key",
			HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
				resp := jsonResponse(http.StatusOK, refusalEnvelope("resp_refuse_sol", "briefing refused"))
				resp.Request = request
				return resp, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Brief(context.Background(), sol.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`), RequestedBy: "op"})
		if err != nil {
			t.Fatal(err)
		}
		if response.RefusalDetail != "briefing refused" || len(response.RecommendationJSON) != 0 {
			t.Fatalf("unexpected refusal response: %#v", response)
		}
	})
	t.Run("luna", func(t *testing.T) {
		client, err := NewLunaClient(Config{
			APIKey: "test-key",
			HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
				resp := jsonResponse(http.StatusOK, refusalEnvelope("resp_refuse_luna", "cannot normalize"))
				resp.Request = request
				return resp, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Normalize(context.Background(), LunaRequest{RawEventJSON: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if response.RefusalDetail != "cannot normalize" || len(response.ResultJSON) != 0 {
			t.Fatalf("unexpected refusal response: %#v", response)
		}
	})
}

func TestContextCancelAndTimeout(t *testing.T) {
	client, err := NewTerraClient(Config{
		APIKey: "test-key",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			select {
			case <-request.Context().Done():
				return nil, request.Context().Err()
			case <-time.After(2 * time.Second):
				resp := jsonResponse(http.StatusOK, successEnvelope("late", `{"ok":true}`))
				resp.Request = request
				return resp, nil
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Assess(ctx, terra.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`)})
	if err == nil || !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancel error = %v", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.Assess(ctx, terra.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestHTTPErrorsNoRetry(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			client, err := NewTerraClient(Config{
				APIKey: "test-key",
				HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
					calls.Add(1)
					resp := jsonResponse(status, `{"error":{"message":"unavailable"}}`)
					if status == http.StatusTooManyRequests {
						resp.Header.Set("Retry-After", "30")
					}
					resp.Request = request
					return resp, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Assess(context.Background(), terra.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`)})
			if err == nil {
				t.Fatal("expected error")
			}
			if calls.Load() != 1 {
				t.Fatalf("HTTP calls = %d, want 1 (no retry)", calls.Load())
			}
			if status == http.StatusTooManyRequests {
				if !strings.Contains(err.Error(), "rate limited") || !strings.Contains(err.Error(), "not retrying") {
					t.Fatalf("rate limit error = %v", err)
				}
			} else if !strings.Contains(err.Error(), "HTTP") {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Fatalf("error leaked API key: %v", err)
			}
		})
	}
}

func TestMissingAPIKey(t *testing.T) {
	if _, err := NewTerraClient(Config{}); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("NewTerraClient error = %v", err)
	}
	if _, err := NewSolClient(Config{APIKey: "  "}); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("NewSolClient error = %v", err)
	}
	if _, err := NewLunaClient(Config{}); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("NewLunaClient error = %v", err)
	}
}

func TestSelectFixtureFallbackEmptyKey(t *testing.T) {
	fixtureLuna := stubLuna{id: "fixture-luna"}
	fixtureTerra := stubTerra{id: "fixture-terra"}
	fixtureSol := stubSol{id: "fixture-sol"}
	liveLuna := stubLuna{id: "live-luna"}
	liveTerra := stubTerra{id: "live-terra"}
	liveSol := stubSol{id: "live-sol"}

	clients, err := Select(SelectConfig{
		Selection: contracts.AgentProviderSelection{
			AgentLuna:  contracts.ProviderLive,
			AgentTerra: contracts.ProviderLive,
			AgentSol:   contracts.ProviderLive,
		},
		APIKey:       "", // force fixture
		LiveLuna:     liveLuna,
		LiveTerra:    liveTerra,
		LiveSol:      liveSol,
		FixtureLuna:  fixtureLuna,
		FixtureTerra: fixtureTerra,
		FixtureSol:   fixtureSol,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClientID(t, clients, "fixture-luna", "fixture-terra", "fixture-sol")
}

func TestSelectProviderFixtureEvenWithKey(t *testing.T) {
	clients, err := Select(SelectConfig{
		Selection: contracts.AgentProviderSelection{
			AgentLuna:  contracts.ProviderFixture,
			AgentTerra: contracts.ProviderFixture,
			AgentSol:   contracts.ProviderFixture,
		},
		APIKey:       "test-key",
		LiveLuna:     stubLuna{id: "live-luna"},
		LiveTerra:    stubTerra{id: "live-terra"},
		LiveSol:      stubSol{id: "live-sol"},
		FixtureLuna:  stubLuna{id: "fixture-luna"},
		FixtureTerra: stubTerra{id: "fixture-terra"},
		FixtureSol:   stubSol{id: "fixture-sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClientID(t, clients, "fixture-luna", "fixture-terra", "fixture-sol")
}

func TestSelectLiveWhenConfigured(t *testing.T) {
	clients, err := Select(SelectConfig{
		Selection: contracts.AgentProviderSelection{
			AgentLuna:  contracts.ProviderLive,
			AgentTerra: contracts.ProviderLive,
			AgentSol:   contracts.ProviderFixture,
		},
		APIKey:       "test-key",
		LiveLuna:     stubLuna{id: "live-luna"},
		LiveTerra:    stubTerra{id: "live-terra"},
		LiveSol:      stubSol{id: "live-sol"},
		FixtureLuna:  stubLuna{id: "fixture-luna"},
		FixtureTerra: stubTerra{id: "fixture-terra"},
		FixtureSol:   stubSol{id: "fixture-sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClientID(t, clients, "live-luna", "live-terra", "fixture-sol")
}

func TestSelectDefaultNilSelectionUsesFixture(t *testing.T) {
	clients, err := Select(SelectConfig{
		FixtureLuna:  stubLuna{id: "fixture-luna"},
		FixtureTerra: stubTerra{id: "fixture-terra"},
		FixtureSol:   stubSol{id: "fixture-sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClientID(t, clients, "fixture-luna", "fixture-terra", "fixture-sol")
}

func TestSelectMissingFixtureErrors(t *testing.T) {
	_, err := Select(SelectConfig{
		APIKey: "test-key",
		Selection: contracts.AgentProviderSelection{
			AgentTerra: contracts.ProviderFixture,
		},
		FixtureLuna: stubLuna{id: "fixture-luna"},
		FixtureSol:  stubSol{id: "fixture-sol"},
	})
	if err == nil || !strings.Contains(err.Error(), "fixture terra") {
		t.Fatalf("error = %v", err)
	}
}

func TestAPIKeyFromEnv(t *testing.T) {
	t.Setenv(envAPIKey, "  test-key  ")
	if got := APIKeyFromEnv(); got != "test-key" {
		t.Fatalf("APIKeyFromEnv = %q", got)
	}
	t.Setenv(envAPIKey, "")
	if got := APIKeyFromEnv(); got != "" {
		t.Fatalf("APIKeyFromEnv empty = %q", got)
	}
}

func TestPackageSourceHasNoSecretPatterns(t *testing.T) {
	// Grep-proof: no OpenAI-style key prefixes or assigned env secrets in sources.
	// Tests may use the non-secret literal "test-key" only.
	keyPrefix := "sk" + string('-')
	envAssign := envAPIKey + "="
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(".", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if strings.Contains(text, keyPrefix) {
			t.Fatalf("%s contains secret-like key prefix", entry.Name())
		}
		if strings.Contains(text, envAssign) {
			t.Fatalf("%s contains disallowed secret assignment", entry.Name())
		}
	}
}

func TestEmptyAndInvalidBodies(t *testing.T) {
	client, err := NewTerraClient(Config{
		APIKey: "test-key",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			resp := jsonResponse(http.StatusOK, "   ")
			resp.Request = request
			return resp, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Assess(context.Background(), terra.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`)}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty body error = %v", err)
	}

	client, err = NewTerraClient(Config{
		APIKey: "test-key",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			resp := jsonResponse(http.StatusOK, `{"id":"resp_x","output":[{"type":"message","content":[{"type":"output_text","text":"not-json"}]}]}`)
			resp.Request = request
			return resp, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Assess(context.Background(), terra.Request{StateRevision: 1, SerializedCOP: json.RawMessage(`{}`)}); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("invalid JSON error = %v", err)
	}
}

type stubLuna struct{ id string }

func (s stubLuna) Normalize(context.Context, LunaRequest) (LunaResponse, error) {
	return LunaResponse{ResponseID: s.id}, nil
}

type stubTerra struct{ id string }

func (s stubTerra) Assess(context.Context, terra.Request) (terra.Response, error) {
	return terra.Response{ResponseID: s.id}, nil
}

type stubSol struct{ id string }

func (s stubSol) Brief(context.Context, sol.Request) (sol.Response, error) {
	return sol.Response{ResponseID: s.id}, nil
}

func assertClientID(t *testing.T, clients Clients, lunaID, terraID, solID string) {
	t.Helper()
	lunaResp, err := clients.Luna.Normalize(context.Background(), LunaRequest{RawEventJSON: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	terraResp, err := clients.Terra.Assess(context.Background(), terra.Request{})
	if err != nil {
		t.Fatal(err)
	}
	solResp, err := clients.Sol.Brief(context.Background(), sol.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if lunaResp.ResponseID != lunaID || terraResp.ResponseID != terraID || solResp.ResponseID != solID {
		t.Fatalf("client ids = luna:%q terra:%q sol:%q want %q %q %q",
			lunaResp.ResponseID, terraResp.ResponseID, solResp.ResponseID, lunaID, terraID, solID)
	}
}
