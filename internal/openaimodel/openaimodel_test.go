package openaimodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testSchemaDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "ontology"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertAuthoredSchema(t *testing.T, got any, filename string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(testSchemaDir(t), filename))
	if err != nil {
		t.Fatal(err)
	}
	var authored map[string]any
	if err := json.Unmarshal(contents, &authored); err != nil {
		t.Fatal(err)
	}
	actual, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("structured schema for %s = %#v, want object", filename, got)
	}
	if actual["$id"] != authored["$id"] {
		t.Fatalf("structured schema for %s has $id %q, want %q", filename, actual["$id"], authored["$id"])
	}
	assertStrictObjects(t, actual)
}

func assertStrictObjects(t *testing.T, value any) {
	t.Helper()
	switch node := value.(type) {
	case map[string]any:
		if properties, ok := node["properties"].(map[string]any); ok {
			if node["type"] != "object" || node["additionalProperties"] != false {
				t.Fatalf("strict object schema = %#v", node)
			}
			required, _ := node["required"].([]any)
			requiredSet := map[string]bool{}
			for _, item := range required {
				if name, ok := item.(string); ok {
					requiredSet[name] = true
				}
			}
			for name := range properties {
				if !requiredSet[name] {
					t.Fatalf("strict object schema did not require property %q: %#v", name, node)
				}
			}
		}
		for _, child := range node {
			assertStrictObjects(t, child)
		}
	case []any:
		for _, child := range node {
			assertStrictObjects(t, child)
		}
	}
}

func assertStrictFormat(t *testing.T, format map[string]any, name string) {
	t.Helper()
	if format["type"] != "json_schema" || format["name"] != name || format["strict"] != true {
		t.Fatalf("text.format = %#v", format)
	}
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
	client, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt",
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
	if body["instructions"] != "test Terra prompt" {
		t.Fatalf("instructions = %q", body["instructions"])
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
	assertStrictFormat(t, format, insightSchemaName)
	assertAuthoredSchema(t, format["schema"], "insight.schema.json")
}

func TestSolBriefMapsRecommendationAndShapesRequest(t *testing.T) {
	const recJSON = `{"schema_version":"1.0.0","recommendation_id":"recommendation-001","state_revision":7,"text":"review access","evidence":[{"target_kind":"insight","target_id":"insight-001","explanation":"assessment"}],"created_at":"2026-07-18T10:00:04Z"}`

	var capturedBody []byte
	client, err := NewSolClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Sol prompt",
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
	if body["instructions"] != "test Sol prompt" {
		t.Fatalf("instructions = %q", body["instructions"])
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
	assertStrictFormat(t, format, recommendationSchemaName)
	assertAuthoredSchema(t, format["schema"], "recommendation.schema.json")
}

func TestLunaNormalizeMapsResultAndOptionalCanonical(t *testing.T) {
	const payload = `{"result":{"schema_version":"1.0.0","luna_result_id":"luna-001","raw_event_id":"raw-001","status":"accepted","canonical_event_id":"canon-001","evidence":[{"target_kind":"raw_event","target_id":"raw-001","explanation":"envelope"}],"created_at":"2026-07-18T10:00:02Z"},"canonical_event":{"schema_version":"1.0.0","canonical_event_id":"canon-001","raw_event_id":"raw-001","event_type":"note","occurred_at":"2026-07-18T10:00:00Z","ingested_at":"2026-07-18T10:00:01Z","payload":{"summary":"synthetic"}}}`

	var capturedBody []byte
	client, err := NewLunaClient(Config{SchemaDir: testSchemaDir(t),
		Instructions: "test Luna prompt",
		APIKey:       "test-key",
		HTTPClient: testHTTPClient(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			capturedBody = body
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

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatal(err)
	}
	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	assertStrictFormat(t, format, lunaSchemaName)
	wrapper, _ := format["schema"].(map[string]any)
	if wrapper["type"] != "object" || wrapper["additionalProperties"] != false {
		t.Fatalf("Luna wrapper = %#v", wrapper)
	}
	// Wire schema is purpose-built for OpenAI strict mode: no authored $id,
	// single root $defs, nullable canonical_event.
	if _, hasID := wrapper["$id"]; hasID {
		t.Fatal("Luna wire schema must not carry an authored $id")
	}
	if _, hasDefs := wrapper["$defs"].(map[string]any); !hasDefs {
		t.Fatal("Luna wire schema requires a root $defs table")
	}
	assertStrictObjects(t, wrapper)
	properties, _ := wrapper["properties"].(map[string]any)
	result, _ := properties["result"].(map[string]any)
	if result["type"] != "object" {
		t.Fatalf("Luna result wire schema = %#v", result)
	}
	if _, hasAllOf := result["allOf"]; hasAllOf {
		t.Fatal("Luna result wire schema must not retain authored allOf")
	}
	canonical, _ := properties["canonical_event"].(map[string]any)
	anyOf, _ := canonical["anyOf"].([]any)
	if len(anyOf) != 2 {
		t.Fatalf("Luna canonical_event schema = %#v", canonical)
	}
	canonBody, _ := anyOf[0].(map[string]any)
	if _, hasAllOf := canonBody["allOf"]; hasAllOf {
		t.Fatal("canonical_event wire schema must not retain authored allOf")
	}
	if nullSchema, _ := anyOf[1].(map[string]any); nullSchema["type"] != "null" {
		t.Fatalf("Luna nullable canonical_event schema = %#v", anyOf[1])
	}
}

func TestStrictSchemaTransformsOptionalFieldsWithoutChangingAuthoredSchema(t *testing.T) {
	path := filepath.Join(testSchemaDir(t), "insight.schema.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := loadStructuredOutputSchema(testSchemaDir(t), insightSchemaRoute)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("authored schema was modified")
	}

	var document map[string]any
	if err := json.Unmarshal(schema.document, &document); err != nil {
		t.Fatal(err)
	}
	assertStrictObjects(t, document)
	properties, _ := document["properties"].(map[string]any)
	optional, _ := properties["supersedes_insight_id"].(map[string]any)
	types, _ := optional["type"].([]any)
	if len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("optional property was not nullable: %#v", optional)
	}
}

func TestStrictSchemaInfersTypeForConstAndEnumLeaves(t *testing.T) {
	authored := json.RawMessage(`{
		"type": "object",
		"required": ["schema_version", "level", "count", "flag", "mixed"],
		"properties": {
			"schema_version": {"const": "1.0.0"},
			"level": {"enum": ["low", "medium", "high"]},
			"count": {"const": 3},
			"flag": {"const": true},
			"mixed": {"enum": ["auto", 0]}
		}
	}`)
	strict, err := strictCompatibleSchema(authored)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(strict, &document); err != nil {
		t.Fatal(err)
	}
	props, _ := document["properties"].(map[string]any)
	cases := map[string]any{
		"schema_version": "string",
		"level":          "string",
		"count":          "integer",
		"flag":           "boolean",
	}
	for name, want := range cases {
		node, _ := props[name].(map[string]any)
		if node["type"] != want {
			t.Fatalf("%s type = %#v, want %q", name, node["type"], want)
		}
	}
	mixed, _ := props["mixed"].(map[string]any)
	types, _ := mixed["type"].([]any)
	if len(types) != 2 || types[0] != "string" || types[1] != "integer" {
		t.Fatalf("mixed enum type = %#v, want [\"string\",\"integer\"]", mixed["type"])
	}
}

// TestStrictSchemaLeavesNoUntypedConstOrEnum is the OpenAI-strict-mode regression
// guard: every const/enum node in each authored ontology output schema must carry
// a type on the wire, or the live Responses API rejects the request with HTTP 400
// invalid_json_schema (the bug that made all three live agents fail).
func TestStrictSchemaLeavesNoUntypedConstOrEnum(t *testing.T) {
	routes := []schemaRoute{insightSchemaRoute, recommendationSchemaRoute, lunaResultSchemaRoute}
	for _, route := range routes {
		schema, err := loadStructuredOutputSchema(testSchemaDir(t), route)
		if err != nil {
			t.Fatalf("load %s: %v", route.file, err)
		}
		var document any
		if err := json.Unmarshal(schema.document, &document); err != nil {
			t.Fatal(err)
		}
		if violation := findUntypedConstOrEnum(document, route.name); violation != "" {
			t.Fatalf("%s: %s", route.file, violation)
		}
	}
	// Luna wire schema is purpose-built (not loadStructuredOutputSchema).
	luna, err := loadLunaStructuredOutputSchema(testSchemaDir(t))
	if err != nil {
		t.Fatal(err)
	}
	var lunaDoc any
	if err := json.Unmarshal(luna.document, &lunaDoc); err != nil {
		t.Fatal(err)
	}
	if violation := findUntypedConstOrEnum(lunaDoc, luna.name); violation != "" {
		t.Fatalf("luna wire: %s", violation)
	}
}

// TestLunaWireSchemaIsOpenAIStrictCompatible guards the Parcel B design:
// no allOf/if/then/else, every $ref resolves inside the document, and every
// object/array/leaf that needs a type has one. Authored ontology files stay
// untouched.
func TestLunaWireSchemaIsOpenAIStrictCompatible(t *testing.T) {
	beforeLuna, err := os.ReadFile(filepath.Join(testSchemaDir(t), "luna-result.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	beforeCanon, err := os.ReadFile(filepath.Join(testSchemaDir(t), "canonical-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	schema, err := loadLunaStructuredOutputSchema(testSchemaDir(t))
	if err != nil {
		t.Fatal(err)
	}
	afterLuna, _ := os.ReadFile(filepath.Join(testSchemaDir(t), "luna-result.schema.json"))
	afterCanon, _ := os.ReadFile(filepath.Join(testSchemaDir(t), "canonical-event.schema.json"))
	if string(beforeLuna) != string(afterLuna) || string(beforeCanon) != string(afterCanon) {
		t.Fatal("authored ontology schemas were modified")
	}

	var document any
	if err := json.Unmarshal(schema.document, &document); err != nil {
		t.Fatal(err)
	}
	assertStrictObjects(t, document)
	if violation := findForbiddenStrictKeywords(document, "root"); violation != "" {
		t.Fatal(violation)
	}
	if violation := findUntypedSchemaNode(document, "root"); violation != "" {
		t.Fatal(violation)
	}
	defs := map[string]bool{}
	if root, ok := document.(map[string]any); ok {
		if raw, ok := root["$defs"].(map[string]any); ok {
			for name := range raw {
				defs[name] = true
			}
		}
	}
	if violation := findUnresolvedRef(document, "root", defs); violation != "" {
		t.Fatal(violation)
	}
}

func findForbiddenStrictKeywords(value any, path string) string {
	switch node := value.(type) {
	case map[string]any:
		for _, key := range []string{"allOf", "if", "then", "else", "not"} {
			if _, ok := node[key]; ok {
				return "forbidden keyword " + key + " at " + path
			}
		}
		for key, child := range node {
			if violation := findForbiddenStrictKeywords(child, path+"."+key); violation != "" {
				return violation
			}
		}
	case []any:
		for i, child := range node {
			if violation := findForbiddenStrictKeywords(child, fmt.Sprintf("%s[%d]", path, i)); violation != "" {
				return violation
			}
		}
	}
	return ""
}

// findUntypedSchemaNode reports schema objects that look like type constraints
// but omit "type" (and are not pure $ref / anyOf containers).
func findUntypedSchemaNode(value any, path string) string {
	switch node := value.(type) {
	case map[string]any:
		if _, hasRef := node["$ref"]; hasRef {
			return ""
		}
		if _, hasAnyOf := node["anyOf"]; hasAnyOf {
			for i, child := range node["anyOf"].([]any) {
				if violation := findUntypedSchemaNode(child, fmt.Sprintf("%s.anyOf[%d]", path, i)); violation != "" {
					return violation
				}
			}
			return ""
		}
		_, hasType := node["type"]
		_, hasProperties := node["properties"]
		_, hasItems := node["items"]
		_, hasConst := node["const"]
		_, hasEnum := node["enum"]
		if (hasProperties || hasItems || hasConst || hasEnum) && !hasType {
			return "untyped schema node at " + path
		}
		// Empty object with no type is invalid under strict mode.
		if len(node) == 0 {
			return "empty untyped schema node at " + path
		}
		for key, child := range node {
			if key == "$defs" || key == "required" || key == "enum" || key == "const" {
				continue
			}
			if violation := findUntypedSchemaNode(child, path+"."+key); violation != "" {
				return violation
			}
		}
	case []any:
		for i, child := range node {
			if violation := findUntypedSchemaNode(child, fmt.Sprintf("%s[%d]", path, i)); violation != "" {
				return violation
			}
		}
	}
	return ""
}

func findUnresolvedRef(value any, path string, defs map[string]bool) string {
	switch node := value.(type) {
	case map[string]any:
		if ref, ok := node["$ref"].(string); ok {
			const head = "#/$defs/"
			if !strings.HasPrefix(ref, head) {
				return "non-local $ref " + ref + " at " + path
			}
			name := strings.TrimPrefix(ref, head)
			if !defs[name] {
				return "unresolved $ref " + ref + " at " + path
			}
		}
		for key, child := range node {
			if violation := findUnresolvedRef(child, path+"."+key, defs); violation != "" {
				return violation
			}
		}
	case []any:
		for i, child := range node {
			if violation := findUnresolvedRef(child, fmt.Sprintf("%s[%d]", path, i), defs); violation != "" {
				return violation
			}
		}
	}
	return ""
}

func findUntypedConstOrEnum(value any, path string) string {
	switch node := value.(type) {
	case map[string]any:
		_, hasType := node["type"]
		_, hasConst := node["const"]
		_, hasEnum := node["enum"]
		if (hasConst || hasEnum) && !hasType {
			return "untyped const/enum node at " + path
		}
		for key, child := range node {
			if violation := findUntypedConstOrEnum(child, path+"."+key); violation != "" {
				return violation
			}
		}
	case []any:
		for i, child := range node {
			if violation := findUntypedConstOrEnum(child, fmt.Sprintf("%s[%d]", path, i)); violation != "" {
				return violation
			}
		}
	}
	return ""
}

func TestWithoutNullObjectProperties(t *testing.T) {
	got, err := withoutNullObjectProperties(json.RawMessage(`{"optional":null,"nested":{"remove":null,"keep":"value"},"rows":[{"remove":null,"original":null,"keep":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"nested":{"keep":"value"},"rows":[{"keep":1,"original":null}]}`
	if string(got) != want {
		t.Fatalf("normalized output = %s, want %s", got, want)
	}
}

func TestNormalizeArtifactEvidenceRefsStripsFullEvidenceIdentity(t *testing.T) {
	// Live Sol failure mode: model echoes full Evidence records into
	// recommendation.evidence; authored schema allows only evidence_ref fields.
	in := json.RawMessage(`{
		"schema_version":"1.0.0",
		"recommendation_id":"recommendation-001",
		"state_revision":9,
		"text":"Consider reviewing access.",
		"evidence":[{
			"schema_version":"1.0.0",
			"evidence_id":"evidence-001",
			"target_kind":"insight",
			"target_id":"insight-001",
			"explanation":"active access assessment",
			"created_at":"2026-07-21T12:00:00Z"
		}],
		"created_at":"2026-07-21T12:00:01Z"
	}`)
	got, err := normalizeArtifactEvidenceRefs(in)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatal(err)
	}
	arr, ok := root["evidence"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("evidence = %#v", root["evidence"])
	}
	item, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("evidence[0] = %#v", arr[0])
	}
	for _, banned := range []string{"evidence_id", "schema_version", "created_at"} {
		if _, exists := item[banned]; exists {
			t.Fatalf("evidence ref still has %q: %#v", banned, item)
		}
	}
	if item["target_kind"] != "insight" || item["target_id"] != "insight-001" || item["explanation"] != "active access assessment" {
		t.Fatalf("unexpected evidence ref: %#v", item)
	}
}

func TestEvidenceToWireRefsOmitsIdentityFields(t *testing.T) {
	refs := evidenceToWireRefs([]gen.Evidence{{
		SchemaVersion: "1.0.0",
		EvidenceID:    "evidence-001",
		TargetKind:    "insight",
		TargetID:      "insight-001",
		Explanation:   "active access assessment",
		JsonPointer:   "/assertions/0",
		CreatedAt:     "2026-07-21T12:00:00Z",
	}})
	if len(refs) != 1 {
		t.Fatalf("len = %d", len(refs))
	}
	if refs[0].TargetKind != "insight" || refs[0].TargetID != "insight-001" || refs[0].JSONPointer != "/assertions/0" {
		t.Fatalf("refs[0] = %#v", refs[0])
	}
	// Encode and ensure identity fields never appear on the wire.
	encoded, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"evidence_id", "schema_version", "created_at"} {
		if strings.Contains(string(encoded), banned) {
			t.Fatalf("wire refs contain %q: %s", banned, encoded)
		}
	}
}

func TestRefusalDetailPath(t *testing.T) {
	t.Run("terra", func(t *testing.T) {
		client, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt",
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
		client, err := NewSolClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Sol prompt",
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
		client, err := NewLunaClient(Config{SchemaDir: testSchemaDir(t),
			Instructions: "test Luna prompt",
			APIKey:       "test-key",
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
	client, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt",
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
			client, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt",
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
	if _, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt"}); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("NewTerraClient error = %v", err)
	}
	if _, err := NewSolClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Sol prompt", APIKey: "  "}); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("NewSolClient error = %v", err)
	}
	if _, err := NewLunaClient(Config{SchemaDir: testSchemaDir(t)}); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("NewLunaClient error = %v", err)
	}
}

func TestMissingInstructions(t *testing.T) {
	if _, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), APIKey: "test-key"}); err == nil || !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("NewTerraClient error = %v", err)
	}
	if _, err := NewSolClient(Config{SchemaDir: testSchemaDir(t), APIKey: "test-key"}); err == nil || !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("NewSolClient error = %v", err)
	}
	if _, err := NewLunaClient(Config{SchemaDir: testSchemaDir(t), APIKey: "test-key"}); err == nil || !strings.Contains(err.Error(), "instructions") {
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
	client, err := NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt",
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

	client, err = NewTerraClient(Config{SchemaDir: testSchemaDir(t), Instructions: "test Terra prompt",
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
