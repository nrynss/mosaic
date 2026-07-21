package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mosaic.local/mosaic/internal/democast"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
)

func demoRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := simulator.RepositoryRoot(".")
	if err != nil {
		root, err = simulator.RepositoryRoot(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("repository root: %v", err)
		}
	}
	return root
}

func TestDemoInteractionsUnavailableWithoutAssetRoot(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{})
	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/demo/interactions", "", "")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", resp.Code, resp.Body.String())
	}
	if got := responseErrorCode(t, resp); got != "demo_interactions_unavailable" {
		t.Fatalf("error code = %q", got)
	}
}

func TestDemoInteractionsServesManifestPayloads(t *testing.T) {
	root := demoRepoRoot(t)
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{
		DemoAssetRoot: root,
		CassetteMode:  "replay",
	})
	resp := request(t, server.Handler(), http.MethodGet, "/api/v1/demo/interactions", "", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if data["scenario"] != "domestic-disturbance" {
		t.Fatalf("scenario = %#v", data["scenario"])
	}
	if data["supervisor_identity"] != "supervisor-demo" {
		t.Fatalf("supervisor_identity = %#v", data["supervisor_identity"])
	}
	if data["cassette_mode"] != "replay" {
		t.Fatalf("cassette_mode = %#v, want replay", data["cassette_mode"])
	}
	if rev, _ := data["expected_cop_revision"].(float64); int64(rev) != 9 {
		t.Fatalf("expected_cop_revision = %#v", data["expected_cop_revision"])
	}

	steps, ok := data["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("steps missing: %#v", data["steps"])
	}

	wantDoc, err := democast.BuildInteractions(root)
	if err != nil {
		t.Fatalf("reference build: %v", err)
	}
	if len(steps) != len(wantDoc.Steps) {
		t.Fatalf("step count = %d, want %d", len(steps), len(wantDoc.Steps))
	}

	// Spot-check: Luna request fields match dataset; no secret keys in body.
	body := resp.Body.String()
	for _, banned := range []string{"OPENAI_API_KEY", "api_key", "sk-"} {
		if strings.Contains(body, banned) {
			t.Fatalf("response body contains banned secret marker %q", banned)
		}
	}

	var foundLuna, foundTerra, foundSol bool
	for i, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("step %d not object: %#v", i, raw)
		}
		kind, _ := step["kind"].(string)
		endpoint, _ := step["endpoint"].(string)
		req, _ := step["request"].(map[string]any)
		if req == nil {
			t.Fatalf("step %d missing request", i)
		}
		switch kind {
		case "luna":
			foundLuna = true
			if endpoint != "operator/interpret" {
				t.Fatalf("luna endpoint = %q", endpoint)
			}
			if asString(req["raw_event_id"]) == "" {
				t.Fatalf("luna raw_event_id empty at step %d", i)
			}
			if asString(req["payload_bytes_b64"]) == "" {
				t.Fatalf("luna payload_bytes_b64 empty at step %d", i)
			}
			// Compare against first matching reference step.
			ref := wantDoc.Steps[i]
			if ref.Kind == "luna" && req["raw_event_id"] != ref.Request["raw_event_id"] {
				t.Fatalf("step %d raw_event_id = %#v, want %#v", i, req["raw_event_id"], ref.Request["raw_event_id"])
			}
		case "terra":
			foundTerra = true
			if endpoint != "operator/analyze" {
				t.Fatalf("terra endpoint = %q", endpoint)
			}
			if req["evidence"] == nil {
				t.Fatal("terra missing evidence")
			}
			if asString(req["note"]) == "" {
				t.Fatal("terra missing note")
			}
		case "sol":
			foundSol = true
			if endpoint != "operator/brief" {
				t.Fatalf("sol endpoint = %q", endpoint)
			}
			if req["insights"] == nil {
				t.Fatal("sol missing insights")
			}
			if req["evidence"] == nil {
				t.Fatal("sol missing evidence")
			}
		case "play":
			t.Fatal("play steps must not be served")
		}
	}
	if !foundLuna || !foundTerra || !foundSol {
		t.Fatalf("missing kinds: luna=%v terra=%v sol=%v", foundLuna, foundTerra, foundSol)
	}
}

func TestDemoInteractionsMethodNotAllowed(t *testing.T) {
	base := newFixture(t)
	server := newOperatorServer(t, base, Config{DemoAssetRoot: demoRepoRoot(t)})
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/demo/interactions", "", `{}`)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.Code)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
