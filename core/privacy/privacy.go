// Package privacy carries the incognito level of a request.
//
// A conversation of the web UI can be incognito at one of two levels:
//
//	memory-off      the conversation does not read or write long-term memory
//	zero-retention  memory-off, and no part of the content is persisted;
//	                only usage counters are kept
//
// The level lives in the request context (WithLevel, FromContext). Every
// place that persists something asks Allows with the class of what it would
// keep, so a call site reads as a policy check. A new persistence point must
// call Allows; the incognito proof test in tests/e2e guards that rule.
package privacy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Level is how private a request is. The order matters: a higher level
// includes every promise of the lower ones.
type Level int

const (
	// Normal is a request with no incognito promise.
	Normal Level = iota
	// MemoryOff keeps the conversation away from long-term memory.
	MemoryOff
	// ZeroRetention is MemoryOff plus no persisted content at all.
	ZeroRetention
)

// The names of the levels, as the marker header and the settings write them.
const (
	NameNormal        = "normal"
	NameMemoryOff     = "memory-off"
	NameZeroRetention = "zero-retention"
)

// Header is the internal marker the web UI sends on the requests of an
// incognito conversation. It is not a public API: the server honours it only
// for a web UI session (see Decide).
const Header = "X-LocalAI-Incognito"

// String returns the name of the level.
func (l Level) String() string {
	switch l {
	case MemoryOff:
		return NameMemoryOff
	case ZeroRetention:
		return NameZeroRetention
	default:
		return NameNormal
	}
}

// Incognito reports whether the level makes any promise.
func (l Level) Incognito() bool { return l > Normal }

// ErrInvalidLevel means a value names no level.
var ErrInvalidLevel = errors.New("privacy: unknown incognito level")

// ParseLevel reads a level name. Empty is Normal.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", NameNormal:
		return Normal, nil
	case NameMemoryOff:
		return MemoryOff, nil
	case NameZeroRetention:
		return ZeroRetention, nil
	}
	return Normal, fmt.Errorf("%w %q: use %s or %s", ErrInvalidLevel, s, NameMemoryOff, NameZeroRetention)
}

type ctxKey struct{}

// WithLevel returns ctx carrying level. It never lowers a level ctx already
// carries: a request that is incognito stays incognito in everything it
// starts, whatever a later step asks for.
func WithLevel(ctx context.Context, level Level) context.Context {
	if cur := FromContext(ctx); cur >= level {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, level)
}

// FromContext returns the level ctx carries, Normal when none.
func FromContext(ctx context.Context) Level {
	if ctx == nil {
		return Normal
	}
	l, _ := ctx.Value(ctxKey{}).(Level)
	return l
}

// Store is a class of thing a persistence point keeps.
type Store int

const (
	// StoreMemory is long-term memory: notes, the knowledge an agent saves,
	// anything a later conversation reads back.
	StoreMemory Store = iota
	// StoreConversation is the transcript of a conversation and the records
	// derived from it (search index, responses kept for a later turn).
	StoreConversation
	// StoreTrace is a trace or an activity record with request or response
	// bodies.
	StoreTrace
	// StoreContent is any other copy of content: files, generated media,
	// prompt caches, logs of bodies, prompt text in routing records.
	StoreContent
	// StoreCounters is usage: user, model, token counts, time. It is always
	// allowed, so quotas and usage reports keep working.
	StoreCounters
)

// Allows reports whether a persistence point may keep a thing of class st
// for the request of ctx.
func Allows(ctx context.Context, st Store) bool {
	return AllowsLevel(FromContext(ctx), st)
}

// AllowsLevel is Allows for a level known without a context.
func AllowsLevel(l Level, st Store) bool {
	switch st {
	case StoreCounters:
		return true
	case StoreMemory:
		return l == Normal
	default:
		return l < ZeroRetention
	}
}

// Policy is the server-wide incognito setting.
type Policy string

const (
	// PolicyAllow lets web UI sessions start incognito conversations. It is
	// the default.
	PolicyAllow Policy = "allow"
	// PolicyForbid refuses the marker with 403, so the UI never shows an
	// incognito state the server does not honour.
	PolicyForbid Policy = "forbid"
)

// ParsePolicy reads a policy. Empty is PolicyAllow.
func ParsePolicy(s string) (Policy, error) {
	switch Policy(strings.ToLower(strings.TrimSpace(s))) {
	case "", PolicyAllow:
		return PolicyAllow, nil
	case PolicyForbid:
		return PolicyForbid, nil
	}
	return PolicyAllow, fmt.Errorf("privacy: unknown incognito policy %q: use allow or forbid", s)
}

// ParseMaxLevel reads the highest level the server honours. Empty is
// ZeroRetention; Normal is not a valid maximum.
func ParseMaxLevel(s string) (Level, error) {
	if strings.TrimSpace(s) == "" {
		return ZeroRetention, nil
	}
	l, err := ParseLevel(s)
	if err != nil {
		return ZeroRetention, err
	}
	if l == Normal {
		return ZeroRetention, fmt.Errorf("%w %q as a maximum: use %s or %s", ErrInvalidLevel, s, NameMemoryOff, NameZeroRetention)
	}
	return l, nil
}

// Errors Decide returns. Each one is an answer the client must see, never a
// silent downgrade.
var (
	// ErrForbidden: the policy is forbid and the request carries the marker.
	ErrForbidden = errors.New("incognito mode is turned off on this server")
	// ErrAboveMax: the marker asks for more than incognito_max_level.
	ErrAboveMax = errors.New("this server does not offer that incognito level")
)

// Request is what Decide looks at.
type Request struct {
	// Marker is the value of the Header, empty when absent.
	Marker string
	// WebSession is true for a request of the web UI: a session cookie, or a
	// server with no authentication at all. A bearer or header API key is not.
	WebSession bool
	Policy     Policy
	// MaxLevel is the highest level the server honours (ZeroRetention when
	// zero).
	MaxLevel Level
}

// Decide returns the level of a request. A request that is not a web session
// is Normal whatever it sends: the marker is not a public API. A web session
// with the marker gets its level, or an error when the policy forbids it, the
// level is above the maximum, or the value names no level.
func Decide(r Request) (Level, error) {
	if r.Marker == "" || !r.WebSession {
		return Normal, nil
	}
	l, err := ParseLevel(r.Marker)
	if err != nil {
		return Normal, err
	}
	if l == Normal {
		return Normal, nil
	}
	if r.Policy == PolicyForbid {
		return Normal, ErrForbidden
	}
	maxLevel := r.MaxLevel
	if maxLevel == Normal {
		maxLevel = ZeroRetention
	}
	if l > maxLevel {
		return Normal, fmt.Errorf("%w: %s is above the maximum %s", ErrAboveMax, l, maxLevel)
	}
	return l, nil
}

// Discard removes the files a handler wrote for its answer when the request
// may not keep content: a generated file that was already sent to the client
// must not stay in the generated-content directory. Empty paths and missing
// files are skipped. A normal request keeps its files.
func Discard(ctx context.Context, paths ...string) {
	if Allows(ctx, StoreContent) {
		return
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		_ = os.Remove(p)
	}
}

// Redacted is what Redact writes in place of content.
const Redacted = "[redacted: zero-retention]"

// Redact returns s, or Redacted when the request may not keep content. Use it
// for every log line that would print a prompt, a message or an answer, at
// any level: logs are kept by whatever collects them.
func Redact(ctx context.Context, s string) string {
	if Allows(ctx, StoreContent) {
		return s
	}
	return Redacted
}
