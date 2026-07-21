package session

import (
	"strings"
	"sync"

	"mosaic.local/mosaic/internal/contracts"
)

// Compile-time: ActiveSession is the in-process ActiveSessionSource.
var _ contracts.ActiveSessionSource = (*ActiveSession)(nil)

// ActiveSession is a thread-safe in-process holder for the simulation session
// epoch the API should show. Composition shares one instance between the
// controller (writer on Start/Reset/End) and API read ports (COP, advisories).
//
// SQLite demos use this holder only — no durable active pointer is required.
// Postgres compositions may also mirror the pointer into session_epoch tables
// (migration 0004) without changing this type.
//
// Empty / cleared → API returns an empty board (no COP, no advisories).
type ActiveSession struct {
	mu sync.RWMutex
	id string
}

// NewActiveSession returns an empty holder (no active session).
func NewActiveSession() *ActiveSession {
	return &ActiveSession{}
}

// Set records sessionID as the active epoch. Whitespace-only ids clear.
func (a *ActiveSession) Set(sessionID string) {
	if a == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	a.mu.Lock()
	a.id = sessionID
	a.mu.Unlock()
}

// Clear removes the active session so read ports return an empty board.
func (a *ActiveSession) Clear() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.id = ""
	a.mu.Unlock()
}

// ActiveSessionID implements contracts.ActiveSessionSource.
func (a *ActiveSession) ActiveSessionID() (sessionID string, active bool) {
	if a == nil {
		return "", false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.id == "" {
		return "", false
	}
	return a.id, true
}
