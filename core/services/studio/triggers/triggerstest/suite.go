package triggerstest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// Option tunes RunStoreSuite.
type Option func(res *time.Duration)

// WithResolution says that the backend keeps times to d only. PostgreSQL
// timestamps are exact to a microsecond, so the nanosecond specs use d as the
// smallest step and cut their times to it.
func WithResolution(d time.Duration) Option { return func(res *time.Duration) { *res = d } }

// NewStore returns an empty Store for one spec. The spec closes nothing: the
// function registers its own cleanup with DeferCleanup.
type NewStore func() triggers.Store

var base = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func sample(id, owner, name string) triggers.Trigger {
	return triggers.Trigger{
		ID: id, Owner: owner, Name: name, Prompt: "Summarise the new file.", Enabled: true,
		Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "Europe/Rome", DailyCap: 5,
		Since: base, AgentID: "agent-1", SourceChatID: "chat-1", Model: "model-1",
	}
}

func fileTrigger(id, owner string) triggers.Trigger {
	return triggers.Trigger{
		ID: id, Owner: owner, Name: "Contracts " + id, Prompt: "Read the file.", Enabled: false,
		Kind: triggers.KindFile, Folder: "/users/1/Contracts", Quiet: 45 * time.Second, Since: base.Add(time.Hour), Proposed: true,
		Outcome: triggers.OutcomeDeclined, OutcomeAt: base.Add(2 * time.Hour), Touched: true,
	}
}

func run(id, trigger, key string, status triggers.Status) triggers.Run {
	return triggers.Run{ID: id, TriggerID: trigger, DueKey: key, Due: base, Status: status}
}

func same(a, b triggers.Trigger) bool {
	return a.ID == b.ID && a.Owner == b.Owner && a.Name == b.Name && a.Prompt == b.Prompt &&
		a.Enabled == b.Enabled && a.Kind == b.Kind && a.Schedule == b.Schedule && a.TZ == b.TZ &&
		a.Folder == b.Folder && a.Quiet == b.Quiet && a.DailyCap == b.DailyCap && a.Since.Equal(b.Since) &&
		a.AgentID == b.AgentID && a.SourceChatID == b.SourceChatID && a.Model == b.Model && a.Proposed == b.Proposed &&
		a.Outcome == b.Outcome && a.OutcomeAt.Equal(b.OutcomeAt) && a.Touched == b.Touched && a.Change == b.Change
}

func sameRun(a, b triggers.Run) bool {
	return a.ID == b.ID && a.TriggerID == b.TriggerID && a.DueKey == b.DueKey && a.Due.Equal(b.Due) &&
		a.Status == b.Status && a.Reason == b.Reason && a.ChatID == b.ChatID && a.File == b.File &&
		a.Started.Equal(b.Started) && a.Ended.Equal(b.Ended)
}

// RunStoreSuite registers the specs that check a triggers.Store: triggers,
// claims, run states, listings in their order, counts, the cursor, the last due
// times, and claims from concurrent goroutines. Call it inside a Describe.
// Times are checked to the nanosecond unless an option names a coarser
// resolution of the backend.
func RunStoreSuite(newStore NewStore, opts ...Option) {
	ctx := context.Background()
	res := time.Nanosecond
	for _, o := range opts {
		o(&res)
	}
	It("stores a trigger with every field", func() {
		s := newStore()
		for _, tr := range []triggers.Trigger{sample("t1", "1", "Morning"), fileTrigger("t2", "1")} {
			Expect(s.PutTrigger(ctx, tr)).To(Succeed())
			got, err := s.GetTrigger(ctx, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(same(got, tr)).To(BeTrue(), "GetTrigger(%s) = %+v; want %+v", tr.ID, got, tr)
		}
	})
	It("stores a change record with its change", func() {
		s := newStore()
		tr := sample("c1", "1", "Morning")
		tr.Enabled, tr.Proposed = false, true
		tr.Change = triggers.Change{Op: triggers.ChangeUpdate, Target: "t1", Stamp: "abc", On: true, Chat: "chat-1"}
		Expect(s.PutTrigger(ctx, tr)).To(Succeed())
		got, err := s.GetTrigger(ctx, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(same(got, tr)).To(BeTrue(), "GetTrigger = %+v; want %+v", got, tr)
		Expect(got.IsChange()).To(BeTrue())
		Expect(got.Pending()).To(BeFalse(), "a change record is not a proposal")
		en, _ := s.EnabledTriggers(ctx)
		Expect(en).To(BeEmpty(), "a change record is never enabled")
	})
	It("does not find an unknown trigger", func() {
		s := newStore()
		_, err := s.GetTrigger(ctx, "nope")
		Expect(err).To(MatchError(triggers.ErrNotFound), "GetTrigger")
		_, err = s.GetRun(ctx, "nope")
		Expect(err).To(MatchError(triggers.ErrNotFound), "GetRun")
		_, err = s.RunByChat(ctx, "nope")
		Expect(err).To(MatchError(triggers.ErrNotFound), "RunByChat")
		err = s.SetRunStatus(ctx, run("nope", "t1", "k", triggers.StatusDone))
		Expect(err).To(MatchError(triggers.ErrNotFound), "SetRunStatus")
		Expect(s.DeleteTrigger(ctx, "nope")).To(Succeed(), "DeleteTrigger of an unknown trigger")
	})
	It("replaces a trigger that is put again", func() {
		s := newStore()
		tr := sample("t1", "1", "Morning")
		_ = s.PutTrigger(ctx, tr)
		tr.Name, tr.Enabled, tr.Schedule = "Evening", false, "weekdays 18:00"
		Expect(s.PutTrigger(ctx, tr)).To(Succeed())
		got, _ := s.GetTrigger(ctx, "t1")
		Expect(same(got, tr)).To(BeTrue(), "got %+v, want %+v", got, tr)
		l, _ := s.ListTriggers(ctx, "1")
		Expect(l).To(HaveLen(1), "triggers after a replace")
	})
	It("lists triggers by owner, sorted by name and ID", func() {
		s := newStore()
		for _, tr := range []triggers.Trigger{
			sample("c", "1", "Beta"), sample("a", "1", "Beta"), sample("b", "1", "Alpha"), sample("d", "2", "Alpha"),
		} {
			_ = s.PutTrigger(ctx, tr)
		}
		got, err := s.ListTriggers(ctx, "1")
		Expect(err).NotTo(HaveOccurred())
		var ids string
		for _, tr := range got {
			ids += tr.ID
		}
		Expect(ids).To(Equal("bac"))
		l, _ := s.ListTriggers(ctx, "nobody")
		Expect(l).To(BeEmpty(), "triggers for nobody")
	})
	It("lists the enabled triggers of every owner, sorted by ID", func() {
		s := newStore()
		off := sample("b", "1", "Off")
		off.Enabled = false
		for _, tr := range []triggers.Trigger{sample("z", "2", "Z"), off, sample("a", "1", "A")} {
			_ = s.PutTrigger(ctx, tr)
		}
		got, err := s.EnabledTriggers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(2))
		Expect(got[0].ID).To(Equal("a"))
		Expect(got[1].ID).To(Equal("z"))
	})
	It("lists all triggers of every owner, disabled ones too, sorted by owner and name", func() {
		s := newStore()
		off := sample("b", "1", "Off")
		off.Enabled = false
		for _, tr := range []triggers.Trigger{sample("z", "2", "Z"), off, sample("a", "1", "A")} {
			_ = s.PutTrigger(ctx, tr)
		}
		got, err := s.AllTriggers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))
		Expect(got[0].ID).To(Equal("a"))
		Expect(got[1].ID).To(Equal("b"))
		Expect(got[2].ID).To(Equal("z"))
	})
	It("removes the runs and last due time of a deleted trigger", func() {
		s := newStore()
		_ = s.PutTrigger(ctx, sample("t1", "1", "A"))
		_ = s.PutTrigger(ctx, sample("t2", "1", "B"))
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r2", "t2", "k1", triggers.StatusDone))
		_ = s.SetLastDue(ctx, "t1", base)
		Expect(s.DeleteTrigger(ctx, "t1")).To(Succeed())
		_, err := s.GetTrigger(ctx, "t1")
		Expect(err).To(MatchError(triggers.ErrNotFound), "trigger still there")
		l, _ := s.ListRuns(ctx, "t1", 10)
		Expect(l).To(BeEmpty(), "runs of a deleted trigger")
		_, err = s.GetRun(ctx, "r1")
		Expect(err).To(MatchError(triggers.ErrNotFound), "run still there")
		d, _ := s.LastDue(ctx, "t1")
		Expect(d.IsZero()).To(BeTrue(), "last due %v after a delete", d)
		l, _ = s.ListRuns(ctx, "t2", 10)
		Expect(l).To(HaveLen(1), "the runs of another trigger changed")
		// The same key can be claimed again once the old run is gone.
		ok, _ := s.Claim(ctx, run("r3", "t1", "k1", triggers.StatusQueued))
		Expect(ok).To(BeTrue(), "the key of a deleted run is still taken")
	})
	It("stores a claim once per trigger and key", func() {
		s := newStore()
		r := run("r1", "t1", "2026-10-22T08:00:00Z", triggers.StatusQueued)
		r.File = "/users/1/Contracts/a.pdf"
		ok, err := s.Claim(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "first claim")
		again := run("r2", "t1", "2026-10-22T08:00:00Z", triggers.StatusQueued)
		ok, err = s.Claim(ctx, again)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "second claim")
		_, err = s.GetRun(ctx, "r2")
		Expect(err).To(MatchError(triggers.ErrNotFound), "the refused run was stored")
		ok, _ = s.Claim(ctx, run("r3", "t2", "2026-10-22T08:00:00Z", triggers.StatusQueued))
		Expect(ok).To(BeTrue(), "the same key of another trigger was refused")
		got, err := s.GetRun(ctx, "r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(sameRun(got, r)).To(BeTrue(), "GetRun = %+v; want %+v", got, r)
	})
	It("replaces a run state", func() {
		s := newStore()
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusQueued))
		r := run("r1", "t1", "k1", triggers.StatusFailed)
		r.Reason, r.ChatID = "took longer than 15m", "chat-1"
		r.Started, r.Ended = base.Add(time.Minute), base.Add(16*time.Minute)
		Expect(s.SetRunStatus(ctx, r)).To(Succeed())
		got, _ := s.GetRun(ctx, "r1")
		Expect(sameRun(got, r)).To(BeTrue(), "got %+v, want %+v", got, r)
		byChat, err := s.RunByChat(ctx, "chat-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(byChat.ID).To(Equal("r1"))
		_, err = s.RunByChat(ctx, "")
		Expect(err).To(MatchError(triggers.ErrNotFound), "RunByChat of an empty ID")
	})
	It("lists runs newest claim first, with a limit", func() {
		s := newStore()
		for i := 1; i <= 5; i++ {
			_, _ = s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", 6-i), triggers.StatusDone))
		}
		_, _ = s.Claim(ctx, run("other", "t2", "k", triggers.StatusDone))
		got, err := s.ListRuns(ctx, "t1", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(idsOf(got)).To(Equal("r5r4r3"))
		all, _ := s.ListRuns(ctx, "t1", 100)
		Expect(all).To(HaveLen(5))
	})
	It("lists every run for a limit of zero or less", func() {
		s := newStore()
		for i := 1; i <= 3; i++ {
			_, _ = s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", i), triggers.StatusDone))
		}
		for _, limit := range []int{0, -1} {
			all, err := s.ListRuns(ctx, "t1", limit)
			must(err)
			Expect(idsOf(all)).To(Equal("r3r2r1"), "ListRuns with limit %d", limit)
		}
	})
	It("refuses a claim with the ID of another run and stores nothing", func() {
		s := newStore()
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusQueued))
		ok, err := s.Claim(ctx, run("r1", "t1", "k2", triggers.StatusDone))
		Expect(err).To(HaveOccurred(), "a reused run ID must be an error")
		Expect(ok).To(BeFalse())
		ok, err = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		Expect(err).NotTo(HaveOccurred(), "the same run again")
		Expect(ok).To(BeFalse())
		_, _ = s.Claim(ctx, run("r5", "t1", "k5", triggers.StatusQueued))
		ok, err = s.Claim(ctx, run("r1", "t1", "k5", triggers.StatusDone))
		Expect(err).NotTo(HaveOccurred(), "an ID of one run with the key of another")
		Expect(ok).To(BeFalse())
		g, _ := s.GetRun(ctx, "r1")
		Expect(g.DueKey).To(Equal("k1"), "the run with the ID of a claim refused for its key changed: %+v", g)
		g, err = s.GetRun(ctx, "r1")
		must(err)
		Expect(g.DueKey).To(Equal("k1"), "the run with the reused ID changed: %+v", g)
		Expect(g.Status).To(Equal(triggers.StatusQueued), "the run with the reused ID changed: %+v", g)
		l, _ := s.ListRuns(ctx, "t1", 10)
		Expect(l).To(HaveLen(2), "runs after the refused claims, want 2 (r1 and r5)")
		ok, err = s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued))
		Expect(err).NotTo(HaveOccurred(), "the key of the refused claim is taken")
		Expect(ok).To(BeTrue(), "the key of the refused claim is taken")
	})
	It("lists running and queued runs oldest claim first", func() {
		s := newStore()
		for i, st := range []triggers.Status{triggers.StatusQueued, triggers.StatusRunning, triggers.StatusDone, triggers.StatusQueued, triggers.StatusRunning, triggers.StatusSkipped} {
			_, _ = s.Claim(ctx, run(fmt.Sprintf("r%d", i), fmt.Sprintf("t%d", i%2), fmt.Sprintf("k%d", i), st))
		}
		q, _ := s.QueuedRuns(ctx)
		Expect(q).To(HaveLen(2))
		Expect(q[0].ID).To(Equal("r0"))
		Expect(q[1].ID).To(Equal("r3"))
		r, _ := s.RunningRuns(ctx)
		Expect(r).To(HaveLen(2))
		Expect(r[0].ID).To(Equal("r1"))
		Expect(r[1].ID).To(Equal("r4"))
	})
	It("counts runs by due time and does not count skipped runs", func() {
		s := newStore()
		mk := func(id, key string, due time.Time, st triggers.Status) {
			r := run(id, "t1", key, st)
			r.Due = due
			_, _ = s.Claim(ctx, r)
		}
		mk("a", "1", base.Add(-time.Hour), triggers.StatusDone)
		mk("b", "2", base, triggers.StatusDone)
		mk("c", "3", base.Add(time.Hour), triggers.StatusFailed)
		mk("d", "4", base.Add(2*time.Hour), triggers.StatusQueued)
		mk("e", "5", base.Add(3*time.Hour), triggers.StatusSkipped)
		_, _ = s.Claim(ctx, run("f", "t2", "6", triggers.StatusDone))
		n, err := s.CountRunsSince(ctx, "t1", base)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		n, _ = s.CountRunsSince(ctx, "t1", base.Add(time.Hour+res))
		Expect(n).To(Equal(1), "CountRunsSince after the failed run")
	})
	It("starts with an unset cursor", func() {
		s := newStore()
		rev, ok, err := s.Cursor(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "Cursor must start unset")
		Expect(rev).To(BeZero())
		_ = s.SetCursor(ctx, 0)
		_, ok, _ = s.Cursor(ctx)
		Expect(ok).To(BeTrue(), "a cursor of 0 reads as unset")
		_ = s.SetCursor(ctx, 41)
		_ = s.SetCursor(ctx, 42)
		rev, ok, _ = s.Cursor(ctx)
		Expect(ok).To(BeTrue())
		Expect(rev).To(BeEquivalentTo(42))
	})
	It("keeps the last due time per trigger", func() {
		s := newStore()
		d, err := s.LastDue(ctx, "t1")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.IsZero()).To(BeTrue(), "LastDue = %v; want zero", d)
		due := time.Date(2026, 10, 22, 6, 0, 0, 123456789, time.UTC)
		_ = s.SetLastDue(ctx, "t1", due)
		_ = s.SetLastDue(ctx, "t2", due.Add(time.Hour))
		d, _ = s.LastDue(ctx, "t1")
		Expect(d.Equal(due)).To(BeTrue(), "LastDue = %v, want %v", d, due)
		_ = s.SetLastDue(ctx, "t1", due.Add(24*time.Hour))
		d, _ = s.LastDue(ctx, "t1")
		Expect(d.Equal(due.Add(24*time.Hour))).To(BeTrue(), "LastDue after a replace = %v", d)
	})
	It("lets concurrent claims of one key win once", func() {
		s := newStore()
		const workers = 16
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				ok, err := s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", "same", triggers.StatusQueued))
				Expect(err).NotTo(HaveOccurred(), "Claim")
				if ok {
					wins.Add(1)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		Expect(wins.Load()).To(Equal(int32(1)), "claims won")
		l, _ := s.ListRuns(ctx, "t1", 100)
		Expect(l).To(HaveLen(1), "runs stored")
	})
	It("lets concurrent claims of different keys all win", func() {
		s := newStore()
		const workers = 16
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				ok, err := s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", i), triggers.StatusQueued))
				Expect(err).NotTo(HaveOccurred(), "Claim")
				if ok {
					wins.Add(1)
				}
			}(i)
		}
		wg.Wait()
		Expect(wins.Load()).To(Equal(int32(workers)), "claims won")
	})
	It("ignores runs without a chat in RunByChat", func() {
		s := newStore()
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued))
		_, err := s.RunByChat(ctx, "")
		Expect(err).To(MatchError(triggers.ErrNotFound), "RunByChat of an empty ID with chatless runs")
		r3 := run("r3", "t1", "k3", triggers.StatusRunning)
		_, _ = s.Claim(ctx, r3)
		r3.ChatID = "chat-3"
		must(s.SetRunStatus(ctx, r3))
		got, err := s.RunByChat(ctx, "chat-3")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal("r3"))
		r3.ChatID = ""
		must(s.SetRunStatus(ctx, r3))
		_, err = s.RunByChat(ctx, "chat-3")
		Expect(err).To(MatchError(triggers.ErrNotFound), "RunByChat after the chat was cleared")
	})
	It("replaces only the status, reason, chat and times on a status change", func() {
		s := newStore()
		orig := run("r1", "t1", "k1", triggers.StatusQueued)
		orig.File = "/users/1/Contracts/a.pdf"
		ok, err := s.Claim(ctx, orig)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		r := orig
		r.TriggerID, r.DueKey, r.Due, r.File = "t9", "other", base.Add(5*time.Hour), "/users/1/Contracts/b.pdf"
		r.Status, r.Reason, r.ChatID = triggers.StatusFailed, "took longer than 15m", "chat-1"
		r.Started, r.Ended = base.Add(time.Minute), base.Add(16*time.Minute)
		must(s.SetRunStatus(ctx, r))
		got, err := s.GetRun(ctx, "r1")
		must(err)
		want := orig
		want.Status, want.Reason, want.ChatID = r.Status, r.Reason, r.ChatID
		want.Started, want.Ended = r.Started, r.Ended
		Expect(sameRun(got, want)).To(BeTrue(), "got %+v, want %+v", got, want)
		ok, _ = s.Claim(ctx, run("r2", "t1", "k1", triggers.StatusQueued))
		Expect(ok).To(BeFalse(), "the claimed key was freed by a status change")
		ok, _ = s.Claim(ctx, run("r3", "t9", "other", triggers.StatusQueued))
		Expect(ok).To(BeTrue(), "the key written into the run by a status change was taken")
	})
	It("keeps the claim position on a status change", func() {
		s := newStore()
		for _, id := range []string{"a", "b", "c"} {
			_, _ = s.Claim(ctx, run(id, "t1", "k-"+id, triggers.StatusQueued))
		}
		must(s.SetRunStatus(ctx, run("b", "t1", "k-b", triggers.StatusRunning)))
		q, err := s.QueuedRuns(ctx)
		must(err)
		Expect(idsOf(q)).To(Equal("ac"), "queued after b started")
		rn, err := s.RunningRuns(ctx)
		must(err)
		Expect(idsOf(rn)).To(Equal("b"), "running")
		must(s.SetRunStatus(ctx, run("b", "t1", "k-b", triggers.StatusQueued)))
		q, err = s.QueuedRuns(ctx)
		must(err)
		Expect(idsOf(q)).To(Equal("abc"), "queued after b was queued again")
		must(s.SetRunStatus(ctx, run("b", "t1", "k-b", triggers.StatusDone)))
		all, err := s.ListRuns(ctx, "t1", 10)
		must(err)
		Expect(idsOf(all)).To(Equal("cba"), "ListRuns after b is done")
	})
	It("lists by claim order, not by ID, key or due order", func() {
		s := newStore()
		mk := func(id, key string, due time.Duration, st triggers.Status) {
			r := run(id, "t1", key, st)
			r.Due = base.Add(due)
			_, _ = s.Claim(ctx, r)
		}
		// Claim order z, m, a, q. ID order a, m, q, z. Key and due order q, m, a, z.
		mk("z", "k4", 4*time.Hour, triggers.StatusQueued)
		mk("m", "k2", 2*time.Hour, triggers.StatusRunning)
		mk("a", "k3", 3*time.Hour, triggers.StatusQueued)
		mk("q", "k1", time.Hour, triggers.StatusRunning)
		all, err := s.ListRuns(ctx, "t1", 10)
		must(err)
		Expect(idsOf(all)).To(Equal("qamz"), "ListRuns (newest claim first)")
		q, err := s.QueuedRuns(ctx)
		must(err)
		Expect(idsOf(q)).To(Equal("za"), "QueuedRuns (claim order)")
		rn, err := s.RunningRuns(ctx)
		must(err)
		Expect(idsOf(rn)).To(Equal("mq"), "RunningRuns (claim order)")
	})
	It("keeps the runs and last due time when a trigger is put again", func() {
		s := newStore()
		tr := sample("t1", "1", "Morning")
		must(s.PutTrigger(ctx, tr))
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued))
		must(s.SetLastDue(ctx, "t1", base.Add(time.Hour)))
		tr.Name = "Evening"
		must(s.PutTrigger(ctx, tr))
		l, err := s.ListRuns(ctx, "t1", 10)
		must(err)
		Expect(l).To(HaveLen(2), "runs after a replace")
		d, _ := s.LastDue(ctx, "t1")
		Expect(d.Equal(base.Add(time.Hour))).To(BeTrue(), "LastDue after a replace = %v", d)
	})
	It("keeps the instant, zone and nanoseconds of times", func() {
		s := newStore()
		zone := time.FixedZone("x", 5*3600+1800)
		due := time.Date(2026, 10, 22, 13, 45, 30, 123456789, zone).Truncate(res)
		started := due.Add(987654321).Truncate(res)
		ended := started.Add(time.Second + res)
		tr := sample("t1", "1", "Morning")
		tr.Since = time.Date(2026, 10, 22, 8, 0, 0, 500000001, zone).Truncate(res).Add(res)
		must(s.PutTrigger(ctx, tr))
		got, err := s.GetTrigger(ctx, "t1")
		must(err)
		Expect(got.Since.Equal(tr.Since)).To(BeTrue(), "Since = %v, want %v", got.Since, tr.Since)
		r := run("r1", "t1", "k1", triggers.StatusQueued)
		r.Due = due
		ok, err := s.Claim(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		r.Status, r.Started, r.Ended = triggers.StatusDone, started, ended
		must(s.SetRunStatus(ctx, r))
		g, err := s.GetRun(ctx, "r1")
		must(err)
		Expect(sameRun(g, r)).To(BeTrue(), "GetRun = %+v, want %+v", g, r)
		must(s.SetLastDue(ctx, "t1", started))
		d, _ := s.LastDue(ctx, "t1")
		Expect(d.Equal(started)).To(BeTrue(), "LastDue = %v, want %v", d, started)
		for _, c := range []struct {
			name  string
			since time.Time
			want  int
		}{
			{"the same instant", due, 1},
			{"the same instant in UTC", due.UTC(), 1},
			{"one step before", due.Add(-res), 1},
			{"one step after", due.Add(res), 0},
		} {
			n, err := s.CountRunsSince(ctx, "t1", c.since)
			Expect(err).NotTo(HaveOccurred(), "CountRunsSince from %s", c.name)
			Expect(n).To(Equal(c.want), "CountRunsSince from %s", c.name)
		}
	})
	It("counts a running run and leaves no runs behind for a deleted trigger", func() {
		s := newStore()
		_ = s.PutTrigger(ctx, sample("t1", "1", "A"))
		_ = s.PutTrigger(ctx, sample("t2", "1", "B"))
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusRunning))
		chatDel := run("r3", "t1", "k3", triggers.StatusRunning)
		chatDel.ChatID = "chat-del"
		_, _ = s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued))
		_, _ = s.Claim(ctx, chatDel)
		_, _ = s.Claim(ctx, run("r4", "t1", "k4", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r5", "t2", "k5", triggers.StatusQueued))
		other := run("r6", "t2", "k6", triggers.StatusRunning)
		other.ChatID = "chat-t2"
		_, _ = s.Claim(ctx, other)
		must(s.SetCursor(ctx, 7))
		n, err := s.CountRunsSince(ctx, "t1", base)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(4), "CountRunsSince before the delete")
		must(s.DeleteTrigger(ctx, "t1"))
		q, err := s.QueuedRuns(ctx)
		must(err)
		Expect(idsOf(q)).To(Equal("r5"), "QueuedRuns after the delete")
		rn, err := s.RunningRuns(ctx)
		must(err)
		Expect(idsOf(rn)).To(Equal("r6"), "RunningRuns after the delete")
		_, err = s.RunByChat(ctx, "chat-del")
		Expect(err).To(MatchError(triggers.ErrNotFound), "RunByChat of a deleted run")
		got, err := s.RunByChat(ctx, "chat-t2")
		Expect(err).NotTo(HaveOccurred(), "RunByChat of another trigger")
		Expect(got.ID).To(Equal("r6"))
		n, err = s.CountRunsSince(ctx, "t1", base)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero(), "CountRunsSince after the delete")
		rev, ok, _ := s.Cursor(ctx)
		Expect(ok).To(BeTrue())
		Expect(rev).To(BeEquivalentTo(7), "Cursor after the delete")
	})
	pruneContract(newStore)
}

// must fails the spec on an error.
func must(err error) {
	GinkgoHelper()
	Expect(err).NotTo(HaveOccurred())
}

// idsOf joins the IDs of runs in their order.
func idsOf(rs []triggers.Run) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(r.ID)
	}
	return b.String()
}

// seedRuns stores one running run, one queued run and ten finished runs for
// trigger t1 in claim order, due one hour apart from base+1h to base+10h, and
// two finished runs for t2.
func seedRuns(s triggers.Store) {
	ctx := context.Background()
	for _, tr := range []string{"t1", "t2"} {
		Expect(s.PutTrigger(ctx, sample(tr, "1", tr))).To(Succeed())
	}
	old := run("running", "t1", "running", triggers.StatusRunning)
	old.Due = base.Add(-1000 * time.Hour)
	queued := run("queued", "t1", "queued", triggers.StatusQueued)
	queued.Due = base.Add(-999 * time.Hour)
	for _, r := range []triggers.Run{old, queued} {
		ok, err := s.Claim(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	}
	for i := 1; i <= 10; i++ {
		r := run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", i), triggers.StatusDone)
		r.Due = base.Add(time.Duration(i) * time.Hour)
		ok, err := s.Claim(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	}
	for i := 1; i <= 2; i++ {
		r := run(fmt.Sprintf("o%d", i), "t2", fmt.Sprintf("k%d", i), triggers.StatusFailed)
		r.Due = base.Add(-time.Duration(i) * time.Hour)
		ok, err := s.Claim(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	}
}

// runIDs lists the IDs of the runs of a trigger, oldest claim first.
func runIDs(s triggers.Store, trigger string) []string {
	runs, err := s.ListRuns(context.Background(), trigger, 0)
	Expect(err).NotTo(HaveOccurred())
	ids := make([]string, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		ids = append(ids, runs[i].ID)
	}
	return ids
}

// pruneContract registers the specs for Store.PruneRuns: the count limit, the
// protected window, the age limit, that open runs and other triggers stay, and
// that a removed claim can be made again.
func pruneContract(newStore NewStore) {
	ctx := context.Background()
	open := []string{"running", "queued"}
	ids := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf("r%d", i))
		}
		return out
	}
	DescribeTable("pruning runs",
		func(p triggers.Retention, removed int, kept []string) {
			s := newStore()
			seedRuns(s)
			n, err := s.PruneRuns(ctx, "t1", p)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(removed))
			Expect(runIDs(s, "t1")).To(Equal(append(append([]string{}, open...), kept...)))
			Expect(runIDs(s, "t2")).To(Equal([]string{"o1", "o2"}), "another trigger is not touched")
			queued, err := s.QueuedRuns(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(queued).To(HaveLen(1), "a queued run stays")
		},
		Entry("the newest Keep finished runs stay", triggers.Retention{Keep: 3}, 7, ids(8, 10)),
		Entry("runs due in the protected window stay beyond Keep", triggers.Retention{Keep: 3, Protect: base.Add(5 * time.Hour)}, 4, ids(5, 10)),
		Entry("runs older than Expire go whatever Keep says", triggers.Retention{Keep: 10, Protect: base, Expire: base.Add(4 * time.Hour)}, 3, ids(4, 10)),
		Entry("Expire goes over the protected window", triggers.Retention{Keep: 3, Protect: base, Expire: base.Add(6 * time.Hour)}, 5, ids(6, 10)),
		Entry("no limit removes nothing", triggers.Retention{}, 0, ids(1, 10)),
		Entry("Keep above the number of runs removes nothing", triggers.Retention{Keep: 50}, 0, ids(1, 10)),
	)

	It("frees the claim of a pruned run and keeps the claim of a kept run", func() {
		s := newStore()
		seedRuns(s)
		_, err := s.PruneRuns(ctx, "t1", triggers.Retention{Keep: 3})
		Expect(err).NotTo(HaveOccurred())
		again := run("again", "t1", "k1", triggers.StatusQueued)
		ok, err := s.Claim(ctx, again)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "the claim of a removed run is free again")
		ok, err = s.Claim(ctx, run("kept", "t1", "k10", triggers.StatusQueued))
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "the claim of a kept run is still taken")
		n, err := s.PruneRuns(ctx, "nope", triggers.Retention{Keep: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero(), "an unknown trigger has nothing to prune")
	})
}
