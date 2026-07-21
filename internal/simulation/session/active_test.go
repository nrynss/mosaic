package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
)

func TestActiveSessionSetClear(t *testing.T) {
	active := NewActiveSession()
	if id, ok := active.ActiveSessionID(); ok || id != "" {
		t.Fatalf("empty holder = (%q, %v)", id, ok)
	}
	active.Set("sim-1")
	id, ok := active.ActiveSessionID()
	if !ok || id != "sim-1" {
		t.Fatalf("after Set = (%q, %v), want (sim-1, true)", id, ok)
	}
	active.Clear()
	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("after Clear still active")
	}
	active.Set("  ")
	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("whitespace Set should clear/ignore")
	}
}

func TestControllerStartSetsActiveEndClears(t *testing.T) {
	active := NewActiveSession()
	ctrl := newTestController(t, testBeats(), func(cfg *Config) {
		cfg.Active = active
		cfg.NewSessionID = func() string { return "sim-epoch-1" }
	})

	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("active set before Start")
	}

	session, err := ctrl.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id, ok := active.ActiveSessionID()
	if !ok || id != "sim-epoch-1" || id != session.SessionID {
		t.Fatalf("active after Start = (%q, %v), session=%q", id, ok, session.SessionID)
	}

	// Natural end leaves active set so the final board remains visible.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Status().Status == contracts.SessionEnded {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ctrl.Status().Status != contracts.SessionEnded {
		t.Fatal("session did not end")
	}
	if id, ok := active.ActiveSessionID(); !ok || id != "sim-epoch-1" {
		t.Fatalf("active after natural end = (%q, %v), want still sim-epoch-1", id, ok)
	}

	if _, err := ctrl.End(context.Background()); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("active still set after explicit End")
	}
}

func TestControllerResetReplacesActiveSessionIDs(t *testing.T) {
	active := NewActiveSession()
	ids := []string{"sim-a", "sim-b"}
	i := 0
	ctrl := newTestController(t, []contracts.ScheduledBeat{
		{BeatID: "b1", Order: 1, RawEventID: "r1", Delay: time.Hour},
	}, func(cfg *Config) {
		cfg.Active = active
		cfg.NewSessionID = func() string {
			id := ids[i]
			if i < len(ids)-1 {
				i++
			}
			return id
		}
	})

	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id, ok := active.ActiveSessionID(); !ok || id != "sim-a" {
		t.Fatalf("after Start = (%q, %v)", id, ok)
	}

	if _, err := ctrl.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if id, ok := active.ActiveSessionID(); !ok || id != "sim-b" {
		t.Fatalf("after Reset = (%q, %v), want sim-b", id, ok)
	}
}

func TestActiveSessionConcurrentAccess(t *testing.T) {
	active := NewActiveSession()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				active.Set("sim-x")
			} else {
				_, _ = active.ActiveSessionID()
			}
		}(i)
	}
	wg.Wait()
	active.Clear()
	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("expected clear after concurrent set")
	}
}
