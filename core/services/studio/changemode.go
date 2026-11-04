package studio

import (
	"context"
	"regexp"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
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
//	never  the model gets no way to propose a memory note
//
// The setting only ever narrows or keeps the confirmation for anything that is
// not the person's own chat. The rules, in one place:
//
//   - A connector chat keeps ask whatever the setting says: the sender of a
//     message is not necessarily the person, and a stranger's message must
//     never write memory unconfirmed.
//   - An unattended run (a trigger or a job) has no propose tool at all.
//   - A saved agent cannot widen the choice of the person: the agent's grants
//     decide whether it may propose, the person's setting decides how the
//     proposal is applied. An agent with its own memory.write grant still asks
//     when the person said ask.
//   - never applies to every origin: it is the person's choice, and refusing is
//     the narrow side.
//   - Only the memory kind reads the setting. Profile, skill, page and
//     dashboard changes always ask.
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
	SaveAsk   = "ask"
	SaveAuto  = "auto"
	SaveNever = "never"
)

// The limits of an automatic write. A write over a limit is not lost: it
// becomes a normal proposal that the person confirms.
const (
	// MaxAutoWritesPerTurn is the most notes one turn saves without asking.
	MaxAutoWritesPerTurn = 5
	// MaxAutoWriteBytes is the largest note saved without asking.
	MaxAutoWriteBytes = 4 << 10
)

// EffectiveSaveMode is the mode that applies to a write, from the stored
// choice of the person and the origin of the chat (contract.ChangeSource.Kind:
// "" for a chat the person watches). An unknown stored value counts as ask.
func EffectiveSaveMode(stored, origin string, subAgent bool) string {
	switch stored {
	case SaveNever:
		return SaveNever
	case SaveAuto:
		if origin == "" && !subAgent {
			return SaveAuto
		}
	}
	return SaveAsk
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

// strictness orders the modes: the larger one wins when add-ons disagree.
func strictness(mode string) int {
	switch mode {
	case SaveNever:
		return 2
	case SaveAsk:
		return 1
	}
	return 0
}

// storedSaveMode reads the choice of the person. Anything that goes wrong
// reads as ask, the default: a failure never loosens the rule. Without an
// add-on that declares the role there is no choice to read, whatever an old
// stored value says.
func (c *Changes) storedSaveMode(ctx context.Context, user string) string {
	read := c.s.d.UserSetting
	lister, ok := c.s.d.AddonTools.(AddonUserSettingsLister)
	if read == nil || !ok {
		return SaveAsk
	}
	sets, err := lister.AddonUserSettings(ctx, user)
	if err != nil {
		c.s.log.Warn("studio: the add-on settings could not be listed, asking for confirmation", "error", err)
		return SaveAsk
	}
	mode, found := SaveAuto, false
	for _, set := range sets {
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
		return SaveAsk
	}
	switch mode {
	case SaveAuto, SaveNever:
		return mode
	}
	return SaveAsk
}

// saveModeOf is the mode of a memory write made in the chat.
func (c *Changes) saveModeOf(ctx context.Context, ch builtins.Chat) string {
	return EffectiveSaveMode(c.storedSaveMode(ctx, ch.Actor.Principal), ch.Origin, ch.SubAgent)
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
	out := make([]contract.ChangeKind, 0, len(kinds))
	for _, k := range kinds {
		if k != contract.ChangeMemory {
			out = append(out, k)
		}
	}
	return out
}
