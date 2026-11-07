package studio

import (
	"context"
	"regexp"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// How a memory note is saved. The person chooses on the page of an add-on
// that declares the setting (a per-person setting that the host keeps). Every
// memory write is a proposal that the person confirms, and that stays the
// default.
//
//	ask    the model proposes, the person confirms (default)
//	auto   the host applies a note from the person's own chat at once, as a
//	       change set the person can undo, and the chat shows a note with Undo
//	always like ask, and nothing can loosen it: no agent or messaging setting
//	       overrides it, and the model cannot undo or forget by itself either
//	never  the model gets no way to propose a memory note
//
// Strictness runs auto < ask < always < never. The level that applies to a
// write is the strictest among the person's setting, the saved agent's own
// rule (always or never), the messaging row (connector chats) and the rules of
// the chat. effectiveSaveLevel is that one function.
//
// One explicit choice goes the other way: a saved agent whose rule is auto
// saves its notes at once even when the person's level is ask. It loosens the
// person's ask and auto for that agent, and the messaging row ask and
// paired_private (not never). It does not loosen the person's always or never,
// a sub-agent, an unattended run, or a sender that is not paired in a
// one-to-one chat; the limits of an automatic write (autoFits) still hold.
//
// The setting only ever narrows or keeps the confirmation for anything that is
// not the person's own chat. The rules, in one place:
//
//   - A connector chat asks whatever the person's setting says, unless the
//     person chose "Save it for a paired sender in a private chat" in the
//     messaging row and the sender is paired and the chat is one-to-one. The
//     sender of a message is not necessarily the person, so a stranger's
//     message never writes memory unconfirmed. The row cannot loosen always
//     or never.
//   - An unattended run (a trigger or a job) has no propose tool at all.
//   - A saved agent cannot widen the choice of the person: the agent's grants
//     decide whether it may propose, the person's setting decides how the
//     proposal is applied. An agent with its own memory.write grant still asks
//     when the person said ask.
//   - never applies to every origin: it is the person's choice, and refusing is
//     the narrow side.
//   - Only the memory kind reads the setting to decide how it is saved.
//     Profile, skill, page, dashboard and knowledge changes always ask.
//     Never also takes the knowledge kind away; Always confirm changes
//     nothing for it, since it always asks.
//
// The setting belongs to an add-on, not to the host: an add-on declares a user
// setting with the role contract.UserSettingRoleMemorySave in its manifest,
// edits it on its own page, and the host finds it by that role. The host fixes
// the values and the default of the role, so a manifest cannot make the host
// more permissive. If several add-ons declare the role, the strictest answer
// wins (never, then ask, then auto), and an add-on that is not running does not
// count as auto.

// The values of the setting.
const (
	SaveAsk    = "ask"
	SaveAuto   = "auto"
	SaveAlways = "always"
	SaveNever  = "never"
)

// The values of the messaging row: how a note proposed in a messaging chat is
// saved. The default is ask.
const (
	// MessagingAsk: the bot asks in the chat and a listed sender answers there.
	MessagingAsk = "ask"
	// MessagingPairedPrivate: saved at once, to the conversation, when the
	// sender is paired and the chat is one-to-one.
	MessagingPairedPrivate = "paired_private"
	// MessagingNever: no memory note is saved from a messaging app.
	MessagingNever = "never"
)

// ValidMessagingSave reports whether v is a value of the messaging row.
func ValidMessagingSave(v string) bool {
	return v == MessagingAsk || v == MessagingPairedPrivate || v == MessagingNever
}

// The limits of an automatic write. A write over a limit is not lost: it
// becomes a normal proposal that the person confirms.
const (
	// MaxAutoWritesPerTurn is the most notes one turn saves without asking.
	MaxAutoWritesPerTurn = 5
	// MaxAutoWriteBytes is the largest note saved without asking.
	MaxAutoWriteBytes = 4 << 10
)

// SaveInputs is everything that decides how a memory note is saved.
type SaveInputs struct {
	// User is the person's own level (ask, auto, always or never). An unknown
	// value counts as ask.
	User string
	// Agent is the saved agent's rule: agents.MemorySaveAlways,
	// agents.MemorySaveAuto, agents.MemorySaveNever or empty for the person's
	// setting. An unknown value counts as always, because it can only be a rule
	// that was meant to be strict.
	Agent string
	// Messaging is the messaging row. It matters only for a connector chat. An
	// unknown value counts as ask.
	Messaging string
	// Origin is contract.ChangeSource.Kind: "" for a chat the person watches.
	Origin   string
	SubAgent bool
	// Direct says the messaging chat is one-to-one. Paired says the sender of
	// the turn is paired with the bot. Both are false when unknown.
	Direct, Paired bool
}

// effectiveSaveLevel is the level that applies to a memory write. It is the
// strictest of every rule that has a say, and the only place that combines
// them:
//
//   - the person's level, and the agent's rule (always or never; an explicit
//     auto loosens ask and auto, see above);
//   - a sub-agent and an unattended run are not the person: at least ask;
//   - a connector chat is at least ask, because the sender is not necessarily
//     the person, except when the messaging row is paired_private and the
//     sender is paired and the chat is one-to-one. The row never loosens the
//     person's always or never, and its never makes the answer never.
func effectiveSaveLevel(in SaveInputs) string {
	level := levelRank(normalSaveMode(in.User))
	agent := 0
	agentAuto := false
	switch in.Agent {
	case "":
	case agents.MemorySaveAuto:
		agentAuto = true
	case agents.MemorySaveNever:
		agent = levelRank(SaveNever)
	default:
		agent = levelRank(SaveAlways)
	}
	switch {
	case in.Origin == "":
		if agentAuto && level < levelRank(SaveAlways) {
			level = levelRank(SaveAuto)
		}
		if in.SubAgent {
			level = max(level, levelRank(SaveAsk))
		}
	case in.Origin == session.OriginConnector:
		// Only always and never of the person's level hold in a messaging
		// chat; the row decides between ask, auto and never for the rest.
		floor := 0
		if level >= levelRank(SaveAlways) {
			floor = level
		}
		row := levelRank(SaveAsk)
		messaging := in.Messaging
		if agentAuto && messaging != MessagingNever {
			messaging = MessagingPairedPrivate
		}
		switch messaging {
		case MessagingNever:
			row = levelRank(SaveNever)
		case MessagingPairedPrivate:
			if in.Direct && in.Paired && !in.SubAgent {
				row = levelRank(SaveAuto)
			}
		}
		level = max(floor, row)
		if in.SubAgent {
			level = max(level, levelRank(SaveAsk))
		}
	default:
		level = max(level, levelRank(SaveAsk))
	}
	return levelName(max(level, agent))
}

// AddonUserSettingSet is the per-person settings one add-on declares, as the
// host rules see them.
type AddonUserSettingSet struct {
	AddonID  string
	Settings []contract.UserSetting
	// Running is true when the add-on can serve now: installed, enabled and
	// running.
	Running bool
}

// AddonUserSettingsLister is implemented by a broker that knows the per-person
// settings of the add-ons. It lists the installed add-ons that declare some
// and that the user can see. It is optional; a broker without it gives none,
// and the host asks.
type AddonUserSettingsLister interface {
	AddonUserSettings(ctx context.Context, user string) ([]AddonUserSettingSet, error)
}

// levelRank orders the levels: the larger one wins when rules disagree.
func levelRank(mode string) int {
	switch mode {
	case SaveNever:
		return 3
	case SaveAlways:
		return 2
	case SaveAsk:
		return 1
	}
	return 0
}

func levelName(rank int) string {
	return [...]string{SaveAuto, SaveAsk, SaveAlways, SaveNever}[rank]
}

// strictness is levelRank under the name the add-on code uses.
func strictness(mode string) int { return levelRank(mode) }

// ValidSaveMode reports whether v is a value of the save level.
func ValidSaveMode(v string) bool {
	return v == SaveAsk || v == SaveAuto || v == SaveAlways || v == SaveNever
}

// legacySaveKey is the setting of the Memory add-on, read when the person has
// not chosen a level in the host's own setting.
const legacySaveKey = "save-mode"

// storedSaveMode reads the choice of the person. Anything that goes wrong
// reads as ask, the default: a failure never loosens the rule. The level is
// the host's own setting (HostSettingsID, MemorySaveKey). A person who never
// set it keeps the value they gave on the page of the Memory add-on; there auto
// counts only while the add-on runs, as it always did. Another add-on that
// declares the role can only make the answer stricter.
func (c *Changes) storedSaveMode(ctx context.Context, user string) string {
	read := c.s.d.UserSetting
	if read == nil {
		return SaveAsk
	}
	lister, _ := c.s.d.AddonTools.(AddonUserSettingsLister)
	var sets []AddonUserSettingSet
	if lister != nil {
		var err error
		if sets, err = lister.AddonUserSettings(ctx, user); err != nil {
			c.s.log.Warn("studio: the add-on settings could not be listed, asking for confirmation", "error", err)
			return SaveAsk
		}
	}
	hostMode, hostSet := "", false
	v, ok, err := read(ctx, principal(user), HostSettingsID, MemorySaveKey)
	switch {
	case err != nil:
		c.s.log.Warn("studio: the memory save setting could not be read, asking for confirmation", "error", err)
		return SaveAsk
	case ok && ValidSaveMode(v):
		hostMode, hostSet = v, true
	}
	// The add-ons that declare the role: the strictest answer wins (never,
	// then ask, then auto), an add-on that does not run does not count as
	// auto. Once the person chose in the host's own setting, the Memory
	// add-on no longer counts: its page is the old place to choose.
	mode, found := SaveAuto, false
	for _, set := range sets {
		if hostSet && set.AddonID == MemoryAddonID {
			continue
		}
		for _, d := range set.Settings {
			if d.Role != contract.UserSettingRoleMemorySave {
				continue
			}
			found = true
			v, ok, err := read(ctx, principal(user), set.AddonID, d.Key)
			switch {
			case err != nil:
				c.s.log.Warn("studio: the memory save setting could not be read, asking for confirmation", "addon", set.AddonID, "error", err)
				v = SaveAsk
			case !ok || !d.Allows(v):
				v = SaveAsk
			case v == SaveAuto && !set.Running:
				v = SaveAsk
			}
			if strictness(v) > strictness(mode) {
				mode = v
			}
		}
	}
	if !found {
		mode = SaveAsk
		if !hostSet {
			// The Memory add-on was removed: what the person chose on its
			// page still holds as far as it narrows. A looser value never
			// comes back unasked.
			lv, ok, err := read(ctx, principal(user), MemoryAddonID, legacySaveKey)
			switch {
			case err != nil:
				c.s.log.Warn("studio: the memory save setting could not be read, asking for confirmation", "error", err)
			case ok && lv == SaveNever:
				mode = SaveNever
			}
		}
	}
	if hostSet {
		if strictness(mode) > strictness(hostMode) && found {
			return normalSaveMode(mode)
		}
		return hostMode
	}
	return normalSaveMode(mode)
}

func normalSaveMode(m string) string {
	switch m {
	case SaveAuto, SaveAlways, SaveNever:
		return m
	}
	return SaveAsk
}

// storedMessagingSave reads the messaging row of the person. Anything that
// goes wrong reads as ask.
func (c *Changes) storedMessagingSave(ctx context.Context, user string) string {
	if c.s.d.UserSetting == nil {
		return MessagingAsk
	}
	v, ok, err := c.s.d.UserSetting(ctx, principal(user), HostSettingsID, MemorySaveMessagingKey)
	switch {
	case err != nil:
		c.s.log.Warn("studio: the messaging save setting could not be read, asking for confirmation", "error", err)
		return MessagingAsk
	case ok && ValidMessagingSave(v):
		return v
	}
	return MessagingAsk
}

// saveInputsOf gathers what decides how a note of the chat is saved. The
// messaging facts come from the grant of the turn, read now, so a turn whose
// grant is gone counts as a group chat and an unpaired sender.
func (c *Changes) saveInputsOf(ctx context.Context, ch builtins.Chat) SaveInputs {
	in := SaveInputs{User: c.storedSaveMode(ctx, ch.Actor.Principal), Messaging: MessagingAsk,
		Agent: c.s.agentMemorySave(ctx, ch.Actor.Principal, ch.AgentID), Origin: ch.Origin, SubAgent: ch.SubAgent}
	if ch.Origin == session.OriginConnector {
		in.Messaging = c.storedMessagingSave(ctx, ch.Actor.Principal)
		in.Direct, in.Paired = c.s.messagingFacts(ch.Actor.ChatID)
	}
	return in
}

// saveModeOf is the level of a memory write made in the chat.
func (c *Changes) saveModeOf(ctx context.Context, ch builtins.Chat) string {
	return effectiveSaveLevel(c.saveInputsOf(ctx, ch))
}

// autoTurn counts the automatic writes of one turn.
type autoTurn struct{ n atomic.Int32 }

// take reserves one automatic write of the turn.
func (t *autoTurn) take() bool {
	if t == nil {
		return false
	}
	for {
		n := t.n.Load()
		if n >= MaxAutoWritesPerTurn {
			return false
		}
		if t.n.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// give returns a reservation that was not used.
func (t *autoTurn) give() {
	if t != nil {
		t.n.Add(-1)
	}
}

// secretLike matches text that looks like a credential. It is deliberately
// narrow: a note is short prose, and a hit only means the note is not saved
// without asking.
var secretLike = regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----|\b(sk|pk|rk)-[a-z0-9_-]{20,}|\bgh[pousr]_[A-Za-z0-9]{30,}|\bAKIA[0-9A-Z]{16}\b|\bxox[abprs]-[A-Za-z0-9-]{10,}|\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}|\b(password|passwd|secret|api[_ -]?key|token)\s*[:=]\s*\S{6,}`)

// autoFits says whether a note may be saved without asking, and when not, why
// (worded for the model).
func autoFits(content string, turn *autoTurn) (bool, string) {
	switch {
	case len(content) > MaxAutoWriteBytes:
		return false, "the note is longer than a note that is saved at once"
	case secretLike.MatchString(content):
		return false, "the note looks like it holds a secret"
	case !turn.take():
		return false, "this turn already saved the most notes that are saved at once"
	}
	return true, ""
}

// memoryKindOf reports whether a kind is the memory kind.
func memoryKindOf(kind string) bool { return kind == string(contract.ChangeMemory) }

// withoutMemory removes the memory kind from a list.
func withoutMemory(kinds []contract.ChangeKind) []contract.ChangeKind {
	return withoutKind(kinds, contract.ChangeMemory)
}

// withoutKind returns the kinds without one.
func withoutKind(kinds []contract.ChangeKind, drop contract.ChangeKind) []contract.ChangeKind {
	out := make([]contract.ChangeKind, 0, len(kinds))
	for _, k := range kinds {
		if k != drop {
			out = append(out, k)
		}
	}
	return out
}
