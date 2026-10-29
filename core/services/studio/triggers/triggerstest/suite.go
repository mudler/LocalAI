package triggerstest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// NewStore returns an empty Store for one test. The test closes nothing: the
// function registers its own cleanup with t.Cleanup.
type NewStore func(t *testing.T) triggers.Store

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
	}
}

func run(id, trigger, key string, status triggers.Status) triggers.Run {
	return triggers.Run{ID: id, TriggerID: trigger, DueKey: key, Due: base, Status: status}
}

func same(a, b triggers.Trigger) bool {
	return a.ID == b.ID && a.Owner == b.Owner && a.Name == b.Name && a.Prompt == b.Prompt &&
		a.Enabled == b.Enabled && a.Kind == b.Kind && a.Schedule == b.Schedule && a.TZ == b.TZ &&
		a.Folder == b.Folder && a.Quiet == b.Quiet && a.DailyCap == b.DailyCap && a.Since.Equal(b.Since) &&
		a.AgentID == b.AgentID && a.SourceChatID == b.SourceChatID && a.Model == b.Model && a.Proposed == b.Proposed
}

func sameRun(a, b triggers.Run) bool {
	return a.ID == b.ID && a.TriggerID == b.TriggerID && a.DueKey == b.DueKey && a.Due.Equal(b.Due) &&
		a.Status == b.Status && a.Reason == b.Reason && a.ChatID == b.ChatID && a.File == b.File &&
		a.Started.Equal(b.Started) && a.Ended.Equal(b.Ended)
}

// RunStoreSuite checks a triggers.Store: triggers, claims, run states,
// listings in their order, counts, the cursor, the last due times, and claims
// from concurrent goroutines.
func RunStoreSuite(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	t.Run("a trigger is stored with every field", func(t *testing.T) {
		s := newStore(t)
		for _, tr := range []triggers.Trigger{sample("t1", "1", "Morning"), fileTrigger("t2", "1")} {
			if err := s.PutTrigger(ctx, tr); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetTrigger(ctx, tr.ID)
			if err != nil || !same(got, tr) {
				t.Fatalf("GetTrigger(%s) = %+v, %v; want %+v", tr.ID, got, err, tr)
			}
		}
	})
	t.Run("an unknown trigger is not found", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetTrigger(ctx, "nope"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("GetTrigger: %v, want ErrNotFound", err)
		}
		if _, err := s.GetRun(ctx, "nope"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("GetRun: %v, want ErrNotFound", err)
		}
		if _, err := s.RunByChat(ctx, "nope"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("RunByChat: %v, want ErrNotFound", err)
		}
		if err := s.SetRunStatus(ctx, run("nope", "t1", "k", triggers.StatusDone)); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("SetRunStatus: %v, want ErrNotFound", err)
		}
		if err := s.DeleteTrigger(ctx, "nope"); err != nil {
			t.Fatalf("DeleteTrigger of an unknown trigger: %v", err)
		}
	})
	t.Run("putting a trigger again replaces it", func(t *testing.T) {
		s := newStore(t)
		tr := sample("t1", "1", "Morning")
		_ = s.PutTrigger(ctx, tr)
		tr.Name, tr.Enabled, tr.Schedule = "Evening", false, "weekdays 18:00"
		if err := s.PutTrigger(ctx, tr); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetTrigger(ctx, "t1")
		if !same(got, tr) {
			t.Fatalf("got %+v, want %+v", got, tr)
		}
		if l, _ := s.ListTriggers(ctx, "1"); len(l) != 1 {
			t.Fatalf("%d triggers after a replace", len(l))
		}
	})
	t.Run("triggers are listed by owner, sorted by name and ID", func(t *testing.T) {
		s := newStore(t)
		for _, tr := range []triggers.Trigger{
			sample("c", "1", "Beta"), sample("a", "1", "Beta"), sample("b", "1", "Alpha"), sample("d", "2", "Alpha"),
		} {
			_ = s.PutTrigger(ctx, tr)
		}
		got, err := s.ListTriggers(ctx, "1")
		if err != nil {
			t.Fatal(err)
		}
		var ids string
		for _, tr := range got {
			ids += tr.ID
		}
		if ids != "bac" {
			t.Fatalf("order %q, want bac", ids)
		}
		if l, _ := s.ListTriggers(ctx, "nobody"); len(l) != 0 {
			t.Fatalf("%d triggers for nobody", len(l))
		}
	})
	t.Run("enabled triggers of every owner, sorted by ID", func(t *testing.T) {
		s := newStore(t)
		off := sample("b", "1", "Off")
		off.Enabled = false
		for _, tr := range []triggers.Trigger{sample("z", "2", "Z"), off, sample("a", "1", "A")} {
			_ = s.PutTrigger(ctx, tr)
		}
		got, err := s.EnabledTriggers(ctx)
		if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "z" {
			t.Fatalf("EnabledTriggers = %+v, %v", got, err)
		}
	})
	t.Run("all triggers of every owner, disabled ones too, sorted by owner and name", func(t *testing.T) {
		s := newStore(t)
		off := sample("b", "1", "Off")
		off.Enabled = false
		for _, tr := range []triggers.Trigger{sample("z", "2", "Z"), off, sample("a", "1", "A")} {
			_ = s.PutTrigger(ctx, tr)
		}
		got, err := s.AllTriggers(ctx)
		if err != nil || len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "z" {
			t.Fatalf("AllTriggers = %+v, %v", got, err)
		}
	})
	t.Run("deleting a trigger removes its runs and last due time", func(t *testing.T) {
		s := newStore(t)
		_ = s.PutTrigger(ctx, sample("t1", "1", "A"))
		_ = s.PutTrigger(ctx, sample("t2", "1", "B"))
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r2", "t2", "k1", triggers.StatusDone))
		_ = s.SetLastDue(ctx, "t1", base)
		if err := s.DeleteTrigger(ctx, "t1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetTrigger(ctx, "t1"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("trigger still there: %v", err)
		}
		if l, _ := s.ListRuns(ctx, "t1", 10); len(l) != 0 {
			t.Fatalf("%d runs of a deleted trigger", len(l))
		}
		if _, err := s.GetRun(ctx, "r1"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("run still there: %v", err)
		}
		if d, _ := s.LastDue(ctx, "t1"); !d.IsZero() {
			t.Fatalf("last due %v after a delete", d)
		}
		if l, _ := s.ListRuns(ctx, "t2", 10); len(l) != 1 {
			t.Fatalf("the runs of another trigger changed: %d", len(l))
		}
		// The same key can be claimed again once the old run is gone.
		if ok, _ := s.Claim(ctx, run("r3", "t1", "k1", triggers.StatusQueued)); !ok {
			t.Fatal("the key of a deleted run is still taken")
		}
	})
	t.Run("a claim is stored once per trigger and key", func(t *testing.T) {
		s := newStore(t)
		r := run("r1", "t1", "2026-10-22T08:00:00Z", triggers.StatusQueued)
		r.File = "/users/1/Contracts/a.pdf"
		ok, err := s.Claim(ctx, r)
		if err != nil || !ok {
			t.Fatalf("first claim = %v, %v", ok, err)
		}
		again := run("r2", "t1", "2026-10-22T08:00:00Z", triggers.StatusQueued)
		if ok, err := s.Claim(ctx, again); err != nil || ok {
			t.Fatalf("second claim = %v, %v; want false", ok, err)
		}
		if _, err := s.GetRun(ctx, "r2"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("the refused run was stored: %v", err)
		}
		if ok, _ := s.Claim(ctx, run("r3", "t2", "2026-10-22T08:00:00Z", triggers.StatusQueued)); !ok {
			t.Fatal("the same key of another trigger was refused")
		}
		got, err := s.GetRun(ctx, "r1")
		if err != nil || !sameRun(got, r) {
			t.Fatalf("GetRun = %+v, %v; want %+v", got, err, r)
		}
	})
	t.Run("a run state is replaced", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusQueued))
		r := run("r1", "t1", "k1", triggers.StatusFailed)
		r.Reason, r.ChatID = "took longer than 15m", "chat-1"
		r.Started, r.Ended = base.Add(time.Minute), base.Add(16*time.Minute)
		if err := s.SetRunStatus(ctx, r); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetRun(ctx, "r1")
		if !sameRun(got, r) {
			t.Fatalf("got %+v, want %+v", got, r)
		}
		byChat, err := s.RunByChat(ctx, "chat-1")
		if err != nil || byChat.ID != "r1" {
			t.Fatalf("RunByChat = %+v, %v", byChat, err)
		}
		if _, err := s.RunByChat(ctx, ""); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("RunByChat of an empty ID: %v", err)
		}
	})
	t.Run("runs are listed newest claim first, with a limit", func(t *testing.T) {
		s := newStore(t)
		for i := 1; i <= 5; i++ {
			_, _ = s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", 6-i), triggers.StatusDone))
		}
		_, _ = s.Claim(ctx, run("other", "t2", "k", triggers.StatusDone))
		got, err := s.ListRuns(ctx, "t1", 3)
		if err != nil || len(got) != 3 || got[0].ID != "r5" || got[1].ID != "r4" || got[2].ID != "r3" {
			t.Fatalf("ListRuns = %+v, %v", got, err)
		}
		if all, _ := s.ListRuns(ctx, "t1", 100); len(all) != 5 {
			t.Fatalf("%d runs, want 5", len(all))
		}
	})
	t.Run("a limit of zero or less lists every run", func(t *testing.T) {
		s := newStore(t)
		for i := 1; i <= 3; i++ {
			_, _ = s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", i), triggers.StatusDone))
		}
		for _, limit := range []int{0, -1} {
			all, err := s.ListRuns(ctx, "t1", limit)
			must(t, err)
			if got := idsOf(all); got != "r3r2r1" {
				t.Fatalf("ListRuns with limit %d = %q, want r3r2r1", limit, got)
			}
		}
	})
	t.Run("a claim with the ID of another run is an error and stores nothing", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusQueued))
		if ok, err := s.Claim(ctx, run("r1", "t1", "k2", triggers.StatusDone)); err == nil || ok {
			t.Fatalf("a reused run ID = %v, %v; want false and an error", ok, err)
		}
		if ok, err := s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone)); err != nil || ok {
			t.Fatalf("the same run again = %v, %v; want false and no error", ok, err)
		}
		_, _ = s.Claim(ctx, run("r5", "t1", "k5", triggers.StatusQueued))
		if ok, err := s.Claim(ctx, run("r1", "t1", "k5", triggers.StatusDone)); err != nil || ok {
			t.Fatalf("an ID of one run with the key of another = %v, %v; want false and no error", ok, err)
		}
		if g, _ := s.GetRun(ctx, "r1"); g.DueKey != "k1" {
			t.Fatalf("the run with the ID of a claim refused for its key changed: %+v", g)
		}
		g, err := s.GetRun(ctx, "r1")
		must(t, err)
		if g.DueKey != "k1" || g.Status != triggers.StatusQueued {
			t.Fatalf("the run with the reused ID changed: %+v", g)
		}
		if l, _ := s.ListRuns(ctx, "t1", 10); len(l) != 2 {
			t.Fatalf("%d runs after the refused claims, want 2 (r1 and r5)", len(l))
		}
		if ok, err := s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued)); err != nil || !ok {
			t.Fatalf("the key of the refused claim is taken: %v, %v", ok, err)
		}
	})
	t.Run("running and queued runs are listed oldest claim first", func(t *testing.T) {
		s := newStore(t)
		for i, st := range []triggers.Status{triggers.StatusQueued, triggers.StatusRunning, triggers.StatusDone, triggers.StatusQueued, triggers.StatusRunning, triggers.StatusSkipped} {
			_, _ = s.Claim(ctx, run(fmt.Sprintf("r%d", i), fmt.Sprintf("t%d", i%2), fmt.Sprintf("k%d", i), st))
		}
		q, _ := s.QueuedRuns(ctx)
		if len(q) != 2 || q[0].ID != "r0" || q[1].ID != "r3" {
			t.Fatalf("QueuedRuns = %+v", q)
		}
		r, _ := s.RunningRuns(ctx)
		if len(r) != 2 || r[0].ID != "r1" || r[1].ID != "r4" {
			t.Fatalf("RunningRuns = %+v", r)
		}
	})
	t.Run("runs are counted by due time and skipped runs do not count", func(t *testing.T) {
		s := newStore(t)
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
		if err != nil || n != 3 {
			t.Fatalf("CountRunsSince = %d, %v; want 3", n, err)
		}
		if n, _ := s.CountRunsSince(ctx, "t1", base.Add(time.Hour+time.Nanosecond)); n != 1 {
			t.Fatalf("CountRunsSince after the failed run = %d, want 1", n)
		}
	})
	t.Run("the cursor starts unset", func(t *testing.T) {
		s := newStore(t)
		if rev, ok, err := s.Cursor(ctx); err != nil || ok || rev != 0 {
			t.Fatalf("Cursor = %d, %v, %v; want unset", rev, ok, err)
		}
		_ = s.SetCursor(ctx, 0)
		if _, ok, _ := s.Cursor(ctx); !ok {
			t.Fatal("a cursor of 0 reads as unset")
		}
		_ = s.SetCursor(ctx, 41)
		_ = s.SetCursor(ctx, 42)
		if rev, ok, _ := s.Cursor(ctx); !ok || rev != 42 {
			t.Fatalf("Cursor = %d, %v; want 42", rev, ok)
		}
	})
	t.Run("the last due time is kept per trigger", func(t *testing.T) {
		s := newStore(t)
		if d, err := s.LastDue(ctx, "t1"); err != nil || !d.IsZero() {
			t.Fatalf("LastDue = %v, %v; want zero", d, err)
		}
		due := time.Date(2026, 10, 22, 6, 0, 0, 123456789, time.UTC)
		_ = s.SetLastDue(ctx, "t1", due)
		_ = s.SetLastDue(ctx, "t2", due.Add(time.Hour))
		if d, _ := s.LastDue(ctx, "t1"); !d.Equal(due) {
			t.Fatalf("LastDue = %v, want %v", d, due)
		}
		_ = s.SetLastDue(ctx, "t1", due.Add(24*time.Hour))
		if d, _ := s.LastDue(ctx, "t1"); !d.Equal(due.Add(24 * time.Hour)) {
			t.Fatalf("LastDue after a replace = %v", d)
		}
	})
	t.Run("concurrent claims of one key win once", func(t *testing.T) {
		s := newStore(t)
		const workers = 16
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				ok, err := s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", "same", triggers.StatusQueued))
				if err != nil {
					t.Errorf("Claim: %v", err)
				}
				if ok {
					wins.Add(1)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%d claims won, want 1", wins.Load())
		}
		if l, _ := s.ListRuns(ctx, "t1", 100); len(l) != 1 {
			t.Fatalf("%d runs stored, want 1", len(l))
		}
	})
	t.Run("concurrent claims of different keys all win", func(t *testing.T) {
		s := newStore(t)
		const workers = 16
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ok, err := s.Claim(ctx, run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", i), triggers.StatusQueued))
				if err != nil {
					t.Errorf("Claim: %v", err)
				}
				if ok {
					wins.Add(1)
				}
			}(i)
		}
		wg.Wait()
		if wins.Load() != workers {
			t.Fatalf("%d claims won, want %d", wins.Load(), workers)
		}
	})
	t.Run("RunByChat ignores runs without a chat", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued))
		if _, err := s.RunByChat(ctx, ""); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("RunByChat of an empty ID with chatless runs: %v, want ErrNotFound", err)
		}
		r3 := run("r3", "t1", "k3", triggers.StatusRunning)
		_, _ = s.Claim(ctx, r3)
		r3.ChatID = "chat-3"
		must(t, s.SetRunStatus(ctx, r3))
		if got, err := s.RunByChat(ctx, "chat-3"); err != nil || got.ID != "r3" {
			t.Fatalf("RunByChat = %+v, %v; want r3", got, err)
		}
		r3.ChatID = ""
		must(t, s.SetRunStatus(ctx, r3))
		if _, err := s.RunByChat(ctx, "chat-3"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("RunByChat after the chat was cleared: %v, want ErrNotFound", err)
		}
	})
	t.Run("a status change replaces only the status, reason, chat and times", func(t *testing.T) {
		s := newStore(t)
		orig := run("r1", "t1", "k1", triggers.StatusQueued)
		orig.File = "/users/1/Contracts/a.pdf"
		if ok, err := s.Claim(ctx, orig); err != nil || !ok {
			t.Fatalf("Claim = %v, %v", ok, err)
		}
		r := orig
		r.TriggerID, r.DueKey, r.Due, r.File = "t9", "other", base.Add(5*time.Hour), "/users/1/Contracts/b.pdf"
		r.Status, r.Reason, r.ChatID = triggers.StatusFailed, "took longer than 15m", "chat-1"
		r.Started, r.Ended = base.Add(time.Minute), base.Add(16*time.Minute)
		must(t, s.SetRunStatus(ctx, r))
		got, err := s.GetRun(ctx, "r1")
		must(t, err)
		want := orig
		want.Status, want.Reason, want.ChatID = r.Status, r.Reason, r.ChatID
		want.Started, want.Ended = r.Started, r.Ended
		if !sameRun(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if ok, _ := s.Claim(ctx, run("r2", "t1", "k1", triggers.StatusQueued)); ok {
			t.Fatal("the claimed key was freed by a status change")
		}
		if ok, _ := s.Claim(ctx, run("r3", "t9", "other", triggers.StatusQueued)); !ok {
			t.Fatal("the key written into the run by a status change was taken")
		}
	})
	t.Run("a status change keeps the claim position", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"a", "b", "c"} {
			_, _ = s.Claim(ctx, run(id, "t1", "k-"+id, triggers.StatusQueued))
		}
		must(t, s.SetRunStatus(ctx, run("b", "t1", "k-b", triggers.StatusRunning)))
		q, err := s.QueuedRuns(ctx)
		must(t, err)
		if got := idsOf(q); got != "ac" {
			t.Fatalf("queued = %q after b started, want ac", got)
		}
		rn, err := s.RunningRuns(ctx)
		must(t, err)
		if got := idsOf(rn); got != "b" {
			t.Fatalf("running = %q, want b", got)
		}
		must(t, s.SetRunStatus(ctx, run("b", "t1", "k-b", triggers.StatusQueued)))
		q, err = s.QueuedRuns(ctx)
		must(t, err)
		if got := idsOf(q); got != "abc" {
			t.Fatalf("queued = %q after b was queued again, want abc", got)
		}
		must(t, s.SetRunStatus(ctx, run("b", "t1", "k-b", triggers.StatusDone)))
		all, err := s.ListRuns(ctx, "t1", 10)
		must(t, err)
		if got := idsOf(all); got != "cba" {
			t.Fatalf("ListRuns = %q after b is done, want cba", got)
		}
	})
	t.Run("lists follow the claim order, not the ID, key or due order", func(t *testing.T) {
		s := newStore(t)
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
		must(t, err)
		if got := idsOf(all); got != "qamz" {
			t.Fatalf("ListRuns = %q, want qamz (newest claim first)", got)
		}
		q, err := s.QueuedRuns(ctx)
		must(t, err)
		if got := idsOf(q); got != "za" {
			t.Fatalf("QueuedRuns = %q, want za (claim order)", got)
		}
		rn, err := s.RunningRuns(ctx)
		must(t, err)
		if got := idsOf(rn); got != "mq" {
			t.Fatalf("RunningRuns = %q, want mq (claim order)", got)
		}
	})
	t.Run("putting a trigger again keeps its runs and last due time", func(t *testing.T) {
		s := newStore(t)
		tr := sample("t1", "1", "Morning")
		must(t, s.PutTrigger(ctx, tr))
		_, _ = s.Claim(ctx, run("r1", "t1", "k1", triggers.StatusDone))
		_, _ = s.Claim(ctx, run("r2", "t1", "k2", triggers.StatusQueued))
		must(t, s.SetLastDue(ctx, "t1", base.Add(time.Hour)))
		tr.Name = "Evening"
		must(t, s.PutTrigger(ctx, tr))
		l, err := s.ListRuns(ctx, "t1", 10)
		must(t, err)
		if len(l) != 2 {
			t.Fatalf("%d runs after a replace, want 2", len(l))
		}
		if d, _ := s.LastDue(ctx, "t1"); !d.Equal(base.Add(time.Hour)) {
			t.Fatalf("LastDue after a replace = %v", d)
		}
	})
	t.Run("times keep their instant, zone and nanoseconds", func(t *testing.T) {
		s := newStore(t)
		zone := time.FixedZone("x", 5*3600+1800)
		due := time.Date(2026, 10, 22, 13, 45, 30, 123456789, zone)
		started := due.Add(987654321)
		ended := started.Add(time.Second + 1)
		tr := sample("t1", "1", "Morning")
		tr.Since = time.Date(2026, 10, 22, 8, 0, 0, 500000001, zone)
		must(t, s.PutTrigger(ctx, tr))
		got, err := s.GetTrigger(ctx, "t1")
		must(t, err)
		if !got.Since.Equal(tr.Since) {
			t.Fatalf("Since = %v, want %v", got.Since, tr.Since)
		}
		r := run("r1", "t1", "k1", triggers.StatusQueued)
		r.Due = due
		if ok, err := s.Claim(ctx, r); err != nil || !ok {
			t.Fatalf("Claim = %v, %v", ok, err)
		}
		r.Status, r.Started, r.Ended = triggers.StatusDone, started, ended
		must(t, s.SetRunStatus(ctx, r))
		g, err := s.GetRun(ctx, "r1")
		must(t, err)
		if !sameRun(g, r) {
			t.Fatalf("GetRun = %+v, want %+v", g, r)
		}
		must(t, s.SetLastDue(ctx, "t1", started))
		if d, _ := s.LastDue(ctx, "t1"); !d.Equal(started) {
			t.Fatalf("LastDue = %v, want %v", d, started)
		}
		for _, c := range []struct {
			name  string
			since time.Time
			want  int
		}{
			{"the same instant", due, 1},
			{"the same instant in UTC", due.UTC(), 1},
			{"one nanosecond before", due.Add(-time.Nanosecond), 1},
			{"one nanosecond after", due.Add(time.Nanosecond), 0},
		} {
			if n, err := s.CountRunsSince(ctx, "t1", c.since); err != nil || n != c.want {
				t.Fatalf("CountRunsSince from %s = %d, %v; want %d", c.name, n, err, c.want)
			}
		}
	})
	t.Run("a running run counts, and a deleted trigger leaves no runs behind", func(t *testing.T) {
		s := newStore(t)
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
		must(t, s.SetCursor(ctx, 7))
		if n, err := s.CountRunsSince(ctx, "t1", base); err != nil || n != 4 {
			t.Fatalf("CountRunsSince before the delete = %d, %v; want 4", n, err)
		}
		must(t, s.DeleteTrigger(ctx, "t1"))
		q, err := s.QueuedRuns(ctx)
		must(t, err)
		if got := idsOf(q); got != "r5" {
			t.Fatalf("QueuedRuns after the delete = %q, want r5", got)
		}
		rn, err := s.RunningRuns(ctx)
		must(t, err)
		if got := idsOf(rn); got != "r6" {
			t.Fatalf("RunningRuns after the delete = %q, want r6", got)
		}
		if _, err := s.RunByChat(ctx, "chat-del"); !errors.Is(err, triggers.ErrNotFound) {
			t.Fatalf("RunByChat of a deleted run: %v, want ErrNotFound", err)
		}
		if got, err := s.RunByChat(ctx, "chat-t2"); err != nil || got.ID != "r6" {
			t.Fatalf("RunByChat of another trigger = %+v, %v; want r6", got, err)
		}
		if n, err := s.CountRunsSince(ctx, "t1", base); err != nil || n != 0 {
			t.Fatalf("CountRunsSince after the delete = %d, %v; want 0", n, err)
		}
		if rev, ok, _ := s.Cursor(ctx); !ok || rev != 7 {
			t.Fatalf("Cursor after the delete = %d, %v; want 7", rev, ok)
		}
	})
	pruneContract(t, newStore)
}

// must fails the test on an error.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
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
func seedRuns(g *gomega.WithT, s triggers.Store) {
	ctx := context.Background()
	for _, tr := range []string{"t1", "t2"} {
		g.Expect(s.PutTrigger(ctx, sample(tr, "1", tr))).To(gomega.Succeed())
	}
	old := run("running", "t1", "running", triggers.StatusRunning)
	old.Due = base.Add(-1000 * time.Hour)
	queued := run("queued", "t1", "queued", triggers.StatusQueued)
	queued.Due = base.Add(-999 * time.Hour)
	for _, r := range []triggers.Run{old, queued} {
		ok, err := s.Claim(ctx, r)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(ok).To(gomega.BeTrue())
	}
	for i := 1; i <= 10; i++ {
		r := run(fmt.Sprintf("r%d", i), "t1", fmt.Sprintf("k%d", i), triggers.StatusDone)
		r.Due = base.Add(time.Duration(i) * time.Hour)
		ok, err := s.Claim(ctx, r)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(ok).To(gomega.BeTrue())
	}
	for i := 1; i <= 2; i++ {
		r := run(fmt.Sprintf("o%d", i), "t2", fmt.Sprintf("k%d", i), triggers.StatusFailed)
		r.Due = base.Add(-time.Duration(i) * time.Hour)
		ok, err := s.Claim(ctx, r)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(ok).To(gomega.BeTrue())
	}
}

// runIDs lists the IDs of the runs of a trigger, oldest claim first.
func runIDs(g *gomega.WithT, s triggers.Store, trigger string) []string {
	runs, err := s.ListRuns(context.Background(), trigger, 0)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	ids := make([]string, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		ids = append(ids, runs[i].ID)
	}
	return ids
}

// pruneContract checks Store.PruneRuns: the count limit, the protected window,
// the age limit, that open runs and other triggers stay, and that a removed
// claim can be made again.
func pruneContract(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	open := []string{"running", "queued"}
	ids := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf("r%d", i))
		}
		return out
	}
	cases := []struct {
		name    string
		p       triggers.Retention
		removed int
		kept    []string // finished runs of t1 that stay
	}{
		{"the newest Keep finished runs stay", triggers.Retention{Keep: 3}, 7, ids(8, 10)},
		{"runs due in the protected window stay beyond Keep", triggers.Retention{Keep: 3, Protect: base.Add(5 * time.Hour)}, 4, ids(5, 10)},
		{"runs older than Expire go whatever Keep says", triggers.Retention{Keep: 10, Protect: base, Expire: base.Add(4 * time.Hour)}, 3, ids(4, 10)},
		{"Expire goes over the protected window", triggers.Retention{Keep: 3, Protect: base, Expire: base.Add(6 * time.Hour)}, 5, ids(6, 10)},
		{"no limit removes nothing", triggers.Retention{}, 0, ids(1, 10)},
		{"Keep above the number of runs removes nothing", triggers.Retention{Keep: 50}, 0, ids(1, 10)},
	}
	for _, c := range cases {
		g := gomega.NewWithT(t)
		s := newStore(t)
		seedRuns(g, s)
		n, err := s.PruneRuns(ctx, "t1", c.p)
		g.Expect(err).NotTo(gomega.HaveOccurred(), c.name)
		g.Expect(n).To(gomega.Equal(c.removed), c.name)
		g.Expect(runIDs(g, s, "t1")).To(gomega.Equal(append(append([]string{}, open...), c.kept...)), c.name)
		g.Expect(runIDs(g, s, "t2")).To(gomega.Equal([]string{"o1", "o2"}), c.name+": another trigger is not touched")
		queued, err := s.QueuedRuns(ctx)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(queued).To(gomega.HaveLen(1), c.name+": a queued run stays")
	}

	g := gomega.NewWithT(t)
	s := newStore(t)
	seedRuns(g, s)
	_, err := s.PruneRuns(ctx, "t1", triggers.Retention{Keep: 3})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	again := run("again", "t1", "k1", triggers.StatusQueued)
	ok, err := s.Claim(ctx, again)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(ok).To(gomega.BeTrue(), "the claim of a removed run is free again")
	ok, err = s.Claim(ctx, run("kept", "t1", "k10", triggers.StatusQueued))
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(ok).To(gomega.BeFalse(), "the claim of a kept run is still taken")
	n, err := s.PruneRuns(ctx, "nope", triggers.Retention{Keep: 1})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(n).To(gomega.BeZero(), "an unknown trigger has nothing to prune")
}
