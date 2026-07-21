package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

var operatorTestIDSeq atomic.Uint64

// --- model adapter stubs (no network) ---

type stubTerra struct {
	output contracts.TerraOutput
	err    error
	calls  int
	last   contracts.TerraInput
}

func (s *stubTerra) Assess(_ context.Context, input contracts.TerraInput) (contracts.TerraOutput, error) {
	s.calls++
	s.last = input
	return s.output, s.err
}

type stubSol struct {
	output contracts.SolOutput
	err    error
	calls  int
	last   contracts.SolInput
}

func (s *stubSol) Brief(_ context.Context, input contracts.SolInput) (contracts.SolOutput, error) {
	s.calls++
	s.last = input
	return s.output, s.err
}

type stubLuna struct {
	output contracts.LunaOutput
	err    error
	calls  int
	last   gen.RawEvent
}

func (s *stubLuna) Normalize(_ context.Context, raw gen.RawEvent) (contracts.LunaOutput, error) {
	s.calls++
	s.last = raw
	return s.output, s.err
}

func newOperatorServer(t *testing.T, base apiFixture, cfg Config) *Server {
	t.Helper()
	if cfg.Recovery == nil {
		cfg.Recovery = base.server.recovery
	}
	if cfg.Records == nil {
		cfg.Records = base.store
	}
	if cfg.Evidence == nil {
		cfg.Evidence = base.server.evidence
	}
	if cfg.Operations == nil {
		cfg.Operations = base.server.operations
	}
	if cfg.Stream == nil {
		cfg.Stream = base.broker
	}
	if cfg.Clock == nil {
		cfg.Clock = base.server.clock
	}
	if cfg.NewID == nil {
		// Globally unique within the package so shared fixture stores never
		// collide on audit_record_id across subtests or table cases.
		cfg.NewID = func() string {
			return fmt.Sprintf("op-%d", operatorTestIDSeq.Add(1))
		}
	}
	if cfg.Actors == nil {
		cfg.Actors = PublicActorResolver{
			ViewerIdentity:     "viewer-token",
			SupervisorIdentity: "supervisor-token",
		}
	}
	server, err := New(cfg)
	if err != nil {
		t.Fatalf("new operator server: %v", err)
	}
	return server
}

func validModelRun(id, agent, status string) gen.ModelRun {
	return gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          id,
		Agent:               agent,
		Provider:            "fixture",
		Model:               "fixture-model",
		PromptVersion:       "v1",
		OutputSchemaVersion: "1.0.0",
		ValidationStatus:    status,
		StartedAt:           "2026-07-18T12:02:00Z",
		CompletedAt:         "2026-07-18T12:02:01Z",
		StateRevision:       1,
	}
}

func TestOperatorAnalyzeSuccess(t *testing.T) {
	base := newFixture(t)
	terraStub := &stubTerra{
		output: contracts.TerraOutput{
			Insight: gen.Insight{
				SchemaVersion:   "1.0.0",
				InsightID:       "insight-analyze-1",
				StateRevision:   1,
				LifecycleStatus: "active",
				CreatedAt:       "2026-07-18T12:02:01Z",
				Evidence: []any{
					map[string]any{"target_kind": "canonical_event", "target_id": "canonical-1"},
				},
			},
			ModelRun: validModelRun("run-analyze-ok", "terra", "valid"),
		},
	}
	server := newOperatorServer(t, base, Config{
		Terra: terraStub,
		ProviderSelection: contracts.AgentProviderSelection{
			"terra": contracts.ProviderFixture,
		},
	})
	before := durableCounts(t, base.store)

	body := `{"evidence":[{"kind":"canonical_event","id":"canonical-1"}],"note":"analyze now"}`
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/analyze", "", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("analyze status = %d, body = %s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if executed, _ := data["executed"].(bool); executed {
		t.Fatal("analyze claimed operational execution")
	}
	if data["status"] != "ok" {
		t.Fatalf("status = %#v, want ok", data["status"])
	}
	insight, _ := data["insight"].(map[string]any)
	if insight["insight_id"] != "insight-analyze-1" {
		t.Fatalf("insight = %#v", insight)
	}
	run, _ := data["model_run"].(map[string]any)
	if run["model_run_id"] != "run-analyze-ok" || run["validation_status"] != "valid" {
		t.Fatalf("model_run = %#v", run)
	}
	providers, _ := data["providers"].(map[string]any)
	if providers["terra"] != "fixture" {
		t.Fatalf("providers = %#v", providers)
	}
	assertAuditActor(t, data, "public-demo", "viewer")
	audit, _ := data["audit_record"].(map[string]any)
	if audit["action"] != "noted" || audit["target_kind"] != "system" {
		t.Fatalf("audit = %#v", audit)
	}
	if terraStub.calls != 1 {
		t.Fatalf("terra calls = %d", terraStub.calls)
	}
	if terraStub.last.StateRevision != 1 {
		t.Fatalf("terra state revision = %d", terraStub.last.StateRevision)
	}
	if len(terraStub.last.Evidence) != 1 || terraStub.last.Evidence[0].TargetID != "canonical-1" {
		t.Fatalf("terra evidence = %#v", terraStub.last.Evidence)
	}
	// Adapter stubs do not persist model runs; only the operator audit is written.
	if got := tableCount(t, base.store, "audit_records"); got != before["audit_records"]+1 {
		t.Fatalf("audit_records = %d, want %d", got, before["audit_records"]+1)
	}
	if got := tableCount(t, base.store, "checkpoints"); got != before["checkpoints"] {
		t.Fatalf("analyze mutated checkpoints: %d", got)
	}
}

func TestOperatorAnalyzeRefusalAndFailure(t *testing.T) {
	base := newFixture(t)

	t.Run("refused", func(t *testing.T) {
		run := validModelRun("run-analyze-refused", "terra", "refused")
		run.FailureDetail = "policy declined assessment"
		terraStub := &stubTerra{
			output: contracts.TerraOutput{ModelRun: run},
			err:    terra.ErrAssessmentRefused,
		}
		server := newOperatorServer(t, base, Config{Terra: terraStub})
		beforeCheckpoints := tableCount(t, base.store, "checkpoints")
		beforeInsights := tableCount(t, base.store, "insights")

		resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/analyze", "", `{"evidence":[]}`)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
		}
		data := responseData(t, resp)
		if data["status"] != "refused" {
			t.Fatalf("status = %#v", data["status"])
		}
		if data["insight"] != nil {
			t.Fatalf("refusal returned insight: %#v", data["insight"])
		}
		if executed, _ := data["executed"].(bool); executed {
			t.Fatal("refusal claimed execution")
		}
		runPayload, _ := data["model_run"].(map[string]any)
		if runPayload["validation_status"] != "refused" {
			t.Fatalf("model_run = %#v", runPayload)
		}
		if tableCount(t, base.store, "checkpoints") != beforeCheckpoints {
			t.Fatal("refusal mutated COP checkpoints")
		}
		if tableCount(t, base.store, "insights") != beforeInsights {
			t.Fatal("refusal created an insight")
		}
	})

	t.Run("failed", func(t *testing.T) {
		run := validModelRun("run-analyze-failed", "terra", "failed")
		run.FailureDetail = "upstream unavailable"
		terraStub := &stubTerra{
			output: contracts.TerraOutput{ModelRun: run},
			err:    terra.ErrAssessmentFailed,
		}
		server := newOperatorServer(t, base, Config{Terra: terraStub})
		resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/analyze", "", `{}`)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
		}
		data := responseData(t, resp)
		if data["status"] != "failed" {
			t.Fatalf("status = %#v", data["status"])
		}
		if data["insight"] != nil {
			t.Fatalf("failure returned insight: %#v", data["insight"])
		}
	})
}

func TestOperatorBriefSuccessWithSupervisor(t *testing.T) {
	base := newFixture(t)
	// Seed a full Insight so brief hydrates from advisory history by id only.
	seedInsight := gen.Insight{
		SchemaVersion:   "1.0.0",
		InsightID:       "insight-1",
		StateRevision:   1,
		LifecycleStatus: "active",
		Assertions:      []any{"Access may be constrained."},
		Evidence: []any{
			map[string]any{
				"target_kind": "canonical_event",
				"target_id":   "canonical-1",
				"explanation": "Cited synthetic event.",
			},
		},
		Confidence: []byte(`{"source_quality":"medium","transformation_certainty":"medium","reasoning_support":"high","basis":"Fixture assessment."}`),
		CreatedAt:  "2026-07-18T12:01:30Z",
	}
	if err := base.store.AppendInsight(context.Background(), seedInsight); err != nil {
		t.Fatalf("seed insight: %v", err)
	}
	solStub := &stubSol{
		output: contracts.SolOutput{
			Recommendation: gen.Recommendation{
				SchemaVersion:    "1.0.0",
				RecommendationID: "rec-brief-1",
				StateRevision:    1,
				Text:             "Consider staged support options.",
				CreatedAt:        "2026-07-18T12:02:02Z",
			},
			ModelRun: validModelRun("run-brief-ok", "sol", "valid"),
		},
	}
	server := newOperatorServer(t, base, Config{
		Sol:               solStub,
		BriefingRequester: "supervisor-token",
		// Store implements AdvisoryHistoryReader — brief hydrates insights from it.
		AdvisoryHistory: base.store,
		ProviderSelection: contracts.AgentProviderSelection{
			"sol": contracts.ProviderLive,
		},
	})

	// Client only supplies insight_id (and optional evidence refs). Full fields
	// come from the store, not the request body.
	body := `{"insights":[{"insight_id":"insight-1"}],"evidence":[{"kind":"canonical_event","id":"canonical-1"}],"note":"deep brief"}`
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/brief", "supervisor-token", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body = %s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if executed, _ := data["executed"].(bool); executed {
		t.Fatal("brief claimed execution")
	}
	if data["status"] != "ok" {
		t.Fatalf("status = %#v", data["status"])
	}
	rec, _ := data["recommendation"].(map[string]any)
	if rec["recommendation_id"] != "rec-brief-1" {
		t.Fatalf("recommendation = %#v", rec)
	}
	providers, _ := data["providers"].(map[string]any)
	if providers["sol"] != "live" {
		t.Fatalf("providers = %#v, want live sol", providers)
	}
	assertAuditActor(t, data, "public-demo", "supervisor")
	audit, _ := data["audit_record"].(map[string]any)
	if audit["action"] != "briefing_requested" {
		t.Fatalf("audit action = %#v", audit["action"])
	}
	if solStub.calls != 1 || solStub.last.RequestedBy != "supervisor-token" {
		t.Fatalf("sol call = %#v", solStub)
	}
	// Hydration must pass the full store insight, not a stub id-only record.
	if len(solStub.last.Insights) != 1 || solStub.last.Insights[0].InsightID != "insight-1" {
		t.Fatalf("hydrated insights = %#v", solStub.last.Insights)
	}
	if len(solStub.last.Insights[0].Assertions) == 0 || solStub.last.Insights[0].CreatedAt == "" {
		t.Fatalf("insight was not fully hydrated: %#v", solStub.last.Insights[0])
	}
}

func TestOperatorBriefHydrateMissingInsight(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{
		Sol:             &stubSol{},
		AdvisoryHistory: base.store,
	})
	body := `{"insights":[{"insight_id":"insight-missing"}],"evidence":[{"kind":"canonical_event","id":"canonical-1"}]}`
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/brief", "supervisor-token", body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.Code, resp.Body.String())
	}
	if responseErrorCode(t, resp) != "invalid_insights" {
		t.Fatalf("error = %q", responseErrorCode(t, resp))
	}
}

func TestOperatorBriefViewerForbidden(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{Sol: &stubSol{}})
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/brief", "", `{"insights":[]}`)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", resp.Code, resp.Body.String())
	}
	if responseErrorCode(t, resp) != "supervisor_required" {
		t.Fatalf("error = %q", responseErrorCode(t, resp))
	}
}

func TestOperatorInterpret(t *testing.T) {
	base := newFixture(t)
	lunaStub := &stubLuna{
		output: contracts.LunaOutput{
			Result: gen.LunaResult{
				SchemaVersion:    "1.0.0",
				LunaResultID:     "luna-interp-1",
				RawEventID:       "raw-interp-1",
				CanonicalEventID: "canonical-interp-1",
				Status:           "accepted",
				CreatedAt:        "2026-07-18T12:02:00Z",
			},
			CanonicalEvent: &gen.CanonicalEvent{
				SchemaVersion:    "1.0.0",
				CanonicalEventID: "canonical-interp-1",
				RawEventID:       "raw-interp-1",
			},
			ModelRun: validModelRun("run-luna-ok", "luna", "valid"),
		},
	}
	server := newOperatorServer(t, base, Config{
		Luna: lunaStub,
		ProviderSelection: contracts.AgentProviderSelection{
			"luna": contracts.ProviderFixture,
		},
	})

	// Include a synthetic payload; the response must not re-echo it.
	body := `{
		"raw_event_id":"raw-interp-1",
		"schema_version":"1.0.0",
		"received_at":"2026-07-18T12:00:00Z",
		"content_type":"application/json",
		"payload_bytes_b64":"c2VjcmV0LXBheWxvYWQtdGhhdC1tdXN0LW5vdC1sZWFr",
		"source":{"system":"fixture","channel":"test"}
	}`
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/interpret", "", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("interpret status = %d, body = %s", resp.Code, resp.Body.String())
	}
	rawBody := resp.Body.String()
	if strings.Contains(rawBody, "payload_bytes_b64") || strings.Contains(rawBody, "c2VjcmV0") {
		t.Fatalf("interpret response leaked raw payload: %s", rawBody)
	}
	data := responseData(t, resp)
	if executed, _ := data["executed"].(bool); executed {
		t.Fatal("interpret claimed execution")
	}
	if data["status"] != "ok" {
		t.Fatalf("status = %#v", data["status"])
	}
	if data["result_status"] != "accepted" {
		t.Fatalf("result_status = %#v", data["result_status"])
	}
	if data["canonical_event_id"] != "canonical-interp-1" {
		t.Fatalf("canonical_event_id = %#v", data["canonical_event_id"])
	}
	if lunaStub.calls != 1 || lunaStub.last.RawEventID != "raw-interp-1" {
		t.Fatalf("luna call = %#v", lunaStub)
	}
	// Ensure adapter received the envelope (including payload) even though response omits it.
	if lunaStub.last.PayloadBytesB64 == "" {
		t.Fatal("luna stub did not receive payload bytes")
	}
}

func TestOperatorApproveAnnotatePrepareHandoff(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{})
	handler := server.Handler()

	approve := request(t, handler, http.MethodPost, "/api/v1/operator/approve", "",
		`{"target_kind":"recommendation","target_id":"recommendation-1","note":"looks good"}`)
	if approve.Code != http.StatusCreated {
		t.Fatalf("approve status = %d, body = %s", approve.Code, approve.Body.String())
	}
	approveData := responseData(t, approve)
	if executed, _ := approveData["executed"].(bool); executed {
		t.Fatal("approve claimed execution")
	}
	audit, _ := approveData["audit_record"].(map[string]any)
	if audit["action"] != "acknowledged" {
		t.Fatalf("approve action = %#v", audit["action"])
	}

	annotate := request(t, handler, http.MethodPost, "/api/v1/operator/annotate", "",
		`{"target_kind":"recommendation","target_id":"recommendation-1","note":"needs review"}`)
	if annotate.Code != http.StatusCreated {
		t.Fatalf("annotate status = %d, body = %s", annotate.Code, annotate.Body.String())
	}
	annotateData := responseData(t, annotate)
	if executed, _ := annotateData["executed"].(bool); executed {
		t.Fatal("annotate claimed execution")
	}
	annotateAudit, _ := annotateData["audit_record"].(map[string]any)
	if annotateAudit["action"] != "noted" {
		t.Fatalf("annotate action = %#v", annotateAudit["action"])
	}

	handoff := request(t, handler, http.MethodPost, "/api/v1/operator/prepare-handoff", "",
		`{"target_kind":"recommendation","target_id":"recommendation-1","recipient":"dispatch","note":"unit available"}`)
	if handoff.Code != http.StatusCreated {
		t.Fatalf("prepare-handoff status = %d, body = %s", handoff.Code, handoff.Body.String())
	}
	handoffData := responseData(t, handoff)
	if executed, _ := handoffData["executed"].(bool); executed {
		t.Fatal("prepare-handoff claimed execution")
	}
	if delivered, _ := handoffData["delivered"].(bool); delivered {
		t.Fatal("prepare-handoff claimed delivery")
	}
	if handoffData["handoff_status"] != "recorded" {
		t.Fatalf("handoff_status = %#v", handoffData["handoff_status"])
	}
	if handoffData["recipient"] != "dispatch" {
		t.Fatalf("recipient = %#v", handoffData["recipient"])
	}
	handoffAudit, _ := handoffData["audit_record"].(map[string]any)
	if handoffAudit["action"] != "noted" {
		t.Fatalf("handoff stored action = %#v (must be schema-valid noted, not prepare_handoff)", handoffAudit["action"])
	}
	note, _ := handoffAudit["note"].(string)
	if !strings.Contains(note, "dispatch") {
		t.Fatalf("handoff note missing recipient: %q", note)
	}
	if strings.Contains(strings.ToLower(note), "sent") && strings.Contains(strings.ToLower(handoffData["message"].(string)), "was sent") {
		// message should say nothing was sent
	}
	msg, _ := handoffData["message"].(string)
	if !strings.Contains(msg, "nothing was sent") && !strings.Contains(msg, "recorded") {
		t.Fatalf("handoff message should stress recorded-not-sent: %q", msg)
	}
}

func TestOperatorMissingAdaptersReturn503(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{})
	handler := server.Handler()

	cases := []struct {
		path string
		code string
		id   string
	}{
		{"/api/v1/operator/analyze", "terra_unavailable", ""},
		{"/api/v1/operator/brief", "sol_unavailable", "supervisor-token"},
		{"/api/v1/operator/interpret", "luna_unavailable", ""},
	}
	for _, tc := range cases {
		resp := request(t, handler, http.MethodPost, tc.path, tc.id, `{}`)
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, body = %s", tc.path, resp.Code, resp.Body.String())
		}
		if got := responseErrorCode(t, resp); got != tc.code {
			t.Fatalf("%s error = %q, want %q", tc.path, got, tc.code)
		}
	}
}

func TestOperatorFixtureAndLiveRoutingViaInjectedStubs(t *testing.T) {
	base := newFixture(t)

	// Fixture path: valid insight.
	fixtureTerra := &stubTerra{
		output: contracts.TerraOutput{
			Insight:  gen.Insight{SchemaVersion: "1.0.0", InsightID: "insight-fixture", StateRevision: 1, LifecycleStatus: "active"},
			ModelRun: validModelRun("run-fixture", "terra", "valid"),
		},
	}
	fixtureServer := newOperatorServer(t, base, Config{
		Terra: fixtureTerra,
		ProviderSelection: contracts.AgentProviderSelection{
			"terra": contracts.ProviderFixture,
		},
	})
	fixtureResp := request(t, fixtureServer.Handler(), http.MethodPost, "/api/v1/operator/analyze", "", `{"evidence":[]}`)
	if fixtureResp.Code != http.StatusOK {
		t.Fatalf("fixture analyze status = %d", fixtureResp.Code)
	}
	fixtureData := responseData(t, fixtureResp)
	if fixtureData["status"] != "ok" {
		t.Fatalf("fixture status = %#v", fixtureData["status"])
	}
	if providers, _ := fixtureData["providers"].(map[string]any); providers["terra"] != "fixture" {
		t.Fatalf("fixture providers = %#v", providers)
	}

	// Live path simulated by a stub that returns a refusal ModelRun (no network).
	liveRun := validModelRun("run-live-refused", "terra", "refused")
	liveRun.Provider = "openai"
	liveRun.Model = "gpt-live-stub"
	liveRun.FailureDetail = "live transport refused"
	liveTerra := &stubTerra{
		output: contracts.TerraOutput{ModelRun: liveRun},
		err:    terra.ErrAssessmentRefused,
	}
	liveServer := newOperatorServer(t, base, Config{
		Terra: liveTerra,
		ProviderSelection: contracts.AgentProviderSelection{
			"terra": contracts.ProviderLive,
		},
	})
	liveResp := request(t, liveServer.Handler(), http.MethodPost, "/api/v1/operator/analyze", "", `{"evidence":[]}`)
	if liveResp.Code != http.StatusOK {
		t.Fatalf("live analyze status = %d, body = %s", liveResp.Code, liveResp.Body.String())
	}
	liveData := responseData(t, liveResp)
	if liveData["status"] != "refused" {
		t.Fatalf("live status = %#v", liveData["status"])
	}
	if liveData["insight"] != nil {
		t.Fatal("live refusal must not invent an insight")
	}
	providers, _ := liveData["providers"].(map[string]any)
	if providers["terra"] != "live" {
		t.Fatalf("live providers = %#v", providers)
	}
	run, _ := liveData["model_run"].(map[string]any)
	if run["provider"] != "openai" || run["validation_status"] != "refused" {
		t.Fatalf("live model_run = %#v", run)
	}
}

func TestOperatorBriefSolRefusal(t *testing.T) {
	base := newFixture(t)
	run := validModelRun("run-sol-refused", "sol", "refused")
	solStub := &stubSol{
		output: contracts.SolOutput{ModelRun: run},
		err:    sol.ErrBriefingRefused,
	}
	server := newOperatorServer(t, base, Config{
		Sol:               solStub,
		BriefingRequester: "supervisor-token",
	})
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/brief", "supervisor-token", `{"insights":[]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if data["status"] != "refused" {
		t.Fatalf("status = %#v", data["status"])
	}
	if data["recommendation"] != nil {
		t.Fatalf("refusal returned recommendation: %#v", data["recommendation"])
	}
}

func TestOperatorPrepareHandoffRequiresRecipient(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{})
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/prepare-handoff", "",
		`{"target_kind":"system","target_id":"x","note":"no recipient"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if responseErrorCode(t, resp) != "invalid_handoff" {
		t.Fatalf("error = %q", responseErrorCode(t, resp))
	}
}

func TestOperatorAnalyzeWithLiveProviderHintDoesNotLeakSecrets(t *testing.T) {
	base := newFixture(t)
	terraStub := &stubTerra{
		output: contracts.TerraOutput{
			Insight:  gen.Insight{SchemaVersion: "1.0.0", InsightID: "i1", StateRevision: 1, LifecycleStatus: "active"},
			ModelRun: validModelRun("run1", "terra", "valid"),
		},
	}
	server := newOperatorServer(t, base, Config{
		Terra: terraStub,
		ProviderSelection: contracts.AgentProviderSelection{
			"terra": contracts.ProviderLive,
		},
	})
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/analyze", "", `{}`)
	body := resp.Body.String()
	for _, secretish := range []string{"OPENAI", "api_key", "sk-", "Bearer"} {
		if strings.Contains(body, secretish) {
			t.Fatalf("response leaked secret material %q: %s", secretish, body)
		}
	}
}

func TestOperatorDecisionMissingTarget(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{})
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/operator/approve", "",
		`{"target_kind":"recommendation","target_id":"missing-rec","note":"x"}`)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
}

// Ensure stub types satisfy contracts at compile time.
var (
	_ contracts.TerraAdapter = (*stubTerra)(nil)
	_ contracts.SolAdapter   = (*stubSol)(nil)
	_ contracts.LunaAdapter  = (*stubLuna)(nil)
)
