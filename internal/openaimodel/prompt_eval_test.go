package openaimodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mosaic.local/mosaic/internal/luna"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/terra"
)

// These are intentional prompt-eval baselines. A prompt edit must update its
// semantic fixture expectations and this hash in the same reviewed change.
var promptEvalHashes = map[string]string{
	AgentLuna:  "c08e5460c76db1833a5be6f23a2ac6f60915bb00e11f24ef8add7b770d1864d4",
	AgentTerra: "da9e7449a49dc6dc4b4bcde844989ee93ba1b67bd1196a3c8b30685b7635a7cc",
	AgentSol:   "293cfa720c5885099060d3000f98eb4eb56c3a5d716432855b64769bb82fb378",
}

type promptEvalFixture struct {
	RawEvents       []gen.RawEvent       `json:"raw_events"`
	LunaResults     []gen.LunaResult     `json:"luna_results"`
	Canonical       []gen.CanonicalEvent `json:"canonical_events"`
	Insights        []gen.Insight        `json:"insights"`
	Recommendations []gen.Recommendation `json:"recommendations"`
}

type promptEvalCapture struct {
	body []byte
}

func (c *promptEvalCapture) client(response string) *http.Client {
	return testHTTPClient(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		c.body = append([]byte(nil), body...)
		result := jsonResponse(http.StatusOK, response)
		result.Request = request
		return result, nil
	})
}

func TestPromptEvalHarness(t *testing.T) {
	root := promptEvalRoot(t)
	fixture := loadPromptEvalFixture(t, root)
	timeline := runPromptEvalScenario(t, root)

	lunaValidator, err := luna.LoadSchemaValidator(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatal(err)
	}
	terraValidator, err := terra.LoadSchemaValidator(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatal(err)
	}
	solValidator, err := sol.LoadSchemaValidator(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatal(err)
	}

	prompts := map[string]string{
		AgentLuna:  loadPromptEvalPrompt(t, root, AgentLuna),
		AgentTerra: loadPromptEvalPrompt(t, root, AgentTerra),
		AgentSol:   loadPromptEvalPrompt(t, root, AgentSol),
	}

	for _, test := range []struct {
		name          string
		rawID         string
		wantStatus    string
		wantCanonical string
	}{
		{name: "accepted", rawID: "raw-domestic-001-call", wantStatus: "accepted", wantCanonical: "canonical-domestic-001-call"},
		{name: "repaired", rawID: "raw-domestic-007-incomplete-road", wantStatus: "repaired", wantCanonical: "canonical-domestic-007-repaired-road"},
		{name: "quarantined", rawID: "raw-domestic-008-invalid-input", wantStatus: "quarantined"},
	} {
		t.Run("luna/"+test.name, func(t *testing.T) {
			raw := fixture.rawEvent(t, test.rawID)
			result := fixture.lunaResult(t, test.rawID)
			canonical := fixture.canonicalEvent(test.wantCanonical)
			payload := marshalPromptEvalJSON(t, map[string]any{"result": result, "canonical_event": canonical})
			capture := &promptEvalCapture{}
			client, err := NewLunaClient(Config{
				APIKey: "fixture-only-key", SchemaDir: filepath.Join(root, "ontology"), Instructions: prompts[AgentLuna], HTTPClient: capture.client(successEnvelope("prompt-eval-luna-"+test.name, string(payload))),
			})
			if err != nil {
				t.Fatal(err)
			}
			rawJSON := marshalPromptEvalJSON(t, raw)
			response, err := client.Normalize(context.Background(), LunaRequest{RawEventJSON: rawJSON})
			if err != nil {
				t.Fatal(err)
			}
			assertPromptEvalWire(t, capture.body, prompts[AgentLuna], lunaSchemaName, "luna-result.schema.json")
			assertPromptEvalLunaInput(t, capture.body, raw.RawEventID)

			var got gen.LunaResult
			if err := json.Unmarshal(response.ResultJSON, &got); err != nil {
				t.Fatal(err)
			}
			if err := lunaValidator.ValidateLunaResult(got); err != nil {
				t.Fatalf("validate replayed Luna result: %v", err)
			}
			if got.RawEventID != raw.RawEventID || got.Status != test.wantStatus || got.LunaResultID != result.LunaResultID {
				t.Fatalf("Luna semantics = %#v", got)
			}
			if test.wantStatus == "repaired" && got.Repair == nil {
				t.Fatal("repaired result has no repair record")
			}
			if test.wantStatus == "quarantined" && (got.Reason == "" || len(response.CanonicalEventJSON) != 0) {
				t.Fatalf("quarantined response must retain a reason and no canonical event: %#v", response)
			}
			if test.wantCanonical == "" {
				return
			}
			var canonicalOut gen.CanonicalEvent
			if err := json.Unmarshal(response.CanonicalEventJSON, &canonicalOut); err != nil {
				t.Fatal(err)
			}
			if err := lunaValidator.ValidateCanonicalEvent(canonicalOut); err != nil {
				t.Fatalf("validate replayed canonical event: %v", err)
			}
			if canonicalOut.CanonicalEventID != test.wantCanonical || canonicalOut.RawEventID != raw.RawEventID {
				t.Fatalf("canonical semantics = %#v", canonicalOut)
			}
		})
	}

	active := fixture.insight(t, "insight-domestic-access-001")
	obsolete := fixture.insight(t, "insight-domestic-access-001-obsolete")
	for _, test := range []struct {
		name      string
		insight   gen.Insight
		revision  int64
		lifecycle string
	}{
		{name: "access-at-revision-7", insight: active, revision: 7, lifecycle: "active"},
		{name: "obsolescence-at-revision-9", insight: obsolete, revision: 9, lifecycle: "obsolete"},
	} {
		t.Run("terra/"+test.name, func(t *testing.T) {
			evidence := promptEvalEvidence(t, test.insight.Evidence, test.insight.CreatedAt, test.insight.InsightID)
			capture := &promptEvalCapture{}
			client, err := NewTerraClient(Config{
				APIKey: "fixture-only-key", SchemaDir: filepath.Join(root, "ontology"), Instructions: prompts[AgentTerra], HTTPClient: capture.client(successEnvelope("prompt-eval-terra-"+fmt.Sprint(test.revision), string(marshalPromptEvalJSON(t, test.insight)))),
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Assess(context.Background(), terra.Request{StateRevision: test.revision, SerializedCOP: marshalPromptEvalJSON(t, timeline[test.revision]), Evidence: evidence})
			if err != nil {
				t.Fatal(err)
			}
			assertPromptEvalWire(t, capture.body, prompts[AgentTerra], insightSchemaName, "insight.schema.json")
			assertPromptEvalRevision(t, capture.body, test.revision)

			var got gen.Insight
			if err := json.Unmarshal(response.InsightJSON, &got); err != nil {
				t.Fatal(err)
			}
			if err := terraValidator.ValidateInsight(got); err != nil {
				t.Fatalf("validate replayed Terra insight: %v", err)
			}
			if got.InsightID != test.insight.InsightID || got.StateRevision != test.revision || got.LifecycleStatus != test.lifecycle || len(got.Evidence) != len(evidence) {
				t.Fatalf("Terra semantics = %#v", got)
			}
			if test.lifecycle == "active" && !strings.Contains(strings.ToLower(fmt.Sprint(got.Assertions)), "brook lane") {
				t.Fatalf("active access insight lost Brook Lane semantics: %#v", got.Assertions)
			}
			if test.lifecycle == "obsolete" && got.SupersedesInsightID != active.InsightID || test.lifecycle == "obsolete" && got.ObsoleteReason == "" {
				t.Fatalf("obsolete insight lifecycle semantics = %#v", got)
			}
		})
	}

	t.Run("sol/recommendation-at-revision-7", func(t *testing.T) {
		recommendation := fixture.recommendation(t, "recommendation-domestic-001")
		evidence := promptEvalEvidence(t, recommendation.Evidence, recommendation.CreatedAt, recommendation.RecommendationID)
		capture := &promptEvalCapture{}
		client, err := NewSolClient(Config{
			APIKey: "fixture-only-key", SchemaDir: filepath.Join(root, "ontology"), Instructions: prompts[AgentSol], HTTPClient: capture.client(successEnvelope("prompt-eval-sol-7", string(marshalPromptEvalJSON(t, recommendation)))),
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Brief(context.Background(), sol.Request{StateRevision: 7, SerializedCOP: marshalPromptEvalJSON(t, timeline[7]), Insights: []gen.Insight{active}, Evidence: evidence, RequestedBy: "supervisor-demo"})
		if err != nil {
			t.Fatal(err)
		}
		assertPromptEvalWire(t, capture.body, prompts[AgentSol], recommendationSchemaName, "recommendation.schema.json")
		assertPromptEvalRevision(t, capture.body, 7)

		var got gen.Recommendation
		if err := json.Unmarshal(response.RecommendationJSON, &got); err != nil {
			t.Fatal(err)
		}
		if err := solValidator.ValidateRecommendation(got); err != nil {
			t.Fatalf("validate replayed Sol recommendation: %v", err)
		}
		if got.RecommendationID != recommendation.RecommendationID || got.StateRevision != 7 || !strings.HasPrefix(got.Text, "Consider") || len(got.Evidence) != 1 {
			t.Fatalf("Sol semantics = %#v", got)
		}
		if reference := promptEvalReference(t, got.Evidence[0]); reference.TargetKind != "insight" || reference.TargetID != active.InsightID {
			t.Fatalf("Sol evidence does not remain bound to the active insight: %#v", reference)
		}
		if strings.Contains(strings.ToLower(got.Text), "dispatch") {
			t.Fatalf("Sol recommendation must remain non-operational: %q", got.Text)
		}
	})
}

func promptEvalRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadPromptEvalPrompt(t *testing.T, root, agent string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "prompts", agent, "v1.0.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	if got := hex.EncodeToString(sum[:]); got != promptEvalHashes[agent] {
		t.Fatalf("%s prompt hash = %s; update its semantic eval baseline intentionally", agent, got)
	}
	instructions := strings.TrimSpace(string(contents))
	if instructions == "" {
		t.Fatalf("%s prompt is empty", agent)
	}
	return instructions
}

func loadPromptEvalFixture(t *testing.T, root string) promptEvalFixture {
	t.Helper()
	var raw struct {
		RawEvents []gen.RawEvent `json:"raw_events"`
	}
	decodePromptEvalFile(t, filepath.Join(root, "datasets", simulator.DomesticDisturbance, "raw-events.json"), &raw)
	var outcomes promptEvalFixture
	decodePromptEvalFile(t, filepath.Join(root, "datasets", simulator.DomesticDisturbance, "expected-outcomes.json"), &outcomes)
	outcomes.RawEvents = raw.RawEvents
	return outcomes
}

func decodePromptEvalFile(t *testing.T, path string, destination any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func runPromptEvalScenario(t *testing.T, root string) map[int64]map[string]any {
	t.Helper()
	database, err := store.OpenInMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service, err := simulator.New(simulator.Config{Store: database, SchemaDir: filepath.Join(root, "ontology"), FixtureDir: filepath.Join(root, "datasets", simulator.DomesticDisturbance)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	timeline := make(map[int64]map[string]any, len(result.Timeline))
	for _, entry := range result.Timeline {
		if entry.StateRevision > 0 {
			timeline[entry.StateRevision] = entry.COP
		}
	}
	for _, revision := range []int64{7, 9} {
		if timeline[revision] == nil {
			t.Fatalf("fixture scenario omitted COP revision %d", revision)
		}
	}
	return timeline
}

func (f promptEvalFixture) rawEvent(t *testing.T, id string) gen.RawEvent {
	t.Helper()
	for _, value := range f.RawEvents {
		if value.RawEventID == id {
			return value
		}
	}
	t.Fatalf("fixture raw event %q is missing", id)
	return gen.RawEvent{}
}

func (f promptEvalFixture) lunaResult(t *testing.T, rawID string) gen.LunaResult {
	t.Helper()
	for _, value := range f.LunaResults {
		if value.RawEventID == rawID {
			return value
		}
	}
	t.Fatalf("fixture Luna result for %q is missing", rawID)
	return gen.LunaResult{}
}

func (f promptEvalFixture) canonicalEvent(id string) any {
	if id == "" {
		return nil
	}
	for _, value := range f.Canonical {
		if value.CanonicalEventID == id {
			return value
		}
	}
	return nil
}

func (f promptEvalFixture) insight(t *testing.T, id string) gen.Insight {
	t.Helper()
	for _, value := range f.Insights {
		if value.InsightID == id {
			return value
		}
	}
	t.Fatalf("fixture insight %q is missing", id)
	return gen.Insight{}
}

func (f promptEvalFixture) recommendation(t *testing.T, id string) gen.Recommendation {
	t.Helper()
	for _, value := range f.Recommendations {
		if value.RecommendationID == id {
			return value
		}
	}
	t.Fatalf("fixture recommendation %q is missing", id)
	return gen.Recommendation{}
}

func marshalPromptEvalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func promptEvalEvidence(t *testing.T, values []any, createdAt, ownerID string) []gen.Evidence {
	t.Helper()
	evidence := make([]gen.Evidence, 0, len(values))
	for index, value := range values {
		ref := promptEvalReference(t, value)
		evidence = append(evidence, gen.Evidence{
			SchemaVersion: "1.0.0", EvidenceID: fmt.Sprintf("prompt-eval-%s-%d", ownerID, index+1), TargetKind: ref.TargetKind, TargetID: ref.TargetID, JsonPointer: ref.JSONPointer, Explanation: ref.Explanation, CreatedAt: createdAt,
		})
	}
	return evidence
}

type promptEvalEvidenceReference struct {
	TargetKind  string `json:"target_kind"`
	TargetID    string `json:"target_id"`
	JSONPointer string `json:"json_pointer"`
	Explanation string `json:"explanation"`
}

func promptEvalReference(t *testing.T, value any) promptEvalEvidenceReference {
	t.Helper()
	encoded := marshalPromptEvalJSON(t, value)
	var reference promptEvalEvidenceReference
	if err := json.Unmarshal(encoded, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.TargetKind == "" || reference.TargetID == "" || reference.Explanation == "" {
		t.Fatalf("incomplete fixture evidence reference: %#v", reference)
	}
	return reference
}

func assertPromptEvalWire(t *testing.T, body []byte, prompt, schemaName, schemaFile string) {
	t.Helper()
	var request struct {
		Instructions string `json:"instructions"`
		Input        string `json:"input"`
		Text         struct {
			Format map[string]any `json:"format"`
		} `json:"text"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Instructions != prompt {
		t.Fatal("wire request did not use the versioned prompt artifact")
	}
	assertStrictFormat(t, request.Text.Format, schemaName)
	if schemaName == lunaSchemaName {
		wrapper, ok := request.Text.Format["schema"].(map[string]any)
		if !ok {
			t.Fatalf("Luna structured schema = %#v", request.Text.Format["schema"])
		}
		assertStrictObjects(t, wrapper)
		properties, _ := wrapper["properties"].(map[string]any)
		assertAuthoredSchema(t, properties["result"], schemaFile)
		return
	}
	assertAuthoredSchema(t, request.Text.Format["schema"], schemaFile)
}

func assertPromptEvalLunaInput(t *testing.T, body []byte, rawEventID string) {
	t.Helper()
	var request struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	var raw gen.RawEvent
	if err := json.Unmarshal([]byte(request.Input), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.RawEventID != rawEventID {
		t.Fatalf("Luna input raw_event_id = %q, want %q", raw.RawEventID, rawEventID)
	}
}

func assertPromptEvalRevision(t *testing.T, body []byte, want int64) {
	t.Helper()
	var request struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	var input struct {
		StateRevision int64 `json:"state_revision"`
	}
	if err := json.Unmarshal([]byte(request.Input), &input); err != nil {
		t.Fatal(err)
	}
	if input.StateRevision != want {
		t.Fatalf("wire state revision = %d, want %d", input.StateRevision, want)
	}
}
