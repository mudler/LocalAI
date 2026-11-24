package studio

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

const (
	// hookSkew is how far the signed timestamp of a call may be from now.
	hookSkew = 5 * time.Minute
	// hookLimit is how many authenticated calls one trigger takes in a window.
	hookLimit = 60
	// hookWindow is the length of the fixed window of that limit.
	hookWindow = time.Minute
	// hookMaxKeys bounds the memory of the limiter.
	hookMaxKeys = 4096
	// hookMaxKeyLen is the longest delivery id used as it is; a longer one is hashed.
	hookMaxKeyLen = 200
)

// hookLimiter counts the calls that authenticated, for each trigger, in a fixed
// window that starts at the first call. It lives in memory, so each replica
// counts its own calls.
type hookLimiter struct {
	mu    sync.Mutex
	slots map[string]hookSlot
}

type hookSlot struct {
	start time.Time
	n     int
}

// allow counts one call of the trigger and tells whether it is within the limit.
func (l *hookLimiter) allow(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.slots == nil {
		l.slots = map[string]hookSlot{}
	}
	s, ok := l.slots[id]
	if !ok || now.Sub(s.start) >= hookWindow || now.Before(s.start) {
		if !ok && len(l.slots) >= hookMaxKeys {
			for k, v := range l.slots {
				if now.Sub(v.start) >= hookWindow {
					delete(l.slots, k)
				}
			}
			if len(l.slots) >= hookMaxKeys {
				return false
			}
		}
		s = hookSlot{start: now}
	}
	s.n++
	l.slots[id] = s
	return s.n <= hookLimit
}

// hookNotFound is the one answer to every call the hook refuses before it
// counts the call: an unknown id, a trigger that is off or of another kind, a
// wrong secret, a bad signature and a stale timestamp. It tells nothing about
// which of them it was.
func hookNotFound(c echo.Context, why string) error {
	xlog.Debug("trigger hook refused", "reason", why)
	return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
}

// hookScheme is the way a call authenticated.
type hookScheme int

const (
	hookNone hookScheme = iota
	hookSigned
	hookGitHub
	hookBearer
)

// hookAuthorized checks the call against the secret of the trigger. The scheme
// is chosen by the headers present, first match wins: a signed call with a
// timestamp, a GitHub signature, a bearer secret. A trigger with no secret
// never authenticates.
func hookAuthorized(secret string, h http.Header, body []byte, now time.Time) (hookScheme, bool) {
	if secret == "" {
		return hookNone, false
	}
	if sig := h.Get("X-Webhook-Signature-V2"); sig != "" {
		secs, err := strconv.ParseInt(strings.TrimSpace(h.Get("X-Webhook-Timestamp")), 10, 64)
		if err != nil {
			return hookNone, false
		}
		return hookSigned, triggers.Verify(secret, sig, time.Unix(secs, 0), body, now, hookSkew)
	}
	if sig := h.Get("X-Hub-Signature-256"); sig != "" {
		return hookGitHub, triggers.VerifyGitHub(secret, sig, body)
	}
	if got, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer "); ok {
		a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(secret))
		return hookBearer, subtle.ConstantTimeCompare(a[:], b[:]) == 1
	}
	return hookNone, false
}

// hookEvent builds the event of a call that authenticated.
func hookEvent(c echo.Context, scheme hookScheme, body []byte, now time.Time) triggers.Event {
	h := c.Request().Header
	ev := triggers.Event{Payload: body}
	gh := h.Get("X-GitHub-Event")
	switch {
	case gh != "":
		ev.Source = "github"
	case c.QueryParam("source") != "":
		ev.Source = c.QueryParam("source")
	default:
		ev.Source = "webhook"
	}
	switch {
	case gh != "":
		ev.Type = gh
	case h.Get("X-Webhook-Event") != "":
		ev.Type = h.Get("X-Webhook-Event")
	default:
		var doc struct {
			Type any `json:"type"`
		}
		if json.Unmarshal(body, &doc) == nil {
			if s, ok := doc.Type.(string); ok && s != "" {
				ev.Type = s
			}
		}
		if ev.Type == "" {
			ev.Type = "event"
		}
	}
	key := h.Get("X-GitHub-Delivery")
	if scheme == hookSigned {
		// Only the signature covers the timestamp and the body. A delivery id
		// or the minute of the call is not signed, so a replay of a captured
		// call would get a new key from either. The signature does not change.
		sum := sha256.Sum256([]byte(h.Get("X-Webhook-Signature-V2")))
		key = "v2:" + hex.EncodeToString(sum[:])
	} else if key == "" {
		key = h.Get("X-Webhook-Id")
	}
	if key == "" {
		sum := sha256.Sum256(body)
		key = hex.EncodeToString(sum[:]) + "@" + strconv.FormatInt(now.Unix()/60, 10)
	} else if len(key) > hookMaxKeyLen {
		sum := sha256.Sum256([]byte(key))
		key = hex.EncodeToString(sum[:])
	}
	ev.DedupeKey = key
	return ev
}

// TriggerHook takes an event for an event trigger.
//
// It is public: the caller is a service, and the secret of the trigger is the
// credential, as a signature or as a bearer value. Every refusal before the
// call is counted is the same 404.
// @Summary Send an event to an event trigger.
// @Description Starts a run of an event trigger. Sign the body with the secret of the trigger (X-Webhook-Signature-V2 with X-Webhook-Timestamp, or X-Hub-Signature-256), or send the secret as a bearer value. An event that the trigger does not match answers 202 with no run.
// @Tags chats
// @Param id path string true "The trigger id"
// @Success 202 {object} map[string]string "The run, or an empty run id when the trigger does not match the event"
// @Failure 404 {object} map[string]string "Refused, whatever the reason"
// @Failure 413 {object} map[string]string "The body is over 256 KiB"
// @Failure 429 {object} map[string]string "More than 60 events in a minute"
// @Router /api/chats/triggers/{id}/hook [post]
func (h *Handlers) TriggerHook(c echo.Context) error {
	// The body is read first, up to the cap, so a 413 does not tell which ids
	// exist. The signature needs the whole body anyway.
	ctx := c.Request().Context()
	body, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, triggers.MaxEventBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
		}
		return hookNotFound(c, "unreadable body")
	}
	host := h.svc.Triggers()
	if host == nil {
		return hookNotFound(c, "triggers off")
	}
	t, err := host.EventTrigger(ctx, c.Param("id"))
	if err != nil {
		return hookNotFound(c, "unknown trigger")
	}
	if t.Kind != triggers.KindEvent || !t.Enabled || t.Proposed || t.IsChange() {
		return hookNotFound(c, "not an enabled event trigger")
	}
	now := time.Now()
	if h.clock != nil {
		now = h.clock()
	}
	scheme, ok := hookAuthorized(t.Secret, c.Request().Header, body, now)
	if !ok {
		return hookNotFound(c, "not authenticated")
	}
	if !h.hooks.allow(t.ID, now) {
		c.Response().Header().Set("Retry-After", "60")
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "too many events"})
	}
	run, err := host.PublishRun(ctx, t.ID, hookEvent(c, scheme, body, now))
	switch {
	case err == nil:
		return c.JSON(http.StatusAccepted, map[string]string{"run": run.ID})
	case errors.Is(err, triggers.ErrNoMatch):
		return c.JSON(http.StatusAccepted, map[string]string{"run": ""})
	case errors.Is(err, triggers.ErrNotFound), errors.Is(err, triggers.ErrNotEnabled):
		return hookNotFound(c, "trigger changed")
	default:
		xlog.Error("trigger hook failed", "trigger", t.ID, "error", err)
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
	}
}
