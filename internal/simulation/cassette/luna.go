package cassette

import (
	"context"
	"fmt"
	"sync"

	"mosaic.local/mosaic/internal/openaimodel"
)

// LunaCassette decorates an openaimodel.LunaStructuredClient with record/replay
// behavior. It is safe for sequential demo use; concurrent Normalize calls
// serialize on mu.
//
// Composition wraps a live client the same way as Terra/Sol:
//
//	client := cassette.NewLuna(cassette.ModeRecord, store, live)
//
// Set BeatID, PromptVersion, and PromptHash on the decorator between calls when
// simulation context or prompt provenance must be banked.
type LunaCassette struct {
	Inner openaimodel.LunaStructuredClient
	Mode  Mode
	Store Store

	// BeatID is optional simulation context mixed into the recording key.
	BeatID string

	// PromptVersion and PromptHash are copied onto new recordings in ModeRecord.
	// ModeReplay overwrites them from the banked recording after a successful Get.
	PromptVersion string
	PromptHash    string

	mu sync.Mutex
}

// NewLuna constructs a LunaCassette. Inner may be nil only when Mode is
// ModeReplay (responses come exclusively from Store).
func NewLuna(mode Mode, store Store, inner openaimodel.LunaStructuredClient) *LunaCassette {
	return &LunaCassette{Inner: inner, Mode: mode, Store: store}
}

// Normalize implements openaimodel.LunaStructuredClient.
func (c *LunaCassette) Normalize(ctx context.Context, req openaimodel.LunaRequest) (openaimodel.LunaResponse, error) {
	if c == nil {
		return openaimodel.LunaResponse{}, fmt.Errorf("cassette: LunaCassette is nil")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	meta := KeyMeta{BeatID: c.BeatID}
	key, fingerprint, err := LunaKey(req, meta)
	if err != nil {
		return openaimodel.LunaResponse{}, fmt.Errorf("cassette: luna key: %w", err)
	}

	switch c.Mode {
	case ModePassthrough:
		return c.callInner(ctx, req)
	case ModeReplay:
		return c.replay(ctx, key)
	case ModeRecord:
		return c.record(ctx, req, key, fingerprint, meta)
	default:
		return openaimodel.LunaResponse{}, fmt.Errorf("cassette: unsupported mode %s", c.Mode)
	}
}

func (c *LunaCassette) callInner(ctx context.Context, req openaimodel.LunaRequest) (openaimodel.LunaResponse, error) {
	if c.Inner == nil {
		return openaimodel.LunaResponse{}, ErrInnerRequired
	}
	return c.Inner.Normalize(ctx, req)
}

func (c *LunaCassette) replay(ctx context.Context, key string) (openaimodel.LunaResponse, error) {
	if c.Store == nil {
		return openaimodel.LunaResponse{}, ErrStoreRequired
	}
	rec, err := c.Store.Get(ctx, key)
	if err != nil {
		return openaimodel.LunaResponse{}, err
	}
	c.PromptVersion = rec.PromptVersion
	c.PromptHash = rec.PromptHash
	return openaimodel.LunaResponse{
		ResultJSON:         cloneRaw(rec.ResultJSON),
		CanonicalEventJSON: cloneRaw(rec.CanonicalEventJSON),
		ResponseID:         rec.ResponseID,
		RefusalDetail:      rec.RefusalDetail,
	}, nil
}

func (c *LunaCassette) record(ctx context.Context, req openaimodel.LunaRequest, key, fingerprint string, meta KeyMeta) (openaimodel.LunaResponse, error) {
	if c.Store == nil {
		return openaimodel.LunaResponse{}, ErrStoreRequired
	}
	resp, err := c.callInner(ctx, req)
	if err != nil {
		// Do not bank failed transports; caller sees the live error.
		return openaimodel.LunaResponse{}, err
	}
	rec := &Recording{
		SchemaVersion: SchemaVersion,
		Key:           key,
		Agent:         agentLuna,
		// Luna is not COP-revision-scoped; StateRevision stays 0.
		StateRevision:      0,
		BeatID:             meta.BeatID,
		RequestFingerprint: fingerprint,
		PromptVersion:      c.PromptVersion,
		PromptHash:         c.PromptHash,
		ResponseID:         resp.ResponseID,
		RefusalDetail:      resp.RefusalDetail,
		ResultJSON:         cloneRaw(resp.ResultJSON),
		CanonicalEventJSON: cloneRaw(resp.CanonicalEventJSON),
		RecordedAt:         nowUTC(),
	}
	if putErr := c.Store.Put(ctx, rec); putErr != nil {
		return openaimodel.LunaResponse{}, fmt.Errorf("cassette: persist luna recording: %w", putErr)
	}
	return resp, nil
}

// Compile-time check: LunaCassette is an openaimodel.LunaStructuredClient.
var _ openaimodel.LunaStructuredClient = (*LunaCassette)(nil)
