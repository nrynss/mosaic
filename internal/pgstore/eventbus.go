package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mosaic.local/mosaic/internal/eventlog"
)

// EventBus reconnect backoff bounds. Best-effort: after a connection drop the
// subscriber re-LISTENs; notes published during the gap are lost (recover via
// re-reading the read model).
const (
	eventBusReconnectMinBackoff = 50 * time.Millisecond
	eventBusReconnectMaxBackoff = 5 * time.Second
)

// Compile-time proof that EventBus satisfies the fan-out transport seam.
var _ eventlog.EventBus = (*EventBus)(nil)

const (
	// maxNotifyPayloadBytes is the largest NOTIFY payload we accept. Postgres
	// documents the limit as "less than 8000 bytes" (sql-notify); 7999 is the
	// largest size that is reliably accepted. Larger notes are rejected by
	// Publish so callers know the note never left.
	maxNotifyPayloadBytes = 7999

	// postgresChannelNameMax is NAMEDATALEN-1: the maximum length of an
	// unquoted Postgres identifier / NOTIFY channel name.
	postgresChannelNameMax = 63

	// channelPrefix namespaces every bus channel so application LISTEN usage
	// cannot collide with Mosaic fan-out topics.
	channelPrefix = "mb_"

	// hashChannelPrefix marks channels derived from a content hash when the
	// topic cannot be sanitized into a short, legal identifier.
	hashChannelPrefix = "mb_h_"

	// subscriberBufferSize is the per-subscription note buffer. When full the
	// listener drops the newest note (drop-new) so a slow reader never stalls
	// the LISTEN loop, publishers, or other subscribers. Matches the
	// best-effort spirit of stream.Broker (buffer + non-blocking offer).
	subscriberBufferSize = 16
)

// ErrNotifyPayloadTooLarge is returned by EventBus.Publish when note exceeds
// the Postgres NOTIFY payload limit (< 8000 bytes; we allow at most 7999).
// Callers should shrink the hint (e.g. send a revision id, not a snapshot).
var ErrNotifyPayloadTooLarge = errors.New("pgstore: NOTIFY payload exceeds 7999-byte limit")

// EventBus is the Postgres LISTEN/NOTIFY implementation of eventlog.EventBus.
//
// # Connection model
//
//   - Publish borrows a short-lived connection from the shared pool and runs
//     SELECT pg_notify(channel, payload). Success means Postgres accepted the
//     notification, not that any subscriber received it.
//   - Each Subscribe opens a dedicated *pgx.Conn cloned from the pool's
//     ConnConfig (including RuntimeParams such as search_path). Pooled
//     connections cannot safely LISTEN long-term: returning a LISTEN-ing conn
//     to the pool would leak session state to unrelated queries. The dedicated
//     connection is closed when the subscribe context is cancelled.
//
// There is one dedicated listener connection per Subscribe call (not a single
// multiplexed listener). That keeps cancellation and channel lifecycle simple
// and independent per subscriber; at demo fan-out scale the connection cost is
// negligible.
//
// # Topic → channel mapping
//
// See TopicChannel. Logical topics such as "cop.updated" become safe Postgres
// channel identifiers (e.g. "mb_cop_updated"). Long or illegal topics hash to a
// stable "mb_h_<hex>" name so they remain usable without colliding. Distinct
// topics that differ only by punctuation which collapses identically (e.g.
// "a.b" vs "a_b") share one channel — keep a controlled topic vocabulary.
//
// # Reconnect (best-effort)
//
// runSubscriber reconnects with exponential backoff when WaitForNotification
// fails for reasons other than subscribe-context cancellation. It re-LISTENs
// on the same TopicChannel name and keeps the out channel open until ctx is
// done. Notifications published while the listener is down are not replayed;
// subscribers must treat fan-out as a hint and re-load durable state.
//
// # Backpressure
//
// Each subscription owns a bounded channel of size subscriberBufferSize. When
// the buffer is full the listener drops the newest note rather than blocking.
// Missed notes are expected: subscribers recover by re-reading the read model.
type EventBus struct {
	pool *pgxpool.Pool
}

// NewEventBus returns a LISTEN/NOTIFY EventBus that publishes through pool and
// opens dedicated listen connections from pool.Config().ConnConfig. pool must
// be non-nil and remain open for the lifetime of the bus. A nil pool panics —
// fan-out misconfiguration should fail at composition, not on the first Publish.
func NewEventBus(pool *pgxpool.Pool) *EventBus {
	if pool == nil {
		panic("pgstore: NewEventBus requires a non-nil pool")
	}
	return &EventBus{pool: pool}
}

// Publish sends note to subscribers of topic via pg_notify. Delivery is
// best-effort: a nil error means Postgres accepted the notification.
func (b *EventBus) Publish(ctx context.Context, topic string, note []byte) error {
	if b == nil || b.pool == nil {
		return fmt.Errorf("%w: event bus is not configured", ErrInvalidRecord)
	}
	channel, err := TopicChannel(topic)
	if err != nil {
		return err
	}
	if len(note) > maxNotifyPayloadBytes {
		return fmt.Errorf("%w: note is %d bytes (max %d)", ErrNotifyPayloadTooLarge, len(note), maxNotifyPayloadBytes)
	}
	// nil note is a valid empty payload (same as zero-length).
	payload := string(note)
	if _, err := b.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload); err != nil {
		return fmt.Errorf("event bus notify %q: %w", topic, err)
	}
	return nil
}

// Subscribe LISTENs on topic's channel and returns a receive-only stream of
// note payloads. The channel is closed when ctx is cancelled; the caller must
// drain it. Buffering is bounded (see package docs).
func (b *EventBus) Subscribe(ctx context.Context, topic string) (<-chan []byte, error) {
	if b == nil || b.pool == nil {
		return nil, fmt.Errorf("%w: event bus is not configured", ErrInvalidRecord)
	}
	channel, err := TopicChannel(topic)
	if err != nil {
		return nil, err
	}

	conn, err := b.openListenConn(ctx)
	if err != nil {
		return nil, err
	}

	// Channel names from TopicChannel are restricted to [a-z0-9_]; quoting via
	// Identifier is defense-in-depth against future mapping changes.
	listenSQL := "LISTEN " + pgx.Identifier{channel}.Sanitize()
	if _, err := conn.Exec(ctx, listenSQL); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("event bus listen %q: %w", topic, err)
	}

	out := make(chan []byte, subscriberBufferSize)
	go b.runSubscriber(ctx, conn, channel, out)
	return out, nil
}

// openListenConn creates a dedicated connection for one Subscribe. It clones
// the pool ConnConfig so RuntimeParams (search_path, etc.) match the pool.
func (b *EventBus) openListenConn(ctx context.Context) (*pgx.Conn, error) {
	cfg := b.pool.Config().ConnConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("event bus listen connect: %w", err)
	}
	return conn, nil
}

// runSubscriber pumps NOTIFY payloads into out until ctx is done.
//
// On WaitForNotification errors other than ctx cancellation it closes the
// broken connection, reconnects with exponential backoff, re-issues LISTEN,
// and continues. The out channel stays open across reconnects so callers do
// not observe a closed stream until their subscribe context ends. This is
// best-effort fan-out: gaps during disconnect are not replayed.
func (b *EventBus) runSubscriber(ctx context.Context, conn *pgx.Conn, channel string, out chan []byte) {
	defer close(out)
	defer func() {
		if conn != nil {
			// Detached context so teardown is not aborted by a cancelled subscribe ctx.
			_ = conn.Close(context.Background())
		}
	}()

	listenSQL := "LISTEN " + pgx.Identifier{channel}.Sanitize()
	backoff := eventBusReconnectMinBackoff

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// Subscribe cancelled — normal shutdown.
				return
			}
			// Connection dropped or other transport error: reconnect + re-LISTEN.
			_ = conn.Close(context.Background())
			conn = nil

			for {
				if err := sleepContext(ctx, backoff); err != nil {
					return
				}
				next, openErr := b.openListenConn(ctx)
				if openErr != nil {
					if ctx.Err() != nil {
						return
					}
					if backoff < eventBusReconnectMaxBackoff {
						backoff *= 2
						if backoff > eventBusReconnectMaxBackoff {
							backoff = eventBusReconnectMaxBackoff
						}
					}
					continue
				}
				if _, listenErr := next.Exec(ctx, listenSQL); listenErr != nil {
					_ = next.Close(context.Background())
					if ctx.Err() != nil {
						return
					}
					if backoff < eventBusReconnectMaxBackoff {
						backoff *= 2
						if backoff > eventBusReconnectMaxBackoff {
							backoff = eventBusReconnectMaxBackoff
						}
					}
					continue
				}
				conn = next
				backoff = eventBusReconnectMinBackoff
				break
			}
			continue
		}

		// Defense-in-depth: ignore unexpected channels on this connection.
		if notification.Channel != channel {
			continue
		}
		// Copy the payload so callers own the slice independently of pgx buffers.
		note := []byte(notification.Payload)
		select {
		case out <- note:
		default:
			// Drop-new under backpressure: never block the LISTEN loop.
		case <-ctx.Done():
			return
		}
	}
}

// TopicChannel maps a logical EventBus topic to a Postgres NOTIFY channel name.
//
// Mapping rules (stable, pure function of topic):
//
//  1. Reject empty / whitespace-only topics.
//  2. Lowercase the topic. Replace each run of characters outside [a-z0-9]
//     with a single underscore; trim leading/trailing underscores from the
//     body. Prefix with "mb_" (mosaic bus).
//     Examples: "cop.updated" → "mb_cop_updated", "advisory.updated" →
//     "mb_advisory_updated".
//  3. If the sanitized body is empty, or the full name exceeds 63 bytes
//     (Postgres NAMEDATALEN-1), use a stable hash form instead:
//     "mb_h_" + first 32 hex chars of SHA-256(original trimmed topic).
//
// The mapping is intentionally lossy for illegal characters. The hash path is
// collision-resistant (SHA-256 prefix) for over-long / empty-body topics.
//
// COLLISION NOTE: two different short topics may map to the same sanitized
// channel when they differ only by punctuation that collapses identically
// (e.g. "a.b" and "a_b" both become "mb_a_b"; "cop.updated" vs "cop_updated").
// Prefer dotted topic names from a controlled vocabulary (eventlog topic
// constants) and never mint free-form topics that could collide after sanitize.
func TopicChannel(topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", fmt.Errorf("%w: topic is required", ErrInvalidRecord)
	}

	var body strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(topic) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			body.WriteRune(r)
			prevUnderscore = false
			continue
		}
		// Collapse any separator run (., -, space, punctuation, …) into one
		// underscore. Leading separators are skipped so the body never starts
		// with '_'.
		if !prevUnderscore && body.Len() > 0 {
			body.WriteByte('_')
			prevUnderscore = true
		}
	}
	// Trim trailing underscore introduced by a trailing separator.
	sanitized := strings.TrimRight(body.String(), "_")
	if sanitized == "" {
		return hashTopicChannel(topic), nil
	}
	name := channelPrefix + sanitized
	if len(name) > postgresChannelNameMax {
		return hashTopicChannel(topic), nil
	}
	return name, nil
}

func hashTopicChannel(topic string) string {
	sum := sha256.Sum256([]byte(topic))
	// 32 hex chars + "mb_h_" (5) = 37 ≤ 63.
	return hashChannelPrefix + hex.EncodeToString(sum[:16])
}
