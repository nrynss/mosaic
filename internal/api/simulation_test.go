package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/simsession"
)

type testSchedule struct {
	beats []contracts.ScheduledBeat
}

func (s testSchedule) Beats() []contracts.ScheduledBeat {
	out := make([]contracts.ScheduledBeat, len(s.beats))
	copy(out, s.beats)
	return out
}

func zeroDelayBeats() []contracts.ScheduledBeat {
	return []contracts.ScheduledBeat{
		{BeatID: "beat-a", Order: 1, RawEventID: "raw-a", Delay: 0},
		{BeatID: "beat-b", Order: 2, RawEventID: "raw-b", Delay: 0},
	}
}

// simControllerOpts configure the in-test P36 controller.
// holdBeats keeps positive-delay beats from completing (After never fires)
// so status can be observed as running without races against natural end.
type simControllerOpts struct {
	sessionIDs []string
	holdBeats  bool
}

func newSimulationController(t *testing.T, beats []contracts.ScheduledBeat, opts simControllerOpts) *simsession.Controller {
	t.Helper()
	var (
		mu  sync.Mutex
		idx int
	)
	ids := opts.sessionIDs
	if len(ids) == 0 {
		ids = []string{"sim-test-1", "sim-test-2", "sim-test-3", "sim-test-4"}
	}
	after := func(d time.Duration) <-chan time.Time {
		// Immediate After: with a frozen clock the controller treats this as
		// wait complete (zero-delay / natural completion path).
		ch := make(chan time.Time, 1)
		ch <- time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
		return ch
	}
	if opts.holdBeats {
		// Never-firing After: waitUntil only exits on session cancel (End/Reset).
		after = func(d time.Duration) <-chan time.Time {
			return make(chan time.Time)
		}
	}
	ctrl, err := simsession.New(simsession.Config{
		Schedule: testSchedule{beats: beats},
		Clock:    func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) },
		After:    after,
		NewSessionID: func() string {
			mu.Lock()
			defer mu.Unlock()
			if idx >= len(ids) {
				idx++
				return "sim-test-overflow"
			}
			id := ids[idx]
			idx++
			return id
		},
	})
	if err != nil {
		t.Fatalf("new simsession controller: %v", err)
	}
	return ctrl
}

func newSimulationFixture(t *testing.T, ctrl *simsession.Controller) apiFixture {
	t.Helper()
	base := newFixture(t)
	server, err := New(Config{
		Recovery:   base.server.recovery,
		Records:    base.store,
		Evidence:   base.server.evidence,
		Operations: base.server.operations,
		Stream:     base.broker,
		Simulation: ctrl,
		Clock:      base.server.clock,
		NewID:      sequentialIDs(),
	})
	if err != nil {
		t.Fatalf("new API server with simulation: %v", err)
	}
	return apiFixture{store: base.store, server: server, handler: server.Handler(), broker: base.broker}
}

func waitSessionStatus(t *testing.T, handler http.Handler, want string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp := request(t, handler, http.MethodGet, "/api/v1/simulation/status", "", "")
		if resp.Code != http.StatusOK {
			t.Fatalf("status poll = %d, body = %s", resp.Code, resp.Body.String())
		}
		data := responseData(t, resp)
		if status, _ := data["status"].(string); status == want {
			return data
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for simulation status %q", want)
	return nil
}

func TestSimulationStartStatusEnd(t *testing.T) {
	// Hold the first beat so the session stays running until explicit End.
	beats := []contracts.ScheduledBeat{
		{BeatID: "beat-a", Order: 1, RawEventID: "raw-a", Delay: time.Hour},
	}
	ctrl := newSimulationController(t, beats, simControllerOpts{holdBeats: true})
	fixture := newSimulationFixture(t, ctrl)

	start := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/start", "", "")
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", start.Code, start.Body.String())
	}
	startData := responseData(t, start)
	if startData["status"] != "running" {
		t.Fatalf("start status = %#v, want running", startData["status"])
	}
	sessionID, _ := startData["session_id"].(string)
	if sessionID == "" {
		t.Fatal("start returned empty session_id")
	}
	beatsRaw, ok := startData["beats"].([]any)
	if !ok || len(beatsRaw) != 1 {
		t.Fatalf("start beats = %#v, want 1 beat", startData["beats"])
	}
	beat, _ := beatsRaw[0].(map[string]any)
	if beat["beat_id"] != "beat-a" || beat["raw_event_id"] != "raw-a" {
		t.Fatalf("start beat = %#v", beat)
	}
	if strings.Contains(start.Body.String(), "payload_bytes") || strings.Contains(start.Body.String(), "source body") {
		t.Fatalf("start response leaked raw payload material: %s", start.Body.String())
	}

	status := request(t, fixture.handler, http.MethodGet, "/api/v1/simulation/status", "", "")
	if status.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status.Code, status.Body.String())
	}
	statusData := responseData(t, status)
	if statusData["status"] != "running" || statusData["session_id"] != sessionID {
		t.Fatalf("status while running = %#v", statusData)
	}

	end := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/end", "", "")
	if end.Code != http.StatusOK {
		t.Fatalf("end status = %d, body = %s", end.Code, end.Body.String())
	}
	endData := responseData(t, end)
	if endData["status"] != "ended" {
		t.Fatalf("end status = %#v, want ended", endData["status"])
	}
	if endData["session_id"] != sessionID {
		t.Fatalf("end session_id = %#v, want %s", endData["session_id"], sessionID)
	}

	// Idempotent end while already ended.
	endAgain := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/end", "", "")
	if endAgain.Code != http.StatusOK {
		t.Fatalf("second end status = %d, body = %s", endAgain.Code, endAgain.Body.String())
	}
	if responseData(t, endAgain)["status"] != "ended" {
		t.Fatalf("second end = %#v", responseData(t, endAgain))
	}
}

func TestSimulationNaturalCompletion(t *testing.T) {
	ctrl := newSimulationController(t, zeroDelayBeats(), simControllerOpts{})
	fixture := newSimulationFixture(t, ctrl)

	start := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/start", "", "")
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", start.Code, start.Body.String())
	}
	ended := waitSessionStatus(t, fixture.handler, "ended", time.Second)
	if ended["session_id"] == "" {
		t.Fatalf("ended status missing session_id: %#v", ended)
	}
}

func TestSimulationStartWhileRunningConflict(t *testing.T) {
	beats := []contracts.ScheduledBeat{
		{BeatID: "beat-a", Order: 1, RawEventID: "raw-a", Delay: time.Hour},
	}
	ctrl := newSimulationController(t, beats, simControllerOpts{holdBeats: true})
	fixture := newSimulationFixture(t, ctrl)

	first := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/start", "", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first start = %d, body = %s", first.Code, first.Body.String())
	}
	second := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/start", "", "")
	if second.Code != http.StatusConflict {
		t.Fatalf("second start = %d, want 409, body = %s", second.Code, second.Body.String())
	}
	if code := responseErrorCode(t, second); code != "simulation_already_running" {
		t.Fatalf("conflict code = %q", code)
	}
	// Body still carries the current session for client convenience.
	if responseData(t, second)["status"] != "running" {
		t.Fatalf("conflict data = %#v", responseData(t, second))
	}
}

func TestSimulationResetWhileRunningAndSuccessiveReset(t *testing.T) {
	beats := []contracts.ScheduledBeat{
		{BeatID: "beat-a", Order: 1, RawEventID: "raw-a", Delay: time.Hour},
	}
	ctrl := newSimulationController(t, beats, simControllerOpts{
		holdBeats:  true,
		sessionIDs: []string{"sim-a", "sim-b", "sim-c"},
	})
	fixture := newSimulationFixture(t, ctrl)

	start := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/start", "", "")
	if start.Code != http.StatusOK {
		t.Fatalf("start = %d, body = %s", start.Code, start.Body.String())
	}
	firstID, _ := responseData(t, start)["session_id"].(string)

	reset := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/reset", "", "")
	if reset.Code != http.StatusOK {
		t.Fatalf("reset = %d, body = %s", reset.Code, reset.Body.String())
	}
	resetData := responseData(t, reset)
	if resetData["status"] != "running" {
		t.Fatalf("reset status = %#v, want running", resetData["status"])
	}
	secondID, _ := resetData["session_id"].(string)
	if secondID == "" || secondID == firstID {
		t.Fatalf("reset session_id = %q, first = %q; want a new id", secondID, firstID)
	}

	// Second reset is also safe and yields another new session.
	reset2 := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/reset", "", "")
	if reset2.Code != http.StatusOK {
		t.Fatalf("second reset = %d, body = %s", reset2.Code, reset2.Body.String())
	}
	thirdID, _ := responseData(t, reset2)["session_id"].(string)
	if thirdID == "" || thirdID == secondID {
		t.Fatalf("second reset session_id = %q, previous = %q", thirdID, secondID)
	}
	if responseData(t, reset2)["status"] != "running" {
		t.Fatalf("second reset status = %#v", responseData(t, reset2))
	}
}

func TestSimulationStreamEventsAndCancelCleanup(t *testing.T) {
	ctrl := newSimulationController(t, zeroDelayBeats(), simControllerOpts{})
	fixture := newSimulationFixture(t, ctrl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/simulation/stream", nil).WithContext(ctx)
	writer := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(writer, req)
		close(done)
	}()

	select {
	case <-writer.flushed:
	case <-time.After(time.Second):
		t.Fatal("simulation SSE handler did not flush initial snapshot")
	}
	if body := writer.Body.String(); !strings.Contains(body, "event: session.snapshot") {
		t.Fatalf("missing session.snapshot: %q", body)
	}

	// Start after subscribe so the stream receives lifecycle + beat events.
	start := request(t, fixture.handler, http.MethodPost, "/api/v1/simulation/start", "", "")
	if start.Code != http.StatusOK {
		t.Fatalf("start = %d, body = %s", start.Code, start.Body.String())
	}

	// Collect stream body until workspace_clear, status_change, and beat appear.
	deadline := time.Now().Add(time.Second)
	var body string
	for time.Now().Before(deadline) {
		body = writer.Body.String()
		if strings.Contains(body, "event: workspace_clear") &&
			strings.Contains(body, "event: status_change") &&
			strings.Contains(body, "event: beat") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(body, "event: workspace_clear") {
		t.Fatalf("stream missing workspace_clear: %q", body)
	}
	if !strings.Contains(body, "event: status_change") {
		t.Fatalf("stream missing status_change: %q", body)
	}
	if !strings.Contains(body, "event: beat") {
		t.Fatalf("stream missing beat: %q", body)
	}
	if strings.Contains(body, "payload_bytes") {
		t.Fatalf("stream leaked raw payload material: %q", body)
	}
	// Beat payload carries raw_event_id only (from controller).
	if !strings.Contains(body, "raw_event_id") || !strings.Contains(body, "raw-a") {
		t.Fatalf("stream beat missing raw_event_id: %q", body)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("simulation SSE handler did not return after request cancellation")
	}
}

func TestSimulationMissingControllerReturnsUnavailable(t *testing.T) {
	// Default fixture has no Simulation configured.
	fixture := newFixture(t)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/simulation/start"},
		{http.MethodPost, "/api/v1/simulation/reset"},
		{http.MethodGet, "/api/v1/simulation/status"},
		{http.MethodPost, "/api/v1/simulation/end"},
		{http.MethodGet, "/api/v1/simulation/stream"},
	} {
		resp := request(t, fixture.handler, tc.method, tc.path, "", "")
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d, want 503, body = %s", tc.method, tc.path, resp.Code, resp.Body.String())
		}
		if code := responseErrorCode(t, resp); code != "simulation_unavailable" {
			t.Fatalf("%s %s error code = %q", tc.method, tc.path, code)
		}
	}
}

func TestSimulationDenyPolicy(t *testing.T) {
	ctrl := newSimulationController(t, zeroDelayBeats(), simControllerOpts{})
	base := newFixture(t)
	server, err := New(Config{
		Recovery:   base.server.recovery,
		Records:    base.store,
		Evidence:   base.server.evidence,
		Simulation: ctrl,
		Policy:     denyPolicy{deny: ActionControlSimulation},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	resp := request(t, server.Handler(), http.MethodPost, "/api/v1/simulation/start", "", "")
	if resp.Code != http.StatusForbidden || responseErrorCode(t, resp) != "action_denied" {
		t.Fatalf("denied start = %d %s", resp.Code, resp.Body.String())
	}

	// Read path uses a different action.
	serverRead, err := New(Config{
		Recovery:   base.server.recovery,
		Records:    base.store,
		Evidence:   base.server.evidence,
		Simulation: ctrl,
		Policy:     denyPolicy{deny: ActionReadSimulation},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	status := request(t, serverRead.Handler(), http.MethodGet, "/api/v1/simulation/status", "", "")
	if status.Code != http.StatusForbidden || responseErrorCode(t, status) != "action_denied" {
		t.Fatalf("denied status = %d %s", status.Code, status.Body.String())
	}
}

func TestSimulationMethodNotAllowed(t *testing.T) {
	ctrl := newSimulationController(t, zeroDelayBeats(), simControllerOpts{})
	fixture := newSimulationFixture(t, ctrl)
	resp := request(t, fixture.handler, http.MethodGet, "/api/v1/simulation/start", "", "")
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET start = %d, want 405", resp.Code)
	}
}

func TestSimulationSessionJSONShape(t *testing.T) {
	ctrl := newSimulationController(t, []contracts.ScheduledBeat{
		{BeatID: "b1", Order: 1, RawEventID: "r1", Delay: 1500 * time.Millisecond},
	}, simControllerOpts{holdBeats: true})
	fixture := newSimulationFixture(t, ctrl)

	// Pending status before start.
	pending := request(t, fixture.handler, http.MethodGet, "/api/v1/simulation/status", "", "")
	if pending.Code != http.StatusOK {
		t.Fatalf("pending status = %d", pending.Code)
	}
	pendingData := responseData(t, pending)
	if pendingData["status"] != "pending" {
		t.Fatalf("pending = %#v", pendingData)
	}
	// Empty session_id before start is valid.
	if pendingData["session_id"] != "" {
		t.Fatalf("pending session_id = %#v, want empty", pendingData["session_id"])
	}

	// Encode round-trip: delay_ms present, no raw body.
	var envelope struct {
		Data struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
			Beats     []struct {
				BeatID     string `json:"beat_id"`
				Order      int    `json:"order"`
				RawEventID string `json:"raw_event_id"`
				DelayMS    int64  `json:"delay_ms"`
			} `json:"beats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(pending.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data.Beats) != 1 || envelope.Data.Beats[0].DelayMS != 1500 {
		t.Fatalf("beats shape = %#v", envelope.Data.Beats)
	}
}
