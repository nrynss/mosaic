package democast

import (
	"strings"
	"testing"
)

func TestBuildInteractionsDefaultManifest(t *testing.T) {
	root := repoRoot(t)
	doc, err := BuildInteractions(root)
	if err != nil {
		t.Fatalf("BuildInteractions: %v", err)
	}
	if doc.Scenario != "domestic-disturbance" {
		t.Fatalf("scenario = %q", doc.Scenario)
	}
	if doc.ExpectedCOPRevision != 9 {
		t.Fatalf("expected_cop_revision = %d", doc.ExpectedCOPRevision)
	}
	if doc.SupervisorIdentity != "supervisor-demo" {
		t.Fatalf("supervisor_identity = %q", doc.SupervisorIdentity)
	}

	var luna, terra, sol int
	for _, step := range doc.Steps {
		switch step.Kind {
		case "play":
			t.Fatalf("play step must not appear in interactions document")
		case "luna":
			luna++
			if step.Endpoint != "operator/interpret" {
				t.Fatalf("luna endpoint = %q", step.Endpoint)
			}
			if step.BeatID == "" || step.RawEventRef == "" {
				t.Fatalf("luna step missing identity: %#v", step)
			}
			req := step.Request
			if req["raw_event_id"] != step.RawEventRef {
				t.Fatalf("luna request raw_event_id = %#v, want %q", req["raw_event_id"], step.RawEventRef)
			}
			for _, key := range []string{"schema_version", "received_at", "content_type", "payload_bytes_b64", "raw_sha256", "source"} {
				if _, ok := req[key]; !ok {
					t.Fatalf("luna %s request missing %s", step.RawEventRef, key)
				}
			}
			// No API keys or ambient secrets in the served payload.
			for k, v := range req {
				if strings.Contains(strings.ToLower(k), "api_key") || strings.Contains(strings.ToLower(k), "secret") {
					t.Fatalf("secret-like field %q = %#v", k, v)
				}
			}
			if step.RawEventRef == "raw-domestic-008-invalid-input" && step.ExpectedStatus != "quarantined" {
				t.Fatalf("beat-8 expected_status = %q, want quarantined", step.ExpectedStatus)
			}
		case "terra":
			terra++
			if step.Endpoint != "operator/analyze" {
				t.Fatalf("terra endpoint = %q", step.Endpoint)
			}
			if step.StateRevision != 9 {
				t.Fatalf("terra state_revision = %d", step.StateRevision)
			}
			evidence, _ := step.Request["evidence"].([]map[string]any)
			if len(evidence) == 0 {
				// JSON round-trip may yield []any; accept either.
				if raw, ok := step.Request["evidence"].([]any); !ok || len(raw) == 0 {
					t.Fatalf("terra evidence empty: %#v", step.Request["evidence"])
				}
			}
			if strings.TrimSpace(asString(step.Request["note"])) == "" {
				t.Fatal("terra note missing")
			}
		case "sol":
			sol++
			if step.Endpoint != "operator/brief" {
				t.Fatalf("sol endpoint = %q", step.Endpoint)
			}
			if step.StateRevision != 9 {
				t.Fatalf("sol state_revision = %d", step.StateRevision)
			}
			insights, ok := step.Request["insights"].([]map[string]any)
			if !ok {
				if raw, ok := step.Request["insights"].([]any); !ok || len(raw) == 0 {
					t.Fatalf("sol insights empty: %#v", step.Request["insights"])
				}
			} else if len(insights) == 0 {
				t.Fatal("sol insights empty")
			}
		default:
			t.Fatalf("unexpected kind %q", step.Kind)
		}
	}
	if luna != 10 {
		t.Fatalf("luna steps = %d, want 10", luna)
	}
	if terra != 1 {
		t.Fatalf("terra steps = %d, want 1", terra)
	}
	if sol != 1 {
		t.Fatalf("sol steps = %d, want 1", sol)
	}
}

func TestBuildInteractionsLunaPayloadMatchesDataset(t *testing.T) {
	root := repoRoot(t)
	m, err := LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	raw, err := LoadRawEvents(root, m.Scenario)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	doc, err := BuildInteractionsFrom(m, raw)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, step := range doc.Steps {
		if step.Kind != "luna" {
			continue
		}
		ev, err := raw.Get(step.RawEventRef)
		if err != nil {
			t.Fatalf("get %s: %v", step.RawEventRef, err)
		}
		want := interpretBodyFromRaw(ev)
		for _, key := range []string{"raw_event_id", "schema_version", "received_at", "content_type", "payload_bytes_b64", "raw_sha256", "source_occurred_at"} {
			if step.Request[key] != want[key] {
				t.Fatalf("%s field %s = %#v, want %#v", step.RawEventRef, key, step.Request[key], want[key])
			}
		}
	}
}

func TestBuildInteractionsRequiresAssetRoot(t *testing.T) {
	if _, err := BuildInteractions(""); err == nil {
		t.Fatal("expected error for empty asset root")
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
