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
}

// ErrNameTaken means a target already has an agent with the name.
var ErrNameTaken = errors.New("convert: the name is taken")

// AgentIn is a saved agent to make.
type AgentIn struct {
	Name, Description, Instructions, Model string
	Collections, Skills                    []string
}

// TriggerIn is a schedule trigger to make, switched off.
type TriggerIn struct {
	Name, Prompt, Model, Schedule, AgentID string
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
	// Collections lists the names of the user's collections.
	Collections(ctx context.Context, user string) ([]string, error)
	// SaveAgent returns the new ID, or an error wrapping ErrNameTaken.
	SaveAgent(ctx context.Context, user string, in AgentIn) (string, error)
	CreateTrigger(ctx context.Context, user string, in TriggerIn) error
	SkillExists(ctx context.Context, user string, shared bool, name string) (bool, error)
	WriteSkill(ctx context.Context, user string, shared bool, name, description, body string) error
	ImportChat(ctx context.Context, user string, in ChatIn) error
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
