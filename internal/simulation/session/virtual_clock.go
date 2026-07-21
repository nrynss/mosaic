package session

import (
	"sync"
	"time"
)

// VirtualClock is an injectable clock/timer for deterministic tests. Now and
// After are safe for concurrent use. Advance moves the clock forward and
// fires any timers whose deadline has been reached.
//
// Wire into a controller with:
//
//	cfg.Clock = clock.Now
//	cfg.After = clock.After
type VirtualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*virtualTimer
}

type virtualTimer struct {
	when    time.Time
	ch      chan time.Time
	stopped bool
}

// NewVirtualClock returns a virtual clock starting at start (normalized to UTC).
func NewVirtualClock(start time.Time) *VirtualClock {
	return &VirtualClock{now: start.UTC()}
}

// Now returns the current virtual time.
func (v *VirtualClock) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.now
}

// After returns a channel that receives once when the virtual clock reaches
// now+d (or immediately when d <= 0). The channel is buffered so fire does not
// require a concurrent receiver.
func (v *VirtualClock) After(d time.Duration) <-chan time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()

	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- v.now
		return ch
	}
	v.timers = append(v.timers, &virtualTimer{
		when: v.now.Add(d),
		ch:   ch,
	})
	return ch
}

// Advance moves the clock forward by d (no-op when d <= 0) and delivers any
// timers whose deadline is at or before the new now.
func (v *VirtualClock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.now = v.now.Add(d)
	v.fireLocked()
}

// Set jumps the clock to t and fires due timers. Intended for tests.
func (v *VirtualClock) Set(t time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.now = t.UTC()
	v.fireLocked()
}

func (v *VirtualClock) fireLocked() {
	remaining := v.timers[:0]
	for _, timer := range v.timers {
		if timer.stopped {
			continue
		}
		if !timer.when.After(v.now) {
			timer.stopped = true
			select {
			case timer.ch <- v.now:
			default:
			}
			continue
		}
		remaining = append(remaining, timer)
	}
	// Clear unused tail references for GC.
	for i := len(remaining); i < len(v.timers); i++ {
		v.timers[i] = nil
	}
	v.timers = remaining
}
