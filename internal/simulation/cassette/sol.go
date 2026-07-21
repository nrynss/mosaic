package cassette

import (
	"context"
	"fmt"
	"sync"

	"mosaic.local/mosaic/internal/sol"
)

// SolCassette decorates a sol.StructuredClient with record/replay behavior.
// It is safe for sequential demo use; concurrent Brief calls serialize on mu.
//
// Composition (C5) wraps a live or fixture client:
//
//	client := &cassette.SolCassette{Inner: live, Mode: cassette.ModeRecord, Store: store}
//
// Set BeatID, PromptVersion, and PromptHash on the decorator between calls when
// the simulation needs beat-scoped keys or prompt provenance fields.
type SolCassette struct {
	Inner sol.StructuredClient
	Mode  Mode
	Store Store

	// BeatID is optional simulation context mixed into the recording key.
	BeatID string

	// PromptVersion and PromptHash are copied onto new recordings in ModeRecord.
	// ModeReplay overwrites them from the banked recording after a successful Get
	// so callers can read back honest provenance.
	PromptVersion string
	PromptHash    string

	mu sync.Mutex
}

// NewSol constructs a SolCassette. Inner may be nil only when Mode is
// ModeReplay (responses come exclusively from Store).
func NewSol(mode Mode, store Store, inner sol.StructuredClient) *SolCassette {
	return &SolCassette{Inner: inner, Mode: mode, Store: store}
}

// Brief implements sol.StructuredClient.
func (c *SolCassette) Brief(ctx context.Context, req sol.Request) (sol.Response, error) {
	if c == nil {
		return sol.Response{}, fmt.Errorf("cassette: SolCassette is nil")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	meta := KeyMeta{BeatID: c.BeatID}
	key, fingerprint, err := SolKey(req, meta)
	if err != nil {
		return sol.Response{}, fmt.Errorf("cassette: sol key: %w", err)
	}

	switch c.Mode {
	case ModePassthrough:
		return c.callInner(ctx, req)
	case ModeReplay:
		return c.replay(ctx, key)
	case ModeRecord:
		return c.record(ctx, req, key, fingerprint, meta)
	default:
		return sol.Response{}, fmt.Errorf("cassette: unsupported mode %s", c.Mode)
	}
}

func (c *SolCassette) callInner(ctx context.Context, req sol.Request) (sol.Response, error) {
	if c.Inner == nil {
		return sol.Response{}, ErrInnerRequired
	}
	return c.Inner.Brief(ctx, req)
}

func (c *SolCassette) replay(ctx context.Context, key string) (sol.Response, error) {
	if c.Store == nil {
		return sol.Response{}, ErrStoreRequired
	}
	rec, err := c.Store.Get(ctx, key)
	if err != nil {
		return sol.Response{}, err
	}
	// Restore banked prompt provenance onto the decorator for honest readback.
	c.PromptVersion = rec.PromptVersion
	c.PromptHash = rec.PromptHash
	return sol.Response{
		RecommendationJSON: cloneRaw(rec.RecommendationJSON),
		ResponseID:         rec.ResponseID,
		RefusalDetail:      rec.RefusalDetail,
	}, nil
}

func (c *SolCassette) record(ctx context.Context, req sol.Request, key, fingerprint string, meta KeyMeta) (sol.Response, error) {
	if c.Store == nil {
		return sol.Response{}, ErrStoreRequired
	}
	resp, err := c.callInner(ctx, req)
	if err != nil {
		// Do not bank failed transports; caller sees the live error.
		return sol.Response{}, err
	}
	rec := &Recording{
		SchemaVersion:      SchemaVersion,
		Key:                key,
		Agent:              agentSol,
		StateRevision:      req.StateRevision,
		BeatID:             meta.BeatID,
		RequestFingerprint: fingerprint,
		PromptVersion:      c.PromptVersion,
		PromptHash:         c.PromptHash,
		ResponseID:         resp.ResponseID,
		RefusalDetail:      resp.RefusalDetail,
		RecommendationJSON: cloneRaw(resp.RecommendationJSON),
		RecordedAt:         nowUTC(),
	}
	if putErr := c.Store.Put(ctx, rec); putErr != nil {
		return sol.Response{}, fmt.Errorf("cassette: persist sol recording: %w", putErr)
	}
	return resp, nil
}

// Compile-time check: SolCassette is a sol.StructuredClient.
var _ sol.StructuredClient = (*SolCassette)(nil)
