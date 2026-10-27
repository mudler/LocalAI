// Package agents keeps the saved agents of the chats: a name, instructions and
// the lists of things the agent may use. A definition grants nothing by
// itself; the chat that runs it gets the intersection of these lists and what
// its owner may use at that moment.
//
// Two stores exist: NewJSON (a file under a data directory, or memory when the
// directory is empty) and NewGORM (SQLite or PostgreSQL). agentstest.Run checks
// both.
package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Errors. An agent that does not exist, or belongs to another principal, is
// ErrNotFound for every method.
var (
	ErrNotFound  = errors.New("agents: not found")
	ErrNameTaken = errors.New("agents: the user already has an agent with this name")
	// ErrInvalid wraps the reason a definition is refused.
	ErrInvalid = errors.New("agents: invalid agent")
)

// Limits of a definition.
const (
	MaxNameRunes    = 80
	MaxDescription  = 400
	MaxInstructions = 32 << 10 // bytes
	MaxListEntries  = 100
	MaxEntryRunes   = 200
	MaxModelRunes   = 200
)

// Agent is a saved agent. The four lists are exact grants: an empty list
// grants nothing.
type Agent struct {
	ID, Principal string
	Name          string
	Description   string
	Instructions  string
	// Model is the model a chat with this agent talks to. Empty: the model the
	// chat is created with.
	Model       string
	MCPServers  []string // names of servers in the model's MCP configuration
	Addons      []string // add-on IDs
	Collections []string // collection names
	Skills      []string // skill names
	Created     time.Time
	Updated     time.Time
}

// Store is the storage of saved agents.
type Store interface {
	// Put inserts the agent or replaces the one with the same ID and principal.
	// An ID that belongs to another principal is ErrNotFound. Created is kept
	// when the agent exists; Updated is set by the caller.
	Put(ctx context.Context, a Agent) error
	Get(ctx context.Context, principal, id string) (Agent, error)
	// List returns the agents of the principal sorted by name, case-insensitively.
	List(ctx context.Context, principal string) ([]Agent, error)
	Delete(ctx context.Context, principal, id string) error
	// ListAll returns the agents of every principal sorted by principal, then
	// name. It is for the administrator's view and for migrations.
	ListAll(ctx context.Context) ([]Agent, error)
}

var addonIDShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// NameKey is the form of a name that must be unique per user.
func NameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Validate checks the limits of a definition. The error wraps ErrInvalid.
func Validate(a Agent) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(a.Name)); n == 0 || n > MaxNameRunes {
		return bad("the name must have 1 to %d characters", MaxNameRunes)
	}
	if utf8.RuneCountInString(a.Description) > MaxDescription {
		return bad("the description is longer than %d characters", MaxDescription)
	}
	if len(a.Instructions) > MaxInstructions {
		return bad("the instructions are longer than %d bytes", MaxInstructions)
	}
	if utf8.RuneCountInString(a.Model) > MaxModelRunes {
		return bad("the model name is longer than %d characters", MaxModelRunes)
	}
	for _, l := range []struct {
		name    string
		entries []string
		addon   bool
	}{{"MCP servers", a.MCPServers, false}, {"add-ons", a.Addons, true}, {"collections", a.Collections, false}, {"skills", a.Skills, false}} {
		if len(l.entries) > MaxListEntries {
			return bad("%s has more than %d entries", l.name, MaxListEntries)
		}
		for _, e := range l.entries {
			switch {
			case l.addon && !addonIDShape.MatchString(e):
				return bad("%q is not an add-on ID", e)
			case strings.TrimSpace(e) == "":
				return bad("%s has an empty entry", l.name)
			case utf8.RuneCountInString(e) > MaxEntryRunes:
				return bad("an entry of %s is longer than %d characters", l.name, MaxEntryRunes)
			}
		}
	}
	return nil
}

// clone copies the lists so a caller cannot change a stored agent.
func (a Agent) clone() Agent {
	a.MCPServers = append([]string(nil), a.MCPServers...)
	a.Addons = append([]string(nil), a.Addons...)
	a.Collections = append([]string(nil), a.Collections...)
	a.Skills = append([]string(nil), a.Skills...)
	return a
}
