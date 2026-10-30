package triggers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A file that arrives in the folder of a file trigger waits for a quiet
// period. Every further change of the file restarts the wait. When the wait
// ends and the file is still in the folder, the Runner claims a run for that
// version of the file.

type pendKey struct {
	trigger string
	node    int64
}

type pending struct {
	path     string
	deadline time.Time
	rev      int64     // revision of the last change; the cursor stays below it
	changed  time.Time // the pass that saw the last change
	failed   int       // settle attempts in a row that could not read the file
	failedAt time.Time // the first of them
}

const (
	maxFailedReads = 10
	minFailedSpan  = time.Hour      // a file is dropped after this long and maxFailedReads failures
	maxFileWait    = 24 * time.Hour // from the first failed read
	maxChats       = 1000
)

type feedState struct {
	loaded bool  // the cursor was read from the Store
	cur    int64 // the last revision read from the feed
	saved  int64
	hasSav bool
	pend   map[pendKey]*pending
}

func newFeedState() feedState { return feedState{pend: map[pendKey]*pending{}} }

func (f *feedState) forget(triggerID string) {
	for k := range f.pend {
		if k.trigger == triggerID {
			delete(f.pend, k)
		}
	}
}

// under tells whether p is a path below folder.
func under(p, folder string) bool { return strings.HasPrefix(p, folder+"/") }

func (r *Runner) filePass(ctx context.Context, trs []Trigger, now time.Time) error {
	if r.o.Workspace == nil {
		return nil
	}
	files := map[string]Trigger{}
	for _, t := range trs {
		if t.Kind == KindFile {
			files[t.ID] = t
		}
	}
	for k := range r.feed.pend { // a trigger that is gone or off waits for nothing
		if _, ok := files[k.trigger]; !ok {
			delete(r.feed.pend, k)
		}
	}
	f := &r.feed
	if !f.loaded {
		rev, ok, err := r.o.Store.Cursor(ctx)
		if err != nil {
			return err
		}
		if !ok { // the first start: only what changes from now on
			if rev, err = r.latestRev(ctx); err != nil {
				return err
			}
			if err := r.o.Store.SetCursor(ctx, rev); err != nil {
				return err
			}
		}
		f.cur, f.saved, f.hasSav, f.loaded = rev, rev, true, true
	}
	if len(files) > 0 {
		if err := r.readFeed(ctx, files, now); err != nil {
			return err
		}
	} else if err := r.skipFeed(ctx); err != nil {
		return err
	}
	r.pruneChats()
	if err := r.settle(ctx, files, now); err != nil {
		return err
	}
	return r.saveCursor(ctx)
}

// latestRev is the newest revision of the workspace, 0 for an empty one.
func (r *Runner) latestRev(ctx context.Context) (int64, error) {
	h, err := r.o.Workspace.History(ctx, r.o.Admin, workspace.HistoryQuery{Path: "/", Limit: 1})
	if err != nil || len(h) == 0 {
		return 0, err
	}
	return h[0].Rev, nil
}

// skipFeed moves the cursor to the newest revision when no file trigger is
// enabled, so that the feed is not read for nothing.
func (r *Runner) skipFeed(ctx context.Context) error {
	rev, err := r.latestRev(ctx)
	if err != nil {
		return err
	}
	if rev > r.feed.cur {
		r.feed.cur = rev
	}
	return nil
}

func (r *Runner) readFeed(ctx context.Context, files map[string]Trigger, now time.Time) error {
	f := &r.feed
	for {
		page, err := r.o.Workspace.ChangesSince(ctx, r.o.Admin, f.cur, feedPage)
		if err != nil {
			return err
		}
		for _, cs := range page {
			if err := r.apply(ctx, cs, files, now); err != nil {
				return err
			}
			f.cur = cs.Rev
		}
		if len(page) < feedPage {
			return nil
		}
	}
}

// apply records what one change set means for the file triggers.
func (r *Runner) apply(ctx context.Context, cs workspace.ChangeSet, files map[string]Trigger, now time.Time) error {
	own := "" // the trigger whose run made this change set
	if cs.ChatID != "" {
		if c, ok := r.chats[cs.ChatID]; ok {
			own = c.trigger
		} else {
			run, err := r.o.Store.RunByChat(ctx, cs.ChatID)
			switch {
			case err == nil:
				own = run.TriggerID
			case !errors.Is(err, ErrNotFound):
				return err
			}
		}
	}
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, op := range cs.Ops {
		for _, id := range ids {
			t := files[id]
			if cs.CreatedAt.Before(t.Since) {
				continue // the trigger did not watch yet
			}
			r.applyOp(t, op, cs.Rev, own == t.ID, now)
		}
	}
	return nil
}

func (r *Runner) applyOp(t Trigger, op workspace.Op, rev int64, ownRun bool, now time.Time) {
	f := &r.feed
	key := pendKey{t.ID, op.NodeID}
	isFile := op.NewBlob != "" || op.OldBlob != ""
	switch op.Kind {
	case workspace.OpDelete:
		delete(f.pend, key)
	case workspace.OpCreate, workspace.OpWrite, workspace.OpMove:
		switch {
		case op.Kind == workspace.OpMove && !isFile:
			// A folder moved: the files that wait below it move with it.
			for k, p := range f.pend {
				if k.trigger != t.ID || !(p.path == op.OldPath || under(p.path, op.OldPath)) {
					continue
				}
				np := op.NewPath + strings.TrimPrefix(p.path, op.OldPath)
				if under(np, t.Folder) {
					p.path = np
				} else {
					delete(f.pend, k)
				}
			}
		case !isFile || op.NewBlob == "":
			// A folder was made, or a file has no content: nothing arrives.
		case !under(op.NewPath, t.Folder):
			delete(f.pend, key) // moved out, or elsewhere
		case path.Base(op.NewPath) == RulesFile:
			// A rules file steers the runs; it is never an arrival. A
			// waiting file renamed to it drops its wait.
			delete(f.pend, key)
		case ownRun:
			delete(f.pend, key) // the run of this trigger wrote it
		case op.Kind == workspace.OpMove && under(op.OldPath, t.Folder):
			if p := f.pend[key]; p != nil { // moved inside the folder
				p.path = op.NewPath // keep the arrival revision: the cursor stays below it
			}
		default:
			f.pend[key] = &pending{path: op.NewPath, deadline: now.Add(t.QuietOrDefault()), rev: rev, changed: now}
		}
	}
}

// settle claims a run for each file whose quiet period has ended.
func (r *Runner) settle(ctx context.Context, files map[string]Trigger, now time.Time) error {
	f := &r.feed
	var due []pendKey
	for k, p := range f.pend {
		if !p.deadline.After(now) {
			due = append(due, k)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := f.pend[due[i]], f.pend[due[j]]
		if a.rev != b.rev {
			return a.rev < b.rev
		}
		return due[i].node < due[j].node
	})
	var errs []error
	lastDue := map[string]time.Time{}
	for _, k := range due {
		p := f.pend[k]
		t := files[k.trigger]
		n, err := r.o.Workspace.Stat(ctx, r.o.Admin, p.path)
		if err == nil || errors.Is(err, workspace.ErrNotFound) {
			p.failed = 0
		}
		switch {
		case err != nil && !errors.Is(err, workspace.ErrNotFound):
			// Keep the file waiting; the other due files go on.
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				errs = append(errs, err) // the caller's context, not the file
				continue
			}
			if p.failed == 0 {
				p.failedAt = now
			}
			p.failed++
			if (p.failed >= maxFailedReads && now.Sub(p.failedAt) >= minFailedSpan) || now.Sub(p.failedAt) >= maxFileWait {
				r.logf("triggers: file %s of trigger %s dropped after %d failed reads: %v", p.path, t.ID, p.failed, err)
				delete(f.pend, k)
				continue
			}
			r.logf("triggers: file %s of trigger %s could not be read: %v", p.path, t.ID, err)
			errs = append(errs, err)
			continue
		case errors.Is(err, workspace.ErrNotFound):
			delete(f.pend, k)
			continue
		case n.ID != k.node || n.Kind != workspace.KindFile || !under(n.Path, t.Folder):
			delete(f.pend, k)
			continue
		}
		last, seen := lastDue[t.ID]
		if !seen {
			// Read once a pass, before this pass claims anything.
			var err error
			if last, err = r.lastClaim(ctx, t); err != nil {
				return err
			}
			lastDue[t.ID] = last
		}
		// A file that changed after the last run began, such as the data of a
		// page that saves after each change it sees, waits for the gap. Files
		// that were already waiting then (a batch) run without it.
		if wait := last.Add(t.GapOrDefault()).Sub(now); !last.IsZero() && !p.changed.Before(last) && wait > 0 {
			// Too soon after the last run: the file waits, and further changes
			// to it while it waits start no extra run.
			p.deadline = now.Add(wait)
			continue
		}
		run := Run{ID: newID(), TriggerID: t.ID, DueKey: fmt.Sprintf("%d@%s", n.ID, n.Blob), Due: now.UTC(), File: n.Path}
		if err := r.admit(ctx, t, &run, now, false, false); err != nil {
			return err
		}
		if err := r.claim(ctx, run); err != nil {
			return err
		}
		delete(f.pend, k)
	}
	return errors.Join(errs...)
}

// lastClaim is the due time of the newest run of t, zero when it has none.
func (r *Runner) lastClaim(ctx context.Context, t Trigger) (time.Time, error) {
	runs, err := r.o.Store.ListRuns(ctx, t.ID, 1)
	if err != nil || len(runs) == 0 {
		return time.Time{}, err
	}
	return runs[0].Due, nil
}

// saveCursor saves the revision up to which nothing is waiting: below the
// oldest change of a file that still waits, else the last revision read.
func (r *Runner) saveCursor(ctx context.Context) error {
	f := &r.feed
	rev := f.cur
	for _, p := range f.pend {
		if p.rev-1 < rev {
			rev = p.rev - 1
		}
	}
	if rev < 0 {
		rev = 0
	}
	if f.hasSav && rev == f.saved {
		return nil
	}
	if err := r.o.Store.SetCursor(ctx, rev); err != nil {
		return err
	}
	f.saved, f.hasSav = rev, true
	return nil
}

// chatRun is a chat of a run this Runner started. end is 0 while the run
// goes; after it, the newest revision of the workspace when the run ended.
type chatRun struct {
	trigger, run string
	end          int64
	ended        bool
	seq          int64
}

// rememberChat keeps the chat of a run that was just started, so that the loop
// guard holds even when the Store could not record the chat. At most maxChats
// entries are kept; the oldest go first.
func (r *Runner) rememberChat(run Run) {
	if run.ChatID == "" {
		return
	}
	if r.chats == nil {
		r.chats = map[string]chatRun{}
	}
	r.chatSeq++
	r.chats[run.ChatID] = chatRun{trigger: run.TriggerID, run: run.ID, seq: r.chatSeq}
	for len(r.chats) > maxChats {
		oldest, first := "", true
		for c, x := range r.chats {
			if first || x.seq < r.chats[oldest].seq {
				oldest, first = c, false
			}
		}
		r.logf("triggers: too many chats kept for the loop guard; dropping %s", oldest)
		delete(r.chats, oldest)
	}
}

// endChat marks the chat of a run that ended. The entry stays until the feed
// has been read up to the newest revision of that moment, because the last
// writes of the chat are read after the run ends.
func (r *Runner) endChat(ctx context.Context, runID string) {
	for c, x := range r.chats {
		if x.run != runID {
			continue
		}
		if r.o.Workspace == nil {
			delete(r.chats, c)
			continue
		}
		rev, err := r.latestRev(ctx)
		if err != nil {
			r.logf("triggers: could not read the newest revision at the end of run %s: %v", runID, err)
			rev = math.MaxInt64 // kept until the cap drops it
		}
		x.end, x.ended = rev, true
		r.chats[c] = x
	}
}

// pruneChats drops the chats of ended runs whose writes were all read.
func (r *Runner) pruneChats() {
	for c, x := range r.chats {
		if x.ended && x.end <= r.feed.cur {
			delete(r.chats, c)
		}
	}
}
