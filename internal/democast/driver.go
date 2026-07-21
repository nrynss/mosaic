package democast

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/ontology/gen"
)

// DriverConfig configures the HTTP demo driver.
type DriverConfig struct {
	BaseURL            string
	SupervisorIdentity string
	ExpectedCOPRev     int64
	// Client defaults to http.DefaultClient.
	Client *http.Client
	// PlayTimeout is how long to wait for simulation status == ended.
	PlayTimeout time.Duration
	// PollInterval is the simulation status poll period.
	PollInterval time.Duration
}

// StepResult is the outcome of one manifest step.
type StepResult struct {
	Kind         string
	BeatID       string
	RawEventID   string
	HTTPStatus   int
	Status       string // operator payload status when present
	Provider     string // model_run.provider when present
	Body         map[string]any
	RawBody      []byte
	Err          error
	ExpectedLuna string // expected terminal status for luna (ok/quarantined)
}

// Driver issues the manifest against a running mosaicdemo base URL.
type Driver struct {
	cfg    DriverConfig
	client *http.Client
	raw    RawEventIndex
}

// NewDriver constructs a Driver. raw must contain every raw_event_ref in the
// manifest; typically LoadRawEvents(assetRoot, manifest.Scenario).
func NewDriver(cfg DriverConfig, raw RawEventIndex) (*Driver, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if raw == nil {
		return nil, fmt.Errorf("raw event index is required")
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	if cfg.PlayTimeout <= 0 {
		cfg.PlayTimeout = 45 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 20 * time.Millisecond
	}
	if strings.TrimSpace(cfg.SupervisorIdentity) == "" {
		cfg.SupervisorIdentity = "supervisor-demo"
	}
	if cfg.ExpectedCOPRev <= 0 {
		cfg.ExpectedCOPRev = 9
	}
	return &Driver{cfg: cfg, client: client, raw: raw}, nil
}

// RunAll executes every manifest step in order and returns per-step results.
// It does not soft-fail: the first hard transport/setup error aborts the run
// and is returned; earlier step outcomes are still in results.
func (d *Driver) RunAll(m Manifest) ([]StepResult, error) {
	results := make([]StepResult, 0, len(m.Steps))
	for _, step := range m.Steps {
		res, err := d.RunStep(step)
		results = append(results, res)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// RunStep executes a single manifest step.
func (d *Driver) RunStep(step Step) (StepResult, error) {
	kind := strings.ToLower(strings.TrimSpace(step.Kind))
	switch kind {
	case "play":
		return d.runPlay()
	case "luna":
		return d.runLuna(step)
	case "terra":
		return d.runTerra(step)
	case "sol":
		return d.runSol(step)
	default:
		return StepResult{Kind: kind}, fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

func (d *Driver) runPlay() (StepResult, error) {
	res := StepResult{Kind: "play"}
	resp, body, err := d.do(http.MethodPost, "/api/v1/simulation/start", nil, "")
	if err != nil {
		res.Err = err
		return res, err
	}
	res.HTTPStatus = resp.StatusCode
	res.RawBody = body
	data, err := decodeData(body)
	if err != nil {
		res.Err = err
		return res, fmt.Errorf("play start decode: %w", err)
	}
	res.Body = data
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("play start status %d: %s", resp.StatusCode, truncate(body, 400))
		res.Err = err
		return res, err
	}

	deadline := time.Now().Add(d.cfg.PlayTimeout)
	for {
		if time.Now().After(deadline) {
			err = fmt.Errorf("timed out waiting for simulation ended")
			res.Err = err
			return res, err
		}
		statusResp, statusBody, statusErr := d.do(http.MethodGet, "/api/v1/simulation/status", nil, "")
		if statusErr != nil {
			res.Err = statusErr
			return res, statusErr
		}
		statusData, decErr := decodeData(statusBody)
		if decErr != nil {
			res.Err = decErr
			return res, decErr
		}
		if statusResp.StatusCode != http.StatusOK {
			err = fmt.Errorf("simulation status %d: %s", statusResp.StatusCode, truncate(statusBody, 400))
			res.Err = err
			return res, err
		}
		if st, _ := statusData["status"].(string); st == "ended" {
			res.Status = "ended"
			res.Body = statusData
			break
		}
		time.Sleep(d.cfg.PollInterval)
	}

	// Confirm COP revision.
	copResp, copBody, copErr := d.do(http.MethodGet, "/api/v1/cop", nil, "")
	if copErr != nil {
		res.Err = copErr
		return res, copErr
	}
	if copResp.StatusCode != http.StatusOK {
		err = fmt.Errorf("cop after play status %d: %s", copResp.StatusCode, truncate(copBody, 400))
		res.Err = err
		return res, err
	}
	copData, decErr := decodeData(copBody)
	if decErr != nil {
		res.Err = decErr
		return res, decErr
	}
	rev, _ := copData["state_revision"].(float64)
	if int64(rev) != d.cfg.ExpectedCOPRev {
		err = fmt.Errorf("COP revision after play = %v, want %d", copData["state_revision"], d.cfg.ExpectedCOPRev)
		res.Err = err
		return res, err
	}
	return res, nil
}

func (d *Driver) runLuna(step Step) (StepResult, error) {
	res := StepResult{Kind: "luna", BeatID: step.BeatID, RawEventID: step.RawEventRef}
	res.ExpectedLuna = lunaExpectedStatus(step)

	ev, err := d.raw.Get(step.RawEventRef)
	if err != nil {
		res.Err = err
		return res, err
	}
	payload := interpretBodyFromRaw(ev)
	resp, body, err := d.do(http.MethodPost, "/api/v1/operator/interpret", payload, "")
	if err != nil {
		res.Err = err
		return res, err
	}
	return d.fillOperatorResult(res, resp, body)
}

func (d *Driver) runTerra(step Step) (StepResult, error) {
	res := StepResult{Kind: "terra"}
	if err := d.requireCOPRevision(step.StateRevision); err != nil {
		res.Err = err
		return res, err
	}
	payload := map[string]any{
		"evidence": evidencePayload(step.Evidence),
		"note":     step.Note,
	}
	resp, body, err := d.do(http.MethodPost, "/api/v1/operator/analyze", payload, "")
	if err != nil {
		res.Err = err
		return res, err
	}
	return d.fillOperatorResult(res, resp, body)
}

func (d *Driver) runSol(step Step) (StepResult, error) {
	res := StepResult{Kind: "sol"}
	if err := d.requireCOPRevision(step.StateRevision); err != nil {
		res.Err = err
		return res, err
	}
	insights := make([]map[string]any, 0, len(step.Insights))
	for _, ins := range step.Insights {
		insights = append(insights, map[string]any{"insight_id": ins.InsightID})
	}
	payload := map[string]any{
		"insights": insights,
		"evidence": evidencePayload(step.Evidence),
		"note":     step.Note,
	}
	resp, body, err := d.do(http.MethodPost, "/api/v1/operator/brief", payload, d.cfg.SupervisorIdentity)
	if err != nil {
		res.Err = err
		return res, err
	}
	return d.fillOperatorResult(res, resp, body)
}

// requireCOPRevision fetches the live COP and asserts state_revision matches
// the step's declared revision (Terra/Sol cassette keys embed revN).
func (d *Driver) requireCOPRevision(want int64) error {
	if want < 1 {
		want = d.cfg.ExpectedCOPRev
	}
	copResp, copBody, copErr := d.do(http.MethodGet, "/api/v1/cop", nil, "")
	if copErr != nil {
		return copErr
	}
	if copResp.StatusCode != http.StatusOK {
		return fmt.Errorf("cop before model step status %d: %s", copResp.StatusCode, truncate(copBody, 400))
	}
	copData, decErr := decodeData(copBody)
	if decErr != nil {
		return decErr
	}
	rev, _ := copData["state_revision"].(float64)
	if int64(rev) != want {
		return fmt.Errorf("COP revision before model step = %v, want %d", copData["state_revision"], want)
	}
	return nil
}

// lunaExpectedStatus resolves the CI-strict terminal status for a Luna step.
// Manifest expected_status wins; otherwise beat-8 invalid input defaults to
// quarantined and every other beat defaults to ok.
func lunaExpectedStatus(step Step) string {
	if s := strings.TrimSpace(step.ExpectedStatus); s != "" {
		return strings.ToLower(s)
	}
	if step.RawEventRef == "raw-domestic-008-invalid-input" {
		return "quarantined"
	}
	return "ok"
}

func (d *Driver) fillOperatorResult(res StepResult, resp *http.Response, body []byte) (StepResult, error) {
	res.HTTPStatus = resp.StatusCode
	res.RawBody = body
	data, err := decodeData(body)
	if err != nil {
		// Some error responses still carry a data envelope; try best-effort.
		res.Err = fmt.Errorf("decode operator response: %w; body=%s", err, truncate(body, 400))
		return res, res.Err
	}
	res.Body = data
	if st, ok := data["status"].(string); ok {
		res.Status = st
	}
	if mr, ok := data["model_run"].(map[string]any); ok {
		if p, ok := mr["provider"].(string); ok {
			res.Provider = p
		}
	}
	return res, nil
}

func (d *Driver) do(method, path string, payload any, identity string) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal %s %s: %w", method, path, err)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	url := strings.TrimRight(d.cfg.BaseURL, "/") + path
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if identity != "" {
		req.Header.Set("X-Mosaic-Demo-Identity", identity)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	return resp, body, nil
}

// interpretBodyFromRaw builds the operator interpret request from a dataset
// RawEvent. Fields are copied verbatim so the server-side json.Marshal of
// gen.RawEvent matches across record and replay.
func interpretBodyFromRaw(ev gen.RawEvent) map[string]any {
	body := map[string]any{
		"raw_event_id":       ev.RawEventID,
		"schema_version":     ev.SchemaVersion,
		"received_at":        ev.ReceivedAt,
		"content_type":       ev.ContentType,
		"payload_bytes_b64":  ev.PayloadBytesB64,
		"raw_sha256":         ev.RawSha256,
		"source_occurred_at": ev.SourceOccurredAt,
		"source":             ev.Source,
	}
	if len(ev.Attributes) > 0 {
		body["attributes"] = ev.Attributes
	}
	return body
}

func evidencePayload(items []EvidenceLiteral) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"kind":        item.Kind,
			"id":          item.ID,
			"explanation": item.Explanation,
		})
	}
	return out
}

func decodeData(body []byte) (map[string]any, error) {
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if envelope.Data == nil {
		return map[string]any{}, nil
	}
	return envelope.Data, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// AssertOperatorOK checks a Luna/Terra/Sol step hit an expected terminal status.
//
// When requireFixtureProvider is true (CI / no-live replay), Luna status must
// equal ExpectedLuna exactly — no soft "ok" fallback — so a bank that drifts
// from the manifest contract fails the gate. When false (offline record or
// gated live re-record), Luna may return ok / quarantined / refused.
//
// For replay under mosaic-fixture, provider should be mosaic-fixture when a
// model_run is present.
func AssertOperatorOK(res StepResult, requireFixtureProvider bool) error {
	if res.Err != nil {
		return res.Err
	}
	switch res.Kind {
	case "play":
		if res.Status != "ended" {
			return fmt.Errorf("play status = %q, want ended", res.Status)
		}
		return nil
	case "luna":
		want := res.ExpectedLuna
		if want == "" {
			want = "ok"
		}
		switch res.Status {
		case "error", "failed", "unavailable", "invalid", "timed_out":
			return fmt.Errorf("luna %s terminal status %q body=%s", res.RawEventID, res.Status, truncate(res.RawBody, 400))
		}
		if requireFixtureProvider {
			if res.Status != want {
				return fmt.Errorf("luna %s status = %q, want %q (strict CI/no-live)", res.RawEventID, res.Status, want)
			}
		} else {
			// Live / offline record: model may quarantine non-incident beats.
			switch res.Status {
			case "ok", "quarantined", "refused":
				// acceptable bankable terminals
			default:
				return fmt.Errorf("luna %s status = %q, want ok|quarantined|refused", res.RawEventID, res.Status)
			}
		}
	case "terra", "sol":
		if res.Status != "ok" {
			return fmt.Errorf("%s status = %q, want ok; body=%s", res.Kind, res.Status, truncate(res.RawBody, 400))
		}
	}
	if requireFixtureProvider && res.Kind != "play" && res.Provider != "" && res.Provider != "mosaic-fixture" {
		return fmt.Errorf("%s provider = %q, want mosaic-fixture (no live network)", res.Kind, res.Provider)
	}
	return nil
}
