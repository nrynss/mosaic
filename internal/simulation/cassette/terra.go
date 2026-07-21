package cassette

import (
	"context"
	"fmt"
	"sync"

	"mosaic.local/mosaic/internal/terra"
)

// TerraCassette decorates a terra.StructuredClient with record/replay behavior.
// It is safe for sequential demo use; concurrent Assess calls serialize on mu.
//
// Composition (C5) wraps a live or fixture client:
//
//	client := &cassette.TerraCassette{Inner: live, Mode: cassette.ModeRecord, Store: store}
//
// Set BeatID, PromptVersion, and PromptHash on the decorator between calls when
// the simulation needs beat-scoped keys or H6 provenance fields.
type TerraCassette struct {
	Inner terra.StructuredClient
	Mode  Mode
	Store Store

	// BeatID is optional simulation context mixed into the recording key.
	BeatID string

	// PromptVersion and PromptHash are H6 hooks copied onto new recordings.
	// Leave empty until prompt provenance wiring lands.
	PromptVersion string
	PromptHash    string

	mu sync.Mutex
}

// NewTerra constructs a TerraCassette. Inner may be nil only when Mode is
// ModeReplay (responses come exclusively from Store).
func NewTerra(mode Mode, store Store, inner terra.StructuredClient) *TerraCassette {
	return &TerraCassette{Inner: inner, Mode: mode, Store: store}
}

// Assess implements terra.StructuredClient.
func (c *TerraCassette) Assess(ctx context.Context, req terra.Request) (terra.Response, error) {
	if c == nil {
		return terra.Response{}, fmt.Errorf("cassette: TerraCassette is nil")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	meta := KeyMeta{BeatID: c.BeatID}
	key, fingerprint, err := TerraKey(req, meta)
	if err != nil {
		return terra.Response{}, fmt.Errorf("cassette: terra key: %w", err)
	}

	switch c.Mode {
	case ModePassthrough:
		return c.callInner(ctx, req)
	case ModeReplay:
		return c.replay(ctx, key)
	case ModeRecord:
		return c.record(ctx, req, key, fingerprint, meta)
	default:
		return terra.Response{}, fmt.Errorf("cassette: unsupported mode %s", c.Mode)
	}
}

func (c *TerraCassette) callInner(ctx context.Context, req terra.Request) (terra.Response, error) {
	if c.Inner == nil {
		return terra.Response{}, ErrInnerRequired
	}
	return c.Inner.Assess(ctx, req)
}

func (c *TerraCassette) replay(ctx context.Context, key string) (terra.Response, error) {
	if c.Store == nil {
		return terra.Response{}, ErrStoreRequired
	}
	rec, err := c.Store.Get(ctx, key)
	if err != nil {
		return terra.Response{}, err
	}
	return terra.Response{
		InsightJSON:   cloneRaw(rec.InsightJSON),
		ResponseID:    rec.ResponseID,
		RefusalDetail: rec.RefusalDetail,
	}, nil
}

func (c *TerraCassette) record(ctx context.Context, req terra.Request, key, fingerprint string, meta KeyMeta) (terra.Response, error) {
	if c.Store == nil {
		return terra.Response{}, ErrStoreRequired
	}
	resp, err := c.callInner(ctx, req)
	if err != nil {
		// Do not bank failed transports; caller sees the live error.
		return terra.Response{}, err
	}
	rec := &Recording{
		SchemaVersion:      SchemaVersion,
		Key:                key,
		Agent:              agentTerra,
		StateRevision:      req.StateRevision,
		BeatID:             meta.BeatID,
		RequestFingerprint: fingerprint,
		PromptVersion:      c.PromptVersion,
		PromptHash:         c.PromptHash,
		ResponseID:         resp.ResponseID,
		RefusalDetail:      resp.RefusalDetail,
		InsightJSON:        cloneRaw(resp.InsightJSON),
		RecordedAt:         nowUTC(),
	}
	if putErr := c.Store.Put(ctx, rec); putErr != nil {
		return terra.Response{}, fmt.Errorf("cassette: persist terra recording: %w", putErr)
	}
	return resp, nil
}

// Compile-time check: TerraCassette is a terra.StructuredClient.
var _ terra.StructuredClient = (*TerraCassette)(nil)
