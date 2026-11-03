package proposals

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrBadTarget means a proposal names a place its kind does not allow.
var ErrBadTarget = errors.New("proposals: this target is not allowed")

// Aim is where a proposal wants to write, as the model and the chat gave it.
type Aim struct {
	Scope  contract.ChangeScope
	Agent  string // the saved agent of the chat, "" for a plain chat
	Target string // the name the model gave
	Source contract.ChangeSource
}

// Kind says how one kind of change maps to a file in the home of the owner
// and how big it may be.
type Kind struct {
	Name contract.ChangeKind
	// Sensitive kinds are never confirmed from a chat that is not Studio; the
	// gate and the confirm lanes read it.
	Sensitive bool
	// MaxBytes bounds the content; zero means contract.MaxChangeBytes.
	MaxBytes int
	// Path returns the file inside the home that the change writes.
	Path func(Aim) (string, error)
}

// Limit is the content bound of the kind.
func (k Kind) Limit() int {
	if k.MaxBytes <= 0 || k.MaxBytes > contract.MaxChangeBytes {
		return contract.MaxChangeBytes
	}
	return k.MaxBytes
}

// Kinds is the set of kinds a host offers. Later features register theirs.
type Kinds struct {
	mu sync.RWMutex
	m  map[contract.ChangeKind]Kind
}

// NewKinds returns the kinds this package defines: memory and profile.
func NewKinds() *Kinds {
	k := &Kinds{m: map[contract.ChangeKind]Kind{}}
	for _, d := range []Kind{memoryKind, profileKind} {
		k.m[d.Name] = d
	}
	return k
}

// Register adds a kind. It refuses a name the contract does not know, a kind
// without Path and a kind that is registered already.
func (k *Kinds) Register(d Kind) error {
	switch {
	case !contract.ValidChangeKind(string(d.Name)):
		return fmt.Errorf("proposals: %q is not a change kind", d.Name)
	case d.Path == nil:
		return fmt.Errorf("proposals: %s has no Path", d.Name)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.m[d.Name]; ok {
		return fmt.Errorf("proposals: %s is registered already", d.Name)
	}
	k.m[d.Name] = d
	return nil
}

// Lookup returns the kind of a name.
func (k *Kinds) Lookup(name string) (Kind, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	d, ok := k.m[contract.ChangeKind(name)]
	return d, ok
}

// Names lists the registered kinds in contract order.
func (k *Kinds) Names() []contract.ChangeKind {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var out []contract.ChangeKind
	for _, n := range contract.ChangeKinds() {
		if _, ok := k.m[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

var (
	topicFile = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}\.md$`)
	idSafe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// ChannelDir names the memory folder of one connector conversation: the
// binding and a short hash of the host chat. It uses no platform name.
func ChannelDir(s contract.ChangeSource) string {
	sum := sha256.Sum256([]byte(s.Chat))
	return s.ID + "-" + hex.EncodeToString(sum[:4])
}

var memoryKind = Kind{
	Name: contract.ChangeMemory,
	Path: func(a Aim) (string, error) {
		if !topicFile.MatchString(a.Target) {
			return "", fmt.Errorf("%w: a memory target is a file name such as index.md or food.md", ErrBadTarget)
		}
		switch a.Scope {
		case contract.ScopeUser:
			return "Memory/" + a.Target, nil
		case contract.ScopeAgent:
			if !idSafe.MatchString(a.Agent) {
				return "", fmt.Errorf("%w: agent memory needs a chat that runs a saved agent", ErrBadTarget)
			}
			return "Memory/agents/" + a.Agent + "/" + a.Target, nil
		case contract.ScopeChannel:
			if !idSafe.MatchString(a.Source.ID) || a.Source.Chat == "" {
				return "", fmt.Errorf("%w: channel memory needs a chat that came from a connector", ErrBadTarget)
			}
			return "Memory/channels/" + ChannelDir(a.Source) + "/" + a.Target, nil
		}
		return "", fmt.Errorf("%w: unknown scope %q", ErrBadTarget, a.Scope)
	},
}

var profileKind = Kind{
	Name:      contract.ChangeProfile,
	Sensitive: true,
	MaxBytes:  contract.MaxProfileBytes,
	Path: func(a Aim) (string, error) {
		if a.Scope != contract.ScopeAgent || !idSafe.MatchString(a.Agent) {
			return "", fmt.Errorf("%w: a profile belongs to a saved agent: use scope agent in a chat that runs one", ErrBadTarget)
		}
		return "LocalAI/agents/" + a.Agent + "/profile.md", nil
	},
}
