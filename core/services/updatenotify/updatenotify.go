// Package updatenotify writes the inbox items of the update center: the digest
// of the updates that need a person, the result of an automatic update pass,
// and the notice of a check that keeps failing. It decides what to say and to
// whom; the rules of keyed rows (when an item comes back, when it stays quiet)
// are those of notify.Service.Upsert.
//
// The package keeps no state. Whoever calls it must be the one runner of the
// work that produces the data (the leader of the backend check, the add-on
// owner), because the inbox does not coordinate callers.
package updatenotify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/xlog"
)

// Codes of the items. The UI words them from the params (utils/inbox.js).
const (
	CodeDigest      = "updates.digest"
	CodeResult      = "updates.result"
	CodeCheckFailed = "updates.checkFailed"
	// CodeOpDone and CodeOpFailed tell the end of an install, removal or
	// upgrade that an administrator started. A cancelled one is CodeOpFailed
	// with the outcome "cancelled".
	CodeOpDone   = "updates.opDone"
	CodeOpFailed = "updates.opFailed"
)

// Keys of the rows. One digest and one failed-check row for each admin; one
// result row for each distinct report (see Result).
const (
	KeyDigest      = "updates.digest"
	KeyCheckFailed = "updates.checkFailed"
	keyResult      = "updates.result."
	keyOp          = "updates.op."
)

// Links of the items: the Updates page on the list that shows what the item is
// about. The ready filter lists what the digest counts; needs-you lists the
// failed and held ones.
const (
	LinkReady    = "/app/updates?filter=ready"
	LinkNeedsYou = "/app/updates?filter=needs-you"
	LinkPage     = "/app/updates"
	LinkModels   = "/app/models"
	LinkAddons   = "/app/addons"
)

// Families of the automatic update.
const (
	FamilyBackends = "backends"
	FamilyAddons   = "addons"
)

// Outcomes of one thing in an automatic pass.
const (
	OutcomeUpdated = "updated"
	OutcomeFailed  = "failed"
	OutcomeHeld    = "held"
)

// maxItems is how many names the params and the text of an item list.
const maxItems = 3

// Emitter writes the items for every administrator.
type Emitter struct {
	// Inbox returns the inbox, or nil when the notification center could not
	// start. It is read at each call because the inbox starts after the update
	// checker is built. The chats do not matter: the inbox runs without them.
	Inbox func() *notify.Service
	// Admins returns the principals to tell: the active administrators, or the
	// anonymous principal when authentication is off.
	Admins func(ctx context.Context) ([]string, error)
}

func (e *Emitter) targets(ctx context.Context) (*notify.Service, []string) {
	if e == nil || e.Inbox == nil || e.Admins == nil {
		return nil, nil
	}
	svc := e.Inbox()
	if svc == nil {
		xlog.Debug("Update notifications skipped: there is no notification center")
		return nil, nil
	}
	admins, err := e.Admins(ctx)
	if err != nil {
		xlog.Warn("Update notifications skipped: could not list the administrators", "error", err)
		return nil, nil
	}
	return svc, admins
}

// upsertAll writes the item for each admin. It tries every admin and returns
// the errors together.
func (e *Emitter) upsertAll(ctx context.Context, n notify.Notification, mode notify.UpsertMode) error {
	svc, admins := e.targets(ctx)
	var errs []error
	for _, p := range admins {
		n.Principal = p
		if _, _, err := svc.Upsert(ctx, n, mode); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// Digest writes the digest of the backend updates that need a person, from the
// read model, or removes it. Only what no automatic update covers is counted
// (backendupdates.PendingUpdates), so with the automatic update on there is
// none. The digest is reconciled only when every source that can be checked
// answered: an unanswered source would look like "all done" and remove it.
//
// Add-ons are not part of this digest. Their update state is read by the
// add-on owner, with the actor of an administrator, and a frontend that is not
// the owner cannot read it; the digest would change with the frontend that
// holds the lock.
func (e *Emitter) Digest(ctx context.Context, snap backendupdates.Snapshot) error {
	items, ids, complete := backendupdates.PendingUpdates(snap)
	if !complete {
		xlog.Debug("Update digest not changed: a source did not answer")
		return nil
	}
	n := notify.Notification{
		Key: KeyDigest, Kind: notify.KindNeedsAttention, Status: notify.StatusOK, Source: notify.SourceSystem,
		Link: LinkReady, Code: CodeDigest, Ids: ids,
	}
	if len(items) > 0 {
		names := make([]string, len(items))
		for i, it := range items {
			names[i] = it.Name
		}
		n.Title = plural(len(items), "backend update available", "backend updates available")
		n.Body = listWithMore(names, len(names))
		n.Params = map[string]any{"family": FamilyBackends, "count": len(items), "names": first(names)}
	}
	return e.upsertAll(ctx, n, notify.ModeSet)
}

// CheckFailed tells that these sources (gallery, library or addons) failed their
// last automatic checks, or removes the notice when the list is empty. A source
// stays in the set while it keeps failing, so the notice is written once.
func (e *Emitter) CheckFailed(ctx context.Context, sources []string) error {
	sources = sortedCopy(sources)
	n := notify.Notification{
		Key: KeyCheckFailed, Kind: notify.KindNeedsAttention, Status: notify.StatusFailed, Source: notify.SourceSystem,
		Link: LinkPage, Code: CodeCheckFailed, Ids: sources,
	}
	if len(sources) > 0 {
		names := make([]string, len(sources))
		for i, s := range sources {
			names[i] = sourceName(s)
		}
		n.Title = "The update check failed"
		n.Body = "Could not check: " + strings.Join(names, ", ") + ". The last two automatic checks failed, so updates may be missing from the list."
		n.Params = map[string]any{"sources": sources}
	}
	return e.upsertAll(ctx, n, notify.ModeSet)
}

func sourceName(s string) string {
	switch s {
	case "gallery":
		return "backend gallery"
	case "library":
		return "backend library"
	case FamilyAddons:
		return "add-ons"
	}
	return s
}

// Outcome is what an automatic pass did with one thing.
type Outcome struct {
	// Kind is OutcomeUpdated, OutcomeFailed or OutcomeHeld.
	Kind string
	// ID names the thing and the target it was moved to (or could not be), so the
	// same failure of the same target is the same outcome.
	ID string
	// Name is what the text says: a backend or add-on name.
	Name string
	// From and To are the versions, either may be empty.
	From, To string
	// Err is the reason of a failure, in one line.
	Err string
}

// Result is one automatic update pass of one family.
type Result struct {
	// Family is FamilyBackends or FamilyAddons.
	Family   string
	Outcomes []Outcome
}

// Result posts one item for the pass: how many were updated, failed or held for
// approval, and which. A pass that did nothing posts nothing. The item is keyed
// by the family and the set of outcomes, and posted once for each key: a pass
// that finds exactly the same outcomes as an earlier one (the same failure of
// the same target, the same add-on held) stays silent, also when the person
// deleted the earlier item. A failed or held outcome makes the item a failure
// or an item that needs a person, and it links to the list of those.
func (e *Emitter) Result(ctx context.Context, r Result) error {
	if len(r.Outcomes) == 0 || (r.Family != FamilyBackends && r.Family != FamilyAddons) {
		return nil
	}
	var updated, failed, held []Outcome
	ids := make([]string, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		ids = append(ids, o.Kind+":"+o.ID)
		switch o.Kind {
		case OutcomeUpdated:
			updated = append(updated, o)
		case OutcomeFailed:
			failed = append(failed, o)
		case OutcomeHeld:
			held = append(held, o)
		}
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	label := "Backends"
	if r.Family == FamilyAddons {
		label = "Add-ons"
	}
	var parts, body []string
	params := map[string]any{"family": r.Family}
	add := func(group []Outcome, key, part, line string, describe func(Outcome) string) {
		if len(group) == 0 {
			return
		}
		items := make([]string, len(group))
		for i, o := range group {
			items[i] = describe(o)
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(group), part))
		body = append(body, line+listWithMore(items, len(items))+".")
		params[key] = len(group)
		params[key+"Items"] = first(items)
	}
	add(updated, "updated", "updated automatically", "Updated: ", func(o Outcome) string { return versions(o) })
	add(failed, "failed", "failed", "Failed: ", func(o Outcome) string {
		if o.Err == "" {
			return o.Name
		}
		return o.Name + " (" + oneLine(withoutPaths(o.Err), 100) + ")"
	})
	add(held, "held", "waiting for approval", "Waiting for your approval: ", func(o Outcome) string { return versions(o) })

	n := notify.Notification{
		Key: keyResult + r.Family + "." + hex.EncodeToString(sum[:])[:16], Source: notify.SourceSystem,
		Title: label + ": " + strings.Join(parts, ", "), Body: strings.Join(body, " "), Code: CodeResult, Params: params,
		Kind: notify.KindRunEnded, Status: notify.StatusOK, Link: LinkPage,
	}
	if len(held) > 0 {
		n.Kind, n.Link = notify.KindNeedsAttention, LinkNeedsYou
	}
	if len(failed) > 0 {
		n.Kind, n.Status, n.Link = notify.KindNeedsAttention, notify.StatusFailed, LinkNeedsYou
	}
	return e.upsertAll(ctx, n, notify.ModeOnce)
}

// Operation tells the administrators that an install, removal or upgrade of a
// model or backend reached its end: completed, failed or cancelled. The item
// is keyed by the operation ID and posted once for each administrator, so a
// replayed report of the same operation is silent. An operation with no ID is
// an automatic pass, which Result reports; it posts nothing here.
//
// A model upgrade that completed or failed is not written here: the upgrader
// tells it through ModelUpgradeResult, the only producer of that row, with
// the detail the operation does not have (whether the old version was put
// back, the versions). A cancelled upgrade is told here, as the upgrader does
// not tell a cancel.
func (e *Emitter) Operation(ctx context.Context, op galleryop.OpResult) error {
	if op.ID == "" || op.Name == "" {
		return nil
	}
	if op.Upgrade && !op.Backend && !op.Addon && op.Outcome != galleryop.OpCancelled {
		return nil
	}
	kind, link := "model", LinkModels
	switch {
	case op.Addon:
		kind, link = "addon", LinkAddons
	case op.Backend:
		kind, link = "backend", LinkPage
	}
	action := "install"
	switch {
	case op.Deletion:
		action = "delete"
	case op.Upgrade:
		action = "upgrade"
	}
	label := strings.ToUpper(kind[:1]) + kind[1:]
	if op.Addon {
		label = "Add-on"
	}
	n := notify.Notification{
		Key: keyOp + op.ID, Source: notify.SourceSystem, Kind: notify.KindRunEnded, Status: notify.StatusOK, Link: link,
		Params: map[string]any{"kind": kind, "action": action, "name": op.Name},
	}
	switch op.Outcome {
	case galleryop.OpCompleted:
		n.Code = CodeOpDone
		n.Title = label + " " + op.Name + " " + map[string]string{"install": "installed", "delete": "removed", "upgrade": "upgraded"}[action]
	case galleryop.OpCancelled:
		n.Code, n.Kind = CodeOpFailed, notify.KindNeedsAttention
		n.Title = label + " " + op.Name + ": the " + action + " was cancelled"
		n.Params["outcome"] = "cancelled"
	case galleryop.OpFailed:
		n.Code, n.Kind, n.Status = CodeOpFailed, notify.KindNeedsAttention, notify.StatusFailed
		n.Title = label + " " + op.Name + ": the " + action + " failed"
		n.Body = errorLine(op.Err)
		n.Params["outcome"] = "failed"
		if op.NeedsApproval {
			// The install waits for a new review of its host access.
			n.Params["needsApproval"] = true
		}
		if n.Body != "" {
			n.Params["error"] = n.Body
		}
	default:
		return nil
	}
	return e.upsertAll(ctx, n, notify.ModeOnce)
}

// versions is "name from to to", "name to" or "name".
func versions(o Outcome) string {
	switch {
	case o.From != "" && o.To != "":
		return o.Name + " " + o.From + " to " + o.To
	case o.To != "":
		return o.Name + " " + o.To
	}
	return o.Name
}

// absPath matches an absolute path (POSIX, or Windows with a drive letter)
// and keeps its last element.
var absPath = regexp.MustCompile(`(?:[A-Za-z]:)?(?:[\\/][^\s\\/:"'()]+)+[\\/]([^\s\\/:"'()]+)`)

// withoutPaths replaces each absolute path in s by its file name. An error
// text reaches the inbox and a browser push service, which must not learn
// the layout of the server. A path counts only at the start of the text or
// after a space, a tab, a quote, a parenthesis or an equals sign, so a URL, a
// scheme-less URL and a relative path are left alone.
func withoutPaths(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range absPath.FindAllStringSubmatchIndex(s, -1) {
		start := m[0]
		if start > 0 && !strings.ContainsRune(" \t\"'(=", rune(s[start-1])) {
			// The tail of a longer token: the path of a URL, a relative path
			// ("models/sub/x.gguf") or a scheme-less URL. Only a path that
			// starts a word is absolute.
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(s[m[2]:m[3]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// errorLine is the body of an item that tells a failure: one line, no
// server paths.
func errorLine(s string) string { return oneLine(withoutPaths(s), 200) }

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func plural(n int, one, other string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, other)
}

func first(items []string) []string {
	if len(items) > maxItems {
		return append([]string(nil), items[:maxItems]...)
	}
	return append([]string(nil), items...)
}

// listWithMore is "a, b, c" or "a, b, c and 2 more".
func listWithMore(items []string, total int) string {
	shown := first(items)
	s := strings.Join(shown, ", ")
	if more := total - len(shown); more > 0 {
		s += fmt.Sprintf(" and %d more", more)
	}
	return s
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
