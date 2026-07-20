// Package simsession implements a domain-agnostic interactive simulation
// controller. It owns session lifecycle (start/status/end/reset) and emits
// schedule-driven beats on a session-scoped stream.
//
// Delay model: each ScheduledBeat.Delay is relative to session start time
// (not cumulative between beats). A beat with Delay=2s fires two seconds after
// the session starts, regardless of earlier beats. Emission order follows the
// Order field (ascending), not the schedule slice order.
//
// Stream backpressure: each subscriber has a bounded buffer (default 64). When
// the buffer is full the oldest pending event is dropped so a slow consumer
// cannot block the controller indefinitely (drop-oldest).
//
// The controller holds no durable immutable event store. Reset creates a new
// SessionID and a fresh stream sequence; it never rewrites or truncates prior
// session history because none is retained here.
package simsession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"mosaic.local/mosaic/internal/contracts"
)

const defaultSubscriberBuffer = 64

var (
	// ErrNilSchedule means Config.Schedule was not provided.
	ErrNilSchedule = errors.New("simulation schedule is required")
	// ErrAlreadyRunning means Start was called while a session is running.
	// Use Reset to end the current session and begin a new one.
	ErrAlreadyRunning = errors.New("simulation session already running")
)

// Config wires a controller. Schedule is required; clock and timers are
// injectable so tests can prove deterministic timing without real sleeps.
type Config struct {
	Schedule contracts.SimulationSchedule

	// Clock returns the current time for event timestamps and delay math.
	// Defaults to time.Now.
	Clock func() time.Time

	// After returns a channel that receives once after duration d (like
	// time.After). Defaults to time.After. Inject VirtualClock.After for
	// deterministic, advanceable timing in tests.
	After func(d time.Duration) <-chan time.Time

	// NewSessionID creates a unique session identifier. Defaults to a random
	// hex id with a "sim-" prefix.
	NewSessionID func() string

	// SubscriberBuffer is the per-subscriber channel capacity. Zero selects
	// the package default. Values less than 1 are treated as 1.
	SubscriberBuffer int
}

// Controller is a session-scoped simulation lifecycle engine. Start/End/Reset
// are serialized; Status is safe for concurrent readers.
type Controller struct {
	schedule         contracts.SimulationSchedule
	clock            func() time.Time
	after            func(d time.Duration) <-chan time.Time
	newSessionID     func() string
	subscriberBuffer int

	mu        sync.RWMutex
	session   contracts.SimulationSession
	sequence  int64
	runCancel context.CancelFunc
	runDone   chan struct{}

	nextSubID uint64
	subs      map[uint64]chan contracts.SimulationStreamEvent
}

// Subscription is a caller-owned registration on the session-scoped stream.
// Call Cancel when finished; it is safe to call more than once.
type Subscription struct {
	Events <-chan contracts.SimulationStreamEvent

	once   sync.Once
	cancel func()
}

// New constructs a controller in pending status with a copy of the schedule
// beats. No session id is assigned until Start or Reset.
func New(config Config) (*Controller, error) {
	if config.Schedule == nil {
		return nil, ErrNilSchedule
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	after := config.After
	if after == nil {
		after = time.After
	}
	newID := config.NewSessionID
	if newID == nil {
		newID = randomSessionID
	}
	buf := config.SubscriberBuffer
	if buf == 0 {
		buf = defaultSubscriberBuffer
	}
	if buf < 1 {
		buf = 1
	}

	beats := copyBeats(config.Schedule.Beats())
	return &Controller{
		schedule:         config.Schedule,
		clock:            clock,
		after:            after,
		newSessionID:     newID,
		subscriberBuffer: buf,
		session: contracts.SimulationSession{
			Status: contracts.SessionPending,
			Beats:  beats,
		},
		subs: make(map[uint64]chan contracts.SimulationStreamEvent),
	}, nil
}

// Start creates a new synthetic session, transitions pending→running, emits
// workspace_clear then a status_change, and begins schedule-driven beat
// emission. It fails if a session is already running (use Reset).
func (c *Controller) Start(ctx context.Context) (contracts.SimulationSession, error) {
	if c == nil {
		return contracts.SimulationSession{}, errors.New("nil controller")
	}
	if err := ctx.Err(); err != nil {
		return contracts.SimulationSession{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session.Status == contracts.SessionRunning {
		return c.snapshotLocked(), ErrAlreadyRunning
	}
	return c.startLocked()
}

// Status returns a copy of the current session (id, status, beats).
func (c *Controller) Status() contracts.SimulationSession {
	if c == nil {
		return contracts.SimulationSession{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

// End cancels in-flight beat emission and transitions the active session to
// ended, emitting status_change. It is idempotent when not running.
func (c *Controller) End(ctx context.Context) (contracts.SimulationSession, error) {
	if c == nil {
		return contracts.SimulationSession{}, errors.New("nil controller")
	}
	_ = ctx // reserved for future deadline propagation; cancel is internal

	cancel, done := c.beginStop()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.Status == contracts.SessionRunning {
		c.session.Status = contracts.SessionEnded
		c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
			"status": string(contracts.SessionEnded),
		})
	}
	return c.snapshotLocked(), nil
}

// Reset ends the current session if needed and starts a new one with a fresh
// SessionID and stream sequence. Prior session stream signals are not
// re-emitted; the controller does not retain durable immutable history.
func (c *Controller) Reset(ctx context.Context) (contracts.SimulationSession, error) {
	if c == nil {
		return contracts.SimulationSession{}, errors.New("nil controller")
	}
	if err := ctx.Err(); err != nil {
		return contracts.SimulationSession{}, err
	}

	cancel, done := c.beginStop()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session.Status == contracts.SessionRunning {
		c.session.Status = contracts.SessionEnded
		c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
			"status": string(contracts.SessionEnded),
		})
	}
	return c.startLocked()
}

// Subscribe registers a bounded local subscriber. Events carry the active
// session's SessionID. The caller must Cancel the subscription.
func (c *Controller) Subscribe() *Subscription {
	if c == nil {
		return &Subscription{Events: closedEvents()}
	}

	c.mu.Lock()
	id := c.nextSubID
	c.nextSubID++
	ch := make(chan contracts.SimulationStreamEvent, c.subscriberBuffer)
	c.subs[id] = ch
	c.mu.Unlock()

	return &Subscription{
		Events: ch,
		cancel: func() {
			c.mu.Lock()
			if existing, ok := c.subs[id]; ok {
				delete(c.subs, id)
				close(existing)
			}
			c.mu.Unlock()
		},
	}
}

// Cancel removes the subscriber. Safe to call more than once.
func (s *Subscription) Cancel() {
	if s == nil || s.cancel == nil {
		return
	}
	s.once.Do(s.cancel)
}

// startLocked assumes c.mu is held and the controller is not running.
func (c *Controller) startLocked() (contracts.SimulationSession, error) {
	// Ensure any prior runner slot is clear (status should not be running).
	c.runCancel = nil
	c.runDone = nil

	beats := copyBeats(c.schedule.Beats())
	sessionID := c.newSessionID()
	if sessionID == "" {
		return c.snapshotLocked(), errors.New("session id generator returned empty id")
	}

	c.session = contracts.SimulationSession{
		SessionID: sessionID,
		Status:    contracts.SessionRunning,
		Beats:     beats,
	}
	c.sequence = 0

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.runCancel = cancel
	c.runDone = done
	startTime := c.clock().UTC()

	c.publishLocked(contracts.StreamEventWorkspaceClear, nil)
	c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
		"status": string(contracts.SessionRunning),
	})

	go c.runBeats(runCtx, done, sessionID, beats, startTime)

	return c.snapshotLocked(), nil
}

// beginStop captures cancel/done under lock so End/Reset can wait without
// holding the mutex (avoids deadlock with the runner).
func (c *Controller) beginStop() (context.CancelFunc, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cancel := c.runCancel
	done := c.runDone
	c.runCancel = nil
	// leave runDone for the waiter; runner closes it
	return cancel, done
}

func (c *Controller) runBeats(
	ctx context.Context,
	done chan struct{},
	sessionID string,
	beats []contracts.ScheduledBeat,
	startTime time.Time,
) {
	defer close(done)

	ordered := copyBeats(beats)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Order < ordered[j].Order
	})

	completed := true
	for _, beat := range ordered {
		if err := c.waitUntil(ctx, startTime, beat.Delay); err != nil {
			completed = false
			break
		}
		if !c.emitBeat(sessionID, beat) {
			completed = false
			break
		}
	}

	if !completed {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.SessionID != sessionID || c.session.Status != contracts.SessionRunning {
		return
	}
	c.session.Status = contracts.SessionEnded
	c.runCancel = nil
	c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
		"status": string(contracts.SessionEnded),
	})
}

// waitUntil blocks until clock reaches startTime+delay or ctx is cancelled.
// Delay is relative to session start.
//
// Waiting is driven by the injectable After. When After fires without the
// clock advancing (valid for some test fakes), the wait completes so a frozen
// Clock cannot spin forever. When the clock does advance (VirtualClock), the
// remaining delay is re-checked so partial advances keep waiting.
func (c *Controller) waitUntil(ctx context.Context, startTime time.Time, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := c.clock().UTC()
		remaining := delay - now.Sub(startTime)
		if remaining <= 0 {
			return nil
		}
		timer := c.after(remaining)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer:
			later := c.clock().UTC()
			if !later.After(now) {
				return nil
			}
		}
	}
}

// emitBeat publishes a beat event when the session is still the active one.
// Returns false if the session was replaced or cancelled.
func (c *Controller) emitBeat(sessionID string, beat contracts.ScheduledBeat) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.SessionID != sessionID || c.session.Status != contracts.SessionRunning {
		return false
	}
	c.publishLocked(contracts.StreamEventBeat, map[string]any{
		"beat_id":      beat.BeatID,
		"order":        beat.Order,
		"raw_event_id": beat.RawEventID,
	})
	return true
}

func (c *Controller) publishLocked(eventType contracts.StreamEventType, payload any) {
	c.sequence++
	event := contracts.SimulationStreamEvent{
		SessionID: c.session.SessionID,
		Sequence:  c.sequence,
		Timestamp: c.clock().UTC(),
		Type:      eventType,
		Payload:   payload,
	}
	for _, ch := range c.subs {
		offerDropOldest(ch, event)
	}
}

func offerDropOldest(ch chan contracts.SimulationStreamEvent, event contracts.SimulationStreamEvent) {
	select {
	case ch <- event:
		return
	default:
	}
	// Drop oldest, then try once more without blocking the controller.
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- event:
	default:
	}
}

func (c *Controller) snapshotLocked() contracts.SimulationSession {
	return contracts.SimulationSession{
		SessionID: c.session.SessionID,
		Status:    c.session.Status,
		Beats:     copyBeats(c.session.Beats),
	}
}

func copyBeats(beats []contracts.ScheduledBeat) []contracts.ScheduledBeat {
	if len(beats) == 0 {
		return nil
	}
	out := make([]contracts.ScheduledBeat, len(beats))
	copy(out, beats)
	return out
}

func randomSessionID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("sim-%d", time.Now().UTC().UnixNano())
	}
	return "sim-" + hex.EncodeToString(bytes)
}

func closedEvents() <-chan contracts.SimulationStreamEvent {
	ch := make(chan contracts.SimulationStreamEvent)
	close(ch)
	return ch
}
