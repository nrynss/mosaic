package simulation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/eventlog"
)

// Event type label appended by BeatExecutor. Consumers dispatch on this
// application-level type; the transport treats it as opaque.
const EventTypeRawEvent = "raw.event"

// DefaultPartitionKey is used when ExecutorConfig.PartitionKey is empty.
// Composition should set a real incident (or domain) key for per-key ordering;
// the framework default is a single simulation partition, not a dataset path.
const DefaultPartitionKey = "simulation"

// BeatSource loads frozen raw-event payload bytes for a raw_event_id.
// Injected by composition — simulation never hardcodes dataset filesystem paths.
type BeatSource interface {
	// Payload returns the opaque serialized raw event body for rawEventID.
	// A missing id should return a non-nil error; BeatExecutor will not Append.
	Payload(ctx context.Context, rawEventID string) ([]byte, error)
}

// BeatSourceFunc adapts a function to BeatSource.
type BeatSourceFunc func(ctx context.Context, rawEventID string) ([]byte, error)

// Payload implements BeatSource.
func (f BeatSourceFunc) Payload(ctx context.Context, rawEventID string) ([]byte, error) {
	return f(ctx, rawEventID)
}

// BeatAppended is optional post-Append notification (tests, SSE bridge, etc.).
// It is invoked only after a successful EventLog.Append.
type BeatAppended func(beat contracts.ScheduledBeat, env eventlog.EventEnvelope)

var (
	// ErrNilEventLog means ExecutorConfig.Log was not provided.
	ErrNilEventLog = errors.New("simulation: event log is required")
	// ErrNilBeatSource means ExecutorConfig.Source was not provided.
	ErrNilBeatSource = errors.New("simulation: beat source is required")
	// ErrNilSchedule means ExecutorConfig.Schedule was not provided.
	ErrNilSchedule = errors.New("simulation: schedule is required")
)

// ExecutorConfig wires a BeatExecutor.
//
// # Pacing ownership
//
// BeatExecutor is the sole owner of Append-path pacing. Presentation delays in
// frozen scenario.json (delay_ms) are NOT the interactive schedule; they remain
// available only when UseScheduleDelays is true (tests / explicit override).
//
// Default interactive rule (UseScheduleDelays=false, Burst=false):
//
//	ordered beats sorted by Order ascending; beat at index i fires after
//	i * BeatSpacing from run start (Equal spacing). Fixture Delay is ignored.
//
// Burst=true: every beat appends with zero inter-beat wait (EventLog stress).
//
// UseScheduleDelays=true: each beat waits until start+beat.Delay (historical
// relative-to-start model). Useful for unit tests that set explicit Delays.
//
// A consumer/projector (B3/B5 path) advances the COP after Append; this type
// does not run Luna, Terra, or Sol. Advisory hooks at rev 7/9 are C4/C5.
type ExecutorConfig struct {
	Schedule contracts.SimulationSchedule
	Log      eventlog.EventLog
	Source   BeatSource

	// PartitionKey is written on every envelope. Empty → DefaultPartitionKey.
	// Prefer the incident id (or a stable domain key) so the projector can
	// order per-incident; raw fixture payloads do not carry a structured
	// incident_id at the envelope layer.
	PartitionKey string

	// BeatSpacing is the equal spacing between successive beats. Zero selects
	// DefaultBeatSpacing unless Burst is true (then spacing is irrelevant).
	BeatSpacing time.Duration

	// Burst appends every scheduled beat with zero delay (stress mode).
	// Disabled by default. Env: MOSAIC_SIM_BURST.
	Burst bool

	// UseScheduleDelays uses each ScheduledBeat.Delay relative to run start
	// instead of equal spacing. Ignored when Burst is true.
	UseScheduleDelays bool

	// Clock and After are injectable for deterministic tests (VirtualClock).
	Clock func() time.Time
	After func(d time.Duration) <-chan time.Time

	// OnBeat is called after each successful Append. Optional.
	OnBeat BeatAppended
}

// BeatExecutor appends one EventLog envelope per scheduled beat, in Order,
// with cumulative equal-spacing (or burst / schedule-delay override).
type BeatExecutor struct {
	schedule          contracts.SimulationSchedule
	log               eventlog.EventLog
	source            BeatSource
	partitionKey      string
	beatSpacing       time.Duration
	burst             bool
	useScheduleDelays bool
	clock             func() time.Time
	after             func(d time.Duration) <-chan time.Time
	onBeat            BeatAppended
}

// NewBeatExecutor validates config and returns a ready executor.
func NewBeatExecutor(cfg ExecutorConfig) (*BeatExecutor, error) {
	if cfg.Schedule == nil {
		return nil, ErrNilSchedule
	}
	if cfg.Log == nil {
		return nil, ErrNilEventLog
	}
	if cfg.Source == nil {
		return nil, ErrNilBeatSource
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	after := cfg.After
	if after == nil {
		after = time.After
	}
	spacing := cfg.BeatSpacing
	if spacing <= 0 {
		spacing = DefaultBeatSpacing
	}
	pk := cfg.PartitionKey
	if pk == "" {
		pk = DefaultPartitionKey
	}
	return &BeatExecutor{
		schedule:          cfg.Schedule,
		log:               cfg.Log,
		source:            cfg.Source,
		partitionKey:      pk,
		beatSpacing:       spacing,
		burst:             cfg.Burst,
		useScheduleDelays: cfg.UseScheduleDelays,
		clock:             clock,
		after:             after,
		onBeat:            cfg.OnBeat,
	}, nil
}

// Run walks the schedule in Order, waits per the pacing rule, loads each raw
// payload, and Appends one envelope per beat. It returns on the first error
// (load or append) or when ctx is cancelled. Partial appends already committed
// are not rolled back (EventLog is append-only); callers rely on IdempotencyKey
// for safe retry.
func (e *BeatExecutor) Run(ctx context.Context) error {
	if e == nil {
		return errors.New("simulation: nil beat executor")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	ordered := copyAndSortBeats(e.schedule.Beats())
	start := e.clock().UTC()

	for i, beat := range ordered {
		delay := e.delayFor(i, beat)
		if err := e.waitUntil(ctx, start, delay); err != nil {
			return err
		}
		if err := e.appendBeat(ctx, beat); err != nil {
			return err
		}
	}
	return nil
}

// AppendBeat loads and appends a single beat without pacing. Useful for tests
// and for composition that owns its own schedule loop.
func (e *BeatExecutor) AppendBeat(ctx context.Context, beat contracts.ScheduledBeat) error {
	if e == nil {
		return errors.New("simulation: nil beat executor")
	}
	return e.appendBeat(ctx, beat)
}

// delayFor implements the documented pacing rule for sorted index i.
func (e *BeatExecutor) delayFor(index int, beat contracts.ScheduledBeat) time.Duration {
	if e.burst {
		return 0
	}
	if e.useScheduleDelays {
		if beat.Delay < 0 {
			return 0
		}
		return beat.Delay
	}
	return EqualSpacingDelay(index, e.beatSpacing)
}

func (e *BeatExecutor) appendBeat(ctx context.Context, beat contracts.ScheduledBeat) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rawID := beat.RawEventID
	if rawID == "" {
		return fmt.Errorf("simulation: beat %q has empty raw_event_id", beat.BeatID)
	}
	payload, err := e.source.Payload(ctx, rawID)
	if err != nil {
		return fmt.Errorf("simulation: load raw event %q: %w", rawID, err)
	}
	if payload == nil {
		// Match EventLog backends that store nil as empty BYTEA; avoid
		// nil-vs-empty ambiguity at the envelope layer.
		payload = []byte{}
	}
	env := eventlog.EventEnvelope{
		PartitionKey:   e.partitionKey,
		IdempotencyKey: rawID, // stable source identity; at-least-once safe
		Type:           EventTypeRawEvent,
		Payload:        payload,
	}
	if err := e.log.Append(ctx, env); err != nil {
		return fmt.Errorf("simulation: append beat %q: %w", beat.BeatID, err)
	}
	if e.onBeat != nil {
		e.onBeat(beat, env)
	}
	return nil
}

// waitUntil blocks until clock reaches start+delay or ctx is cancelled.
// Mirrors session.waitUntil so VirtualClock / fake After work the same way.
func (e *BeatExecutor) waitUntil(ctx context.Context, start time.Time, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := e.clock().UTC()
		remaining := delay - now.Sub(start)
		if remaining <= 0 {
			return nil
		}
		timer := e.after(remaining)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer:
			later := e.clock().UTC()
			if !later.After(now) {
				// After fired without clock advance (immediate fake): done.
				return nil
			}
		}
	}
}

func copyAndSortBeats(beats []contracts.ScheduledBeat) []contracts.ScheduledBeat {
	if len(beats) == 0 {
		return nil
	}
	out := make([]contracts.ScheduledBeat, len(beats))
	copy(out, beats)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Order < out[j].Order
	})
	return out
}
