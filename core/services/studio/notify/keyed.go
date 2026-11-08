package notify

import (
	"slices"
	"sort"
)

// UpsertMode selects how a keyed notification treats the row that has its key.
type UpsertMode int

const (
	// ModeSet keeps one row for a set of ids that changes over time, such as
	// the updates that are available. See Decide.
	ModeSet UpsertMode = iota
	// ModeOnce posts the row when its key has no row, and does nothing when it
	// has one, even a hidden one. The key names what the row reports, so the
	// same report is silent the second time.
	ModeOnce
)

// UpsertResult says what an Upsert did.
type UpsertResult string

const (
	// ResultCreated: no row had the key; a new row was stored. Announced.
	ResultCreated UpsertResult = "created"
	// ResultRevived: the set had an id the row did not show. The row is
	// unhidden, unread, newer than every other row, and announced.
	ResultRevived UpsertResult = "revived"
	// ResultUpdated: the text or the set changed without a new id. The row
	// keeps its place, its read flag and its hidden flag. Not announced.
	ResultUpdated UpsertResult = "updated"
	// ResultUnchanged: nothing differs. Nothing is written.
	ResultUnchanged UpsertResult = "unchanged"
	// ResultRemoved: the set is empty, so the row is removed.
	ResultRemoved UpsertResult = "removed"
)

// Announced reports whether the result is a new arrival: clients hear it as a
// new notification, and the OnAdd hooks (browser push, Telegram) run.
func (r UpsertResult) Announced() bool { return r == ResultCreated || r == ResultRevived }

// Decide is the rule of Upsert, shared by the stores so they cannot differ.
// old is the row that has the key, nil when there is none. in carries the new
// text and the new ids.
//
// ModeSet:
//   - an empty set removes the row (or does nothing when there is none);
//   - no row: created;
//   - an id the row did not show: revived, whatever the row's flags;
//   - the same ids and the same text: unchanged;
//   - otherwise (fewer ids, or other text): updated in place, quietly.
//
// ModeOnce: created when there is no row, else unchanged.
func Decide(old *Notification, in Notification, mode UpsertMode) UpsertResult {
	if mode == ModeOnce {
		if old == nil {
			return ResultCreated
		}
		return ResultUnchanged
	}
	if len(in.Ids) == 0 {
		if old == nil {
			return ResultUnchanged
		}
		return ResultRemoved
	}
	if old == nil {
		return ResultCreated
	}
	for _, id := range in.Ids {
		if !slices.Contains(old.Ids, id) {
			return ResultRevived
		}
	}
	if sameIds(old.Ids, in.Ids) && sameText(*old, in) {
		return ResultUnchanged
	}
	return ResultUpdated
}

func sameIds(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

func sameText(a, b Notification) bool {
	if a.Kind != b.Kind || a.Status != b.Status || a.Source != b.Source || a.Title != b.Title ||
		a.Body != b.Body || a.Link != b.Link || a.Code != b.Code {
		return false
	}
	ja, _ := encodeParams(a.Params)
	jb, _ := encodeParams(b.Params)
	return ja == jb
}
