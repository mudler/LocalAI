package contextfiles

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Limits of what a turn carries. They keep the prompt small and stable.
const (
	// MaxIndexLines is the most lines of one index.
	MaxIndexLines = 40
	// MaxIndexBytes is the most bytes of one index, and the default cap of a Source.
	MaxIndexBytes = 2 << 10
	// MaxTotalBytes is the most bytes of all the indexes of one turn together.
	MaxTotalBytes = 6 << 10
	// MaxProfileBytes is the most bytes of an agent profile.
	MaxProfileBytes = 4 << 10
	// MaxDetailBytes is the most bytes one context_read call returns.
	MaxDetailBytes = 8 << 10
	// MaxSources is the most sources a registry holds.
	MaxSources = 8
	// maxPathLen bounds the path of a source.
	maxPathLen = 200
)

// Scope says which part of a chat a source belongs to.
type Scope string

// The scopes. A user source applies to every chat of the user. An agent source
// applies to the chats of one saved agent. A channel source applies to the
// chats that one external channel (a binding of a messaging app, say) started.
const (
	ScopeUser    Scope = "user"
	ScopeAgent   Scope = "agent"
	ScopeChannel Scope = "channel"
)

// scopeOrder is the order of the blocks of a turn: the widest scope first.
var scopeOrder = []Scope{ScopeUser, ScopeAgent, ScopeChannel}

// ErrInvalidSource wraps the reason a registry refuses a source.
var ErrInvalidSource = errors.New("contextfiles: invalid source")

// Source declares one index file.
type Source struct {
	// ID names the source. It is unique in a registry.
	ID string
	// Scope says which chats the source applies to.
	Scope Scope
	// Path is the index file, relative to the user's home. An agent source
	// holds {agent} and a channel source holds {channel}; the host fills them
	// from the chat, never from the model. The file ends in .md.
	Path string
	// Cap is the most bytes of the index the prompt carries. Zero means
	// MaxIndexBytes; a larger value is refused.
	Cap int
}

// limit returns the byte cap of the source.
func (s Source) limit() int {
	if s.Cap == 0 {
		return MaxIndexBytes
	}
	return s.Cap
}

var placeholder = regexp.MustCompile(`\{[^{}]*\}`)

// Validate reports why a registry would refuse the source. A caller that builds
// sources for one turn, instead of registering them, checks them with it.
func (s Source) Validate() error { return s.validate() }

func (s Source) validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSource, fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(s.ID) == "" {
		return bad("an ID is required")
	}
	if !slices.Contains(scopeOrder, s.Scope) {
		return bad("unknown scope %q", s.Scope)
	}
	if s.Cap < 0 || s.Cap > MaxIndexBytes {
		return bad("the cap must be between 0 and %d bytes", MaxIndexBytes)
	}
	p := s.Path
	if p == "" || len(p) > maxPathLen || strings.HasPrefix(p, "/") || !strings.HasSuffix(p, ".md") {
		return bad("the path must be a relative .md path of at most %d bytes", maxPathLen)
	}
	want := map[Scope]string{ScopeUser: "", ScopeAgent: "{agent}", ScopeChannel: "{channel}"}[s.Scope]
	found := placeholder.FindAllString(p, -1)
	switch {
	case want == "" && len(found) > 0:
		return bad("a user path has no placeholder")
	case want != "" && (len(found) != 1 || found[0] != want):
		return bad("a %s path holds %s once and no other placeholder", s.Scope, want)
	}
	rest := placeholder.ReplaceAllString(p, "")
	if strings.ContainsAny(rest, `{}\`) || strings.IndexFunc(rest, unicode.IsControl) >= 0 {
		return bad("the path holds a character that is not allowed")
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return bad("the path holds an empty or parent segment")
		}
	}
	return nil
}

// Registry holds the sources of the host. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	sources []Source
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a source. The ID must be new and the registry not full.
func (r *Registry) Register(s Source) error {
	if err := s.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.ContainsFunc(r.sources, func(o Source) bool { return o.ID == s.ID }) {
		return fmt.Errorf("%w: %q is registered", ErrInvalidSource, s.ID)
	}
	if len(r.sources) >= MaxSources {
		return fmt.Errorf("%w: at most %d sources", ErrInvalidSource, MaxSources)
	}
	r.sources = append(r.sources, s)
	return nil
}

// Remove drops the source with the ID. An unknown ID is not an error.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources = slices.DeleteFunc(r.sources, func(s Source) bool { return s.ID == id })
}

// Sources returns a copy of the sources, widest scope first and in order of
// registration inside a scope, so the prompt does not depend on map order.
func (r *Registry) Sources() []Source {
	r.mu.RLock()
	out := slices.Clone(r.sources)
	r.mu.RUnlock()
	sortSources(out)
	return out
}

func sortSources(s []Source) {
	slices.SortStableFunc(s, func(a, b Source) int {
		return slices.Index(scopeOrder, a.Scope) - slices.Index(scopeOrder, b.Scope)
	})
}
