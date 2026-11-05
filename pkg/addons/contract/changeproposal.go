// SPDX-License-Identifier: MIT
package contract

import (
	"slices"
	"time"
)

// A change proposal is how a model asks to change the person's own state: a
// memory note, a profile, a skill. The host stores it, shows it to the owner
// and writes it only after the owner confirms. Every surface that can run a
// model uses the same record: a Studio chat, a connector chat of any platform
// and an add-on page. An add-on never needs platform code for it. The model
// calls the host tool propose_change, and a connector carries the text of the
// tool result like any other answer.
//
// The record is versioned. A reader must ignore fields it does not know.

// ChangeProposalVersion is the version of the ChangeProposal wire record.
const ChangeProposalVersion = 1

// ChangeKind is what a proposal changes.
type ChangeKind string

// The kinds of change. The host decides which ones it offers.
const (
	ChangeMemory    ChangeKind = "memory"
	ChangeProfile   ChangeKind = "profile"
	ChangeSkill     ChangeKind = "skill"
	ChangePage      ChangeKind = "page"
	ChangeDashboard ChangeKind = "dashboard"
	// ChangeKnowledge adds a text to a collection of the person's Knowledge.
	// It is never applied unasked.
	ChangeKnowledge ChangeKind = "knowledge"
)

// ChangeKinds lists every kind in a stable order.
func ChangeKinds() []ChangeKind {
	return []ChangeKind{ChangeMemory, ChangeProfile, ChangeSkill, ChangePage, ChangeDashboard, ChangeKnowledge}
}

// ValidChangeKind reports whether s names a kind.
func ValidChangeKind(s string) bool { return slices.Contains(ChangeKinds(), ChangeKind(s)) }

// ChangeScope says whose state a proposal changes.
type ChangeScope string

// The scopes. user: the person, in every chat. agent: one saved agent.
// channel: one connector conversation.
const (
	ScopeUser    ChangeScope = "user"
	ScopeAgent   ChangeScope = "agent"
	ScopeChannel ChangeScope = "channel"
)

// ValidChangeScope reports whether s names a scope.
func ValidChangeScope(s string) bool {
	switch ChangeScope(s) {
	case ScopeUser, ScopeAgent, ScopeChannel:
		return true
	}
	return false
}

// ProposalStatus is where a proposal is in its life.
type ProposalStatus string

// The statuses. Only pending can change. Applied can become reverted.
const (
	ProposalPending   ProposalStatus = "pending"
	ProposalApplied   ProposalStatus = "applied"
	ProposalDismissed ProposalStatus = "dismissed"
	ProposalExpired   ProposalStatus = "expired"
	ProposalReverted  ProposalStatus = "reverted"
	// ProposalStale: the target changed after the proposal, so it was refused.
	ProposalStale ProposalStatus = "stale"
)

// Settled reports whether the proposal has an answer.
func (s ProposalStatus) Settled() bool { return s != ProposalPending }

// The codes of ChangeProposal.Warning.
const (
	// WarningSecret: the text looks like it holds a secret, such as a private
	// key, an access token or a password.
	WarningSecret = "secret"
)

// ChangeSource says where a proposal came from, without naming a platform.
// Kind is the origin of the chat as the host names it ("" for a chat a person
// watches, "connector" for a messaging app). ID names the binding of a
// connector chat. Chat is the host chat. Thread is for a connector that has
// threads; the host fills it when it knows it.
type ChangeSource struct {
	Kind   string `json:"kind,omitempty"`
	ID     string `json:"id,omitempty"`
	Chat   string `json:"chat,omitempty"`
	Thread string `json:"thread,omitempty"`
	// Sender is the platform ID of the sender whose message made a connector
	// chat propose this change. The host keeps it so that, in a shared chat,
	// only that sender can answer the proposal there.
	Sender string `json:"sender,omitempty"`
}

// ChangeProposal is one proposed change. Content is the full new text of the
// target. BaseHash is the SHA-256 (hex) of the target when the model proposed,
// or empty when the target did not exist; a proposal is refused if the target
// has another hash when the owner confirms.
type ChangeProposal struct {
	V        int            `json:"v"`
	ID       string         `json:"id"`
	Owner    string         `json:"owner,omitempty"`
	Agent    string         `json:"agent,omitempty"`
	Kind     ChangeKind     `json:"kind"`
	Scope    ChangeScope    `json:"scope"`
	Target   string         `json:"target"`
	Summary  string         `json:"summary,omitempty"`
	Content  string         `json:"content,omitempty"`
	BaseHash string         `json:"base_hash,omitempty"`
	Source   ChangeSource   `json:"source,omitzero"`
	Status   ProposalStatus `json:"status"`
	// Warning is set by the host when the text of a proposal looks risky, as a
	// stable code (see the Warning constants). The owner still sees the text,
	// with a warning, and can confirm it in the web interface; a messaging chat
	// cannot confirm it. The host works it out when it reads the proposal and
	// does not keep it.
	Warning string `json:"warning,omitempty"`
	// Auto is true when the host applied the change at once because the person
	// chose so (the memory save setting), not after a confirmation. The change
	// set is the same and can be undone the same way.
	Auto bool `json:"auto,omitempty"`
	// Rev is the workspace change set that applying wrote; RevertRev the one
	// that reverting wrote. Zero until then.
	Rev       int64     `json:"rev,omitempty"`
	RevertRev int64     `json:"revert_rev,omitempty"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	Answered  time.Time `json:"answered,omitzero"`
}

// The limits of the host. They are decisions of the owner of the product, not
// settings of an add-on.
const (
	// ChangeExpiry is how long a proposal waits for an answer.
	ChangeExpiry = 24 * time.Hour
	// MaxPendingChanges is the most proposals of one person waiting at once.
	MaxPendingChanges = 10
	// MaxChangesPerHour is the most proposals one agent (or one plain chat
	// user) makes in an hour.
	MaxChangesPerHour = 5
	// MaxChangeBytes is the largest content of a proposal.
	MaxChangeBytes = 16 << 10
	// MaxPageChangeBytes is the largest content of a page proposal: all the
	// files of the page together.
	MaxPageChangeBytes = 256 << 10
	// MaxKnowledgeBytes is the largest text of a knowledge proposal. The host
	// cuts it into passages and embeds each one, so the bound is also a bound
	// on compute.
	MaxKnowledgeBytes = 64 << 10
	// MaxProfileBytes is the largest agent profile.
	MaxProfileBytes = 4 << 10
)
