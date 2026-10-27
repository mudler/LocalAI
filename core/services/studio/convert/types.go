// Package convert turns what the old chat, agents, tasks, skills and Telegram
// connectors left on disk and in the database into the definitions of the chats:
// saved agents, triggers, personal and shared skills, chats and connector
// bindings. It reads the old data with its own structs, so it needs none of the
// code that is removed, and it never deletes or changes what it reads.
//
// A run is safe to repeat. Each source item that was converted is marked, and
// is not converted again; one that failed is tried again at the next run, with
// its error in the report. Nothing converted runs, answers or acts by itself:
// triggers and connector bindings are made switched off.
package convert

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Kinds of item in a report.
const (
	KindAgent     = "agent"
	KindTrigger   = "trigger"
	KindSkill     = "skill"
	KindChat      = "chat"
	KindConnector = "connector"
)

// Statuses of an item.
const (
	StatusConverted = "converted"
	StatusSkipped   = "skipped" // already converted, or nothing to do
	StatusFailed    = "failed"  // tried again at the next run
	StatusDropped   = "dropped" // has no counterpart, on purpose
)

// Item is one line of a report. Reason names what was dropped or what went
// wrong. It never holds a secret or the text of a prompt.
type Item struct {
	Kind   string `json:"kind"`
	User   string `json:"-"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Report is the outcome of a run.
type Report struct {
	At    time.Time `json:"at"`
	Items []Item    `json:"items"`
	// Notable lists the items of a run that are news to their user: converted or
	// dropped for the first time, or failed with a reason the report before did
	// not hold. It is what a notification is about, and it is not stored.
	Notable []Item `json:"-"`
}

// Counts returns how many items have each status.
func (r Report) Counts() map[string]int {
	c := map[string]int{}
	for _, i := range r.Items {
		c[i.Status]++
	}
	return c
}

// ForUser returns the items of one user, as a report.
func (r Report) ForUser(user string) Report {
	out := Report{At: r.At}
	for _, i := range r.Items {
		if i.User == user {
			out.Items = append(out.Items, i)
		}
	}
	return out
}

// Users lists the users the report has items for.
func (r Report) Users() []string {
	seen := map[string]bool{}
	var out []string
	for _, i := range r.Items {
		if !seen[i.User] {
			seen[i.User] = true
			out = append(out, i.User)
		}
	}
	return out
}

// Sources says where the old data is.
type Sources struct {
	// StateDir is the state directory of the old agent pool; DataDir the data
	// directory that held the task files (often the same).
	StateDir, DataDir string
	// DB is the auth database that held the tables of distributed mode and the
	// chat history. Nil when the server has none.
	DB *gorm.DB
	// AuthEnabled says whether users have accounts. Without it every old item
	// belongs to the one anonymous user.
	AuthEnabled bool
	// Zone is the IANA name of the zone the old cron tasks ran in, the server's
	// local zone. It is empty when that zone is UTC or cannot be named; then
	// ZoneNote says what the report tells about it. See LocalZone.
	Zone, ZoneNote string
}

// ErrNameTaken means a target already has an agent with the name.
var ErrNameTaken = errors.New("convert: the name is taken")

// ErrInvalid means a target refused an item for good: its name, size or form
// is not valid there. The item is dropped, not tried again.
var ErrInvalid = errors.New("convert: the item is not valid")

// ErrCollectionsUnavailable means the collections cannot be listed now (the
// service is off or has not started). An agent that needs a collection grant is
// then left for the next run.
var ErrCollectionsUnavailable = errors.New("convert: the collections are not available")

// AgentIn is a saved agent to make.
type AgentIn struct {
	Name, Description, Instructions, Model string
	Collections, Skills                    []string
}

// TriggerIn is a schedule trigger to make, switched off.
type TriggerIn struct {
	Name, Prompt, Model, Schedule, AgentID string
	// TZ is the zone of Schedule; empty is UTC.
	TZ string
}

// EntryIn is one line of a transcript to import.
type EntryIn struct {
	Role, Text string
	At         time.Time
}

// ChatIn is a chat to import.
type ChatIn struct {
	Title, Model     string
	Created, Updated time.Time
	Entries          []EntryIn
}

// BindingIn is a Telegram binding to make, switched off.
type BindingIn struct {
	Name, Token, AgentID string
	Allow                []string
}

// Targets is where converted things go. The application implements it over the
// services of the chats; a test implements it with fakes. user is the owner as
// the old data named it: "" is the user of a server with no accounts.
type Targets interface {
	UserExists(ctx context.Context, user string) (bool, error)
	// Collections lists the names of the user's collections. It returns an error
	// wrapping ErrCollectionsUnavailable when the service is off or not started.
	Collections(ctx context.Context, user string) ([]string, error)
	// SaveAgent returns the new ID, or an error wrapping ErrNameTaken.
	SaveAgent(ctx context.Context, user string, in AgentIn) (string, error)
	CreateTrigger(ctx context.Context, user string, in TriggerIn) error
	SkillExists(ctx context.Context, user string, shared bool, name string) (bool, error)
	WriteSkill(ctx context.Context, user string, shared bool, name, description, body string) error
	ImportChat(ctx context.Context, user string, in ChatIn) error
	// DiscardChat deletes the chats of the user with this title and creation
	// time: what an import that a crash cut short left behind.
	DiscardChat(ctx context.Context, user, title string, created time.Time) error
	CreateBinding(ctx context.Context, user string, in BindingIn) error
}

// SkillRec is a skill as a target stores it (used by tests of the targets).
type SkillRec struct{ Name, Description, Body string }

// MaxMessages is how many messages of one old conversation are imported (the newest).
const MaxMessages = 5000

// Marks remembers what was converted and keeps reports.
type Marks interface {
	// Ref returns what a source item became, and whether it was converted.
	Ref(ctx context.Context, key string) (string, bool, error)
	Mark(ctx context.Context, key, ref string) error
	SaveReport(ctx context.Context, r Report) error
	// Report returns the stored report, with every user's items.
	Report(ctx context.Context) (Report, bool, error)
}
