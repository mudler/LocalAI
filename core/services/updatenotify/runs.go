package updatenotify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/xlog"
)

// Codes and keys of the inbox items of an upgrade run. A run has one live row
// for each administrator, edited in place while the run goes on, and one end
// row that tells its outcome once. The UI words both from the params
// (utils/inbox.js).
const (
	CodeRun    = "updates.run"
	CodeRunEnd = "updates.run.end"

	keyRun    = "updates.run."
	keyRunEnd = "updates.run.end."
)

// RunLink is the link of the items of a run: the Updates page on that run.
func RunLink(runID string) string { return LinkPage + "?run=" + url.QueryEscape(runID) }

// DefaultRunInterval is the shortest time between two writes of the live row
// of one run.
const DefaultRunInterval = 1500 * time.Millisecond

// maxRunErrorRunes bounds the error of the first item that needs attention.
const maxRunErrorRunes = 160

// RunNotifier tells the administrators about the upgrade runs: it is an
// upgraderun.Observer. It acts only on the changes the frontend that runs the
// run sees (Change.Local), so a cluster writes one set of rows for each run.
//
// While a run goes on, one keyed row for each administrator is edited in
// place (notify.ModeSet, quiet), at most once for each Interval; the last
// change of a burst is written when the interval ends. When the run ends, the
// live row is closed and read, and one end row is posted once (notify.ModeOnce).
type RunNotifier struct {
	Emitter *Emitter
	// Context bounds the writes; nil means context.Background.
	Context context.Context
	// Interval is the shortest time between two writes of the live row of one
	// run. Zero means DefaultRunInterval.
	Interval time.Duration
	// Now and AfterFunc are the clock; tests replace them. AfterFunc returns
	// the function that stops the timer.
	Now       func() time.Time
	AfterFunc func(d time.Duration, f func()) (stop func())

	mu   sync.Mutex
	runs map[string]*runThrottle
}

type runThrottle struct {
	last    time.Time
	pending *upgraderun.Run
	stop    func()
}

// RunChanged implements upgraderun.Observer.
func (n *RunNotifier) RunChanged(c upgraderun.Change) {
	if n == nil || n.Emitter == nil || !c.Local || c.Run.ID == "" {
		return
	}
	run := c.Run
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.runs == nil {
		n.runs = map[string]*runThrottle{}
	}
	st := n.runs[run.ID]
	if run.State != upgraderun.RunRunning {
		// The end is never throttled.
		if st != nil && st.stop != nil {
			st.stop()
		}
		delete(n.runs, run.ID)
		n.writeEnd(run)
		return
	}
	if st == nil {
		st = &runThrottle{}
		n.runs[run.ID] = st
	}
	now := n.now()
	if st.last.IsZero() || now.Sub(st.last) >= n.interval() {
		if st.stop != nil {
			st.stop()
			st.stop = nil
		}
		st.last, st.pending = now, nil
		n.writeLive(run, false)
		return
	}
	st.pending = &run
	if st.stop == nil {
		id := run.ID
		st.stop = n.afterFunc(n.interval()-now.Sub(st.last), func() { n.flush(id) })
	}
}

// flush writes the last change that the throttle held back.
func (n *RunNotifier) flush(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.runs[id]
	if st == nil {
		return
	}
	st.stop = nil
	if st.pending == nil {
		return
	}
	run := *st.pending
	st.pending, st.last = nil, n.now()
	n.writeLive(run, false)
}

func (n *RunNotifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *RunNotifier) interval() time.Duration {
	if n.Interval > 0 {
		return n.Interval
	}
	return DefaultRunInterval
}

func (n *RunNotifier) afterFunc(d time.Duration, f func()) func() {
	if n.AfterFunc != nil {
		return n.AfterFunc(d, f)
	}
	t := time.AfterFunc(d, f)
	return func() { t.Stop() }
}

func (n *RunNotifier) ctx() context.Context {
	if n.Context != nil {
		return n.Context
	}
	return context.Background()
}

func (n *RunNotifier) writeLive(run upgraderun.Run, closing bool) {
	if err := n.Emitter.runLive(n.ctx(), run, closing); err != nil {
		xlog.Warn("Could not write the live notification of an upgrade run", "run", run.ID, "error", err)
	}
}

func (n *RunNotifier) writeEnd(run upgraderun.Run) {
	n.writeLive(run, true)
	if err := n.Emitter.runEnd(n.ctx(), run); err != nil {
		xlog.Warn("Could not write the end notification of an upgrade run", "run", run.ID, "error", err)
	}
}

// runCounts is what the rows say about a run.
type runCounts struct {
	total, done, updated, running, queued, progress int
}

func settled(it upgraderun.Item) bool {
	return it.Phase.Terminal() || (it.Phase == upgraderun.PhaseUnconfirmed && !it.Verifying)
}

func countRun(run upgraderun.Run) runCounts {
	c := runCounts{total: len(run.Items)}
	sum := 0
	for _, it := range run.Items {
		switch {
		case settled(it):
			c.done++
			sum += 100
		case it.Busy():
			c.running++
			sum += min(max(it.Progress, 0), 100)
		case it.Phase == upgraderun.PhaseQueued:
			c.queued++
		}
		if it.Phase == upgraderun.PhaseUpdated {
			c.updated++
		}
	}
	if c.total > 0 {
		c.progress = sum / c.total
	}
	return c
}

// needsAttention reports whether an item ended in a way a person should look
// at: it failed, was put back, could not be confirmed, or was skipped.
func needsAttention(it upgraderun.Item) bool {
	switch it.Phase {
	case upgraderun.PhaseFailed, upgraderun.PhaseRolledBack, upgraderun.PhaseUnconfirmed, upgraderun.PhaseSkipped:
		return true
	}
	return false
}

func runLabel(run upgraderun.Run) string {
	if run.Source == upgraderun.SourceAuto {
		return "Automatic update"
	}
	return "Update run"
}

// each runs fn for the live and end rows of every administrator.
func (e *Emitter) eachAdmin(ctx context.Context, fn func(svc *notify.Service, principal string) error) error {
	svc, admins := e.targets(ctx)
	if svc == nil {
		return nil
	}
	var errs []error
	for _, p := range admins {
		if err := fn(svc, p); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// runLive writes the live row of a run for each administrator. With closing,
// the row says the run is over (full progress, nothing running or queued) and
// is marked read: the end row carries the news.
func (e *Emitter) runLive(ctx context.Context, run upgraderun.Run, closing bool) error {
	c := countRun(run)
	if closing {
		c.progress, c.running, c.queued = 100, 0, 0
	}
	n := notify.Notification{
		Key: keyRun + run.ID, Ids: []string{run.ID},
		Kind: notify.KindRunEnded, Status: notify.StatusOK, Source: notify.SourceSystem,
		Link: RunLink(run.ID), Code: CodeRun,
		Title: fmt.Sprintf("%s: %d of %d done", runLabel(run), c.done, c.total),
		Params: map[string]any{
			"run": run.ID, "progress": c.progress, "done": c.done, "total": c.total,
			"source": string(run.Source), "updated": c.updated, "running": c.running, "queued": c.queued,
		},
	}
	switch {
	case closing:
		n.Body = "The run is over."
	case c.running > 0:
		n.Body = plural(c.running, "update is running", "updates are running")
		if c.queued > 0 {
			n.Body += fmt.Sprintf(", %d waiting.", c.queued)
		} else {
			n.Body += "."
		}
	case c.queued > 0:
		n.Body = fmt.Sprintf("%d waiting.", c.queued)
	}
	return e.eachAdmin(ctx, func(svc *notify.Service, p string) error {
		n.Principal = p
		got, _, err := svc.Upsert(ctx, n, notify.ModeSet)
		if err != nil {
			return err
		}
		if closing && got.ID != 0 {
			return svc.MarkRead(ctx, p, []int64{got.ID})
		}
		return nil
	})
}

// runEnd posts the end row of a run once for each administrator.
func (e *Emitter) runEnd(ctx context.Context, run upgraderun.Run) error {
	c := countRun(run)
	var attention []upgraderun.Item
	for _, it := range run.Items {
		if needsAttention(it) {
			attention = append(attention, it)
		}
	}
	label := runLabel(run)
	n := notify.Notification{
		Key:  keyRunEnd + run.ID,
		Kind: notify.KindRunEnded, Status: notify.StatusOK, Source: notify.SourceSystem,
		Link: RunLink(run.ID), Code: CodeRunEnd,
		Params: map[string]any{
			"run": run.ID, "total": c.total, "updated": c.updated, "failed": len(attention), "source": string(run.Source),
		},
	}
	verb := "finished"
	if run.State == upgraderun.RunCancelled {
		verb = "cancelled"
	}
	n.Title = fmt.Sprintf("%s %s: %d of %d updated", label, verb, c.updated, c.total)
	if len(attention) > 0 {
		first := attention[0]
		errText := runItemError(first)
		n.Kind, n.Status = notify.KindNeedsAttention, notify.StatusFailed
		n.Title += fmt.Sprintf(", %d need attention", len(attention))
		n.Body = first.Name
		if errText != "" {
			n.Body += ": " + errText
		}
		if len(attention) > 1 {
			n.Body += fmt.Sprintf(" and %d more", len(attention)-1)
		}
		n.Params["first_name"] = oneLine(first.Name, 80)
		n.Params["first_from"] = oneLine(first.From, 80)
		n.Params["first_error"] = errText
	}
	return e.upsertAll(ctx, n, notify.ModeOnce)
}

// runItemError is the reason an item needs attention, in one bounded line of
// plain text: no server paths, no credentials.
func runItemError(it upgraderun.Item) string {
	s := it.Error
	if s == "" {
		s = it.Message
	}
	if s == "" {
		s = strings.ReplaceAll(it.ErrorKey, "_", " ")
	}
	return oneLine(redact(withoutPaths(s)), maxRunErrorRunes)
}

var (
	userinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)
	secretKV = regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api[_-]?key|apikey|authorization|credential)s?\b(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|\S+)`)
	bearer   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
)

// redact removes what looks like a credential from an error text before it
// reaches the inbox, a browser push service or a chat.
func redact(s string) string {
	s = userinfo.ReplaceAllString(s, "${1}")
	s = bearer.ReplaceAllString(s, "${1} [removed]")
	return secretKV.ReplaceAllString(s, "${1}=[removed]")
}
