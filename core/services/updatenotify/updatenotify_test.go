package updatenotify_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/mudler/LocalAI/core/services/updatenotify"
)

// frontend is one process: its own service on the shared store and bus, with a
// hook that counts what push and Telegram would send.
type frontend struct {
	svc    *notify.Service
	pushed []notify.Notification
	em     *Emitter
}

func newFrontend(st notify.Store, fan notify.Fanout, admins ...string) *frontend {
	f := &frontend{}
	f.svc = notify.NewService(notify.Options{Store: st, Fanout: fan})
	f.svc.OnAdd(func(n notify.Notification) { f.pushed = append(f.pushed, n) })
	f.em = &Emitter{Inbox: func() *notify.Service { return f.svc }, Admins: func(context.Context) ([]string, error) { return admins, nil }}
	return f
}

func memStore() notify.Store {
	st, err := notify.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	return st
}

func item(id, name, version string) backendupdates.Item {
	return backendupdates.Item{ID: "gallery:" + name, Kind: backendupdates.KindGallery, Name: name, State: backendupdates.ItemUpdate, AvailableVersion: version}
}

func snap(auto bool, items ...backendupdates.Item) backendupdates.Snapshot {
	return backendupdates.Snapshot{
		Sources:    map[string]backendupdates.Source{"gallery": {State: backendupdates.StateOK}, "library": {State: backendupdates.StateDisabled}},
		AutoUpdate: backendupdates.AutoUpdate{Backends: auto}, Items: items,
	}
}

func list(f *frontend, who string) []notify.Notification {
	got, err := f.svc.List(context.Background(), who, false, 0)
	Expect(err).NotTo(HaveOccurred())
	return got
}

var _ = Describe("the digest", func() {
	ctx := context.Background()

	It("counts the backend updates for each administrator and names them", func() {
		f := newFrontend(memStore(), nil, "admin1", "admin2")
		Expect(f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2"), item("b", "whisper", "3")))).To(Succeed())
		for _, who := range []string{"admin1", "admin2"} {
			got := list(f, who)
			Expect(got).To(HaveLen(1))
			Expect(got[0].Title).To(Equal("2 backend updates available"))
			Expect(got[0].Body).To(Equal("llama-cpp, whisper"))
			Expect(got[0].Code).To(Equal(CodeDigest))
			Expect(got[0].Link).To(Equal(LinkReady))
			Expect(got[0].Kind).To(Equal(notify.KindNeedsAttention))
			Expect(got[0].Source).To(Equal(notify.SourceSystem))
			Expect(got[0].Params).To(HaveKeyWithValue("count", BeNumerically("==", 2)))
			Expect(got[0].Params).To(HaveKeyWithValue("family", "backends"))
		}
		Expect(list(f, "somebody else")).To(BeEmpty(), "only administrators are told")
		Expect(f.pushed).To(HaveLen(2), "one push for each administrator")
	})

	It("says 1 backend update available for one, and 'and N more' past three names", func() {
		f := newFrontend(memStore(), nil, "a")
		Expect(f.em.Digest(ctx, snap(false, item("a", "x", "2")))).To(Succeed())
		Expect(list(f, "a")[0].Title).To(Equal("1 backend update available"))
		Expect(f.em.Digest(ctx, snap(false, item("a", "a", "2"), item("b", "b", "2"), item("c", "c", "2"), item("d", "d", "2"), item("e", "e", "2")))).To(Succeed())
		Expect(list(f, "a")[0].Body).To(Equal("a, b, c and 2 more"))
	})

	It("does not count a source that an automatic update covers, and removes the digest when it is turned on", func() {
		f := newFrontend(memStore(), nil, "a")
		items := []backendupdates.Item{item("a", "llama-cpp", "2")}
		Expect(f.em.Digest(ctx, snap(true, items...))).To(Succeed())
		Expect(list(f, "a")).To(BeEmpty(), "automatic update on: no digest")

		Expect(f.em.Digest(ctx, snap(false, items...))).To(Succeed())
		Expect(list(f, "a")).To(HaveLen(1))
		Expect(f.em.Digest(ctx, snap(true, items...))).To(Succeed())
		Expect(list(f, "a")).To(BeEmpty(), "turned on: the stored digest goes")
	})

	It("comes back once when the switch is turned off again, because the ids are new", func() {
		f := newFrontend(memStore(), nil, "a")
		items := []backendupdates.Item{item("a", "llama-cpp", "2")}
		_ = f.em.Digest(ctx, snap(false, items...))
		_ = f.em.Digest(ctx, snap(true, items...))
		_ = f.em.Digest(ctx, snap(false, items...))
		Expect(f.pushed).To(HaveLen(2), "pushed when it appeared and again when it came back")
		Expect(list(f, "a")).To(HaveLen(1))
	})

	It("stays dismissed for the same updates and for fewer, and comes back unread for a new one", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2"), item("b", "whisper", "3")))
		row := list(f, "a")[0]
		Expect(f.svc.Delete(ctx, "a", []int64{row.ID})).To(Succeed())

		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2"), item("b", "whisper", "3")))
		Expect(list(f, "a")).To(BeEmpty(), "same updates after the dismissal")
		_ = f.em.Digest(ctx, snap(false, item("b", "whisper", "3")))
		Expect(list(f, "a")).To(BeEmpty(), "fewer updates after the dismissal")
		Expect(f.pushed).To(HaveLen(1))

		_ = f.em.Digest(ctx, snap(false, item("b", "whisper", "3"), item("c", "piper", "1")))
		got := list(f, "a")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Read).To(BeFalse())
		Expect(got[0].Title).To(Equal("2 backend updates available"))
		Expect(f.pushed).To(HaveLen(2), "announced again for the new one")
	})

	It("treats a newer release of the same backend as a new update", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2")))
		row := list(f, "a")[0]
		Expect(f.svc.Delete(ctx, "a", []int64{row.ID})).To(Succeed())
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "3")))
		Expect(list(f, "a")).To(HaveLen(1))
	})

	It("edits the count quietly when updates are done, and keeps it read", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2"), item("b", "whisper", "3")))
		row := list(f, "a")[0]
		Expect(f.svc.MarkRead(ctx, "a", []int64{row.ID})).To(Succeed())
		sub, cancel := f.svc.Subscribe("a")
		defer cancel()
		_ = f.em.Digest(ctx, snap(false, item("b", "whisper", "3")))
		got := list(f, "a")
		Expect(got).To(HaveLen(1))
		Expect(got[0].ID).To(Equal(row.ID))
		Expect(got[0].Title).To(Equal("1 backend update available"))
		Expect(got[0].Read).To(BeTrue())
		Expect(f.pushed).To(HaveLen(1), "no push for a smaller count")
		var ev notify.Notification
		Eventually(sub).Should(Receive(&ev))
		Expect(ev.Op).To(Equal(notify.OpChanged), "open tabs read the list again")
	})

	It("is silent when nothing changed", func() {
		f := newFrontend(memStore(), nil, "a")
		s := snap(false, item("a", "llama-cpp", "2"))
		_ = f.em.Digest(ctx, s)
		sub, cancel := f.svc.Subscribe("a")
		defer cancel()
		_ = f.em.Digest(ctx, s)
		Consistently(sub, 50*time.Millisecond).ShouldNot(Receive())
		Expect(f.pushed).To(HaveLen(1))
	})

	It("removes the digest when no update is left, and the same update later is new", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2")))
		_ = f.em.Digest(ctx, snap(false))
		Expect(list(f, "a")).To(BeEmpty())
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2")))
		Expect(list(f, "a")).To(HaveLen(1))
		Expect(f.pushed).To(HaveLen(2))
	})

	It("does not change the digest while a source has not answered", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2")))
		failed := snap(false) // nothing listed: the failed check lost the list
		failed.Sources["library"] = backendupdates.Source{State: backendupdates.StateFailed}
		Expect(f.em.Digest(ctx, failed)).To(Succeed())
		Expect(list(f, "a")).To(HaveLen(1), "a failed check is not 'all done'")
		never := snap(false)
		never.Sources["gallery"] = backendupdates.Source{State: backendupdates.StateNever}
		_ = f.em.Digest(ctx, never)
		Expect(list(f, "a")).To(HaveLen(1))
	})

	It("counts nothing when this server refuses installs", func() {
		f := newFrontend(memStore(), nil, "a")
		s := snap(false, item("a", "llama-cpp", "2"))
		s.InstallsDisabled = true
		_ = f.em.Digest(ctx, s)
		Expect(list(f, "a")).To(BeEmpty())
	})
})

var _ = Describe("who is told", func() {
	ctx := context.Background()
	s := snap(false, item("a", "llama-cpp", "2"))

	It("tells nobody when there are no administrators", func() {
		f := newFrontend(memStore(), nil)
		Expect(f.em.Digest(ctx, s)).To(Succeed())
		Expect(f.pushed).To(BeEmpty())
	})

	It("does nothing, without an error, when the chats are off", func() {
		em := &Emitter{Inbox: func() *notify.Service { return nil }, Admins: func(context.Context) ([]string, error) { return []string{"a"}, nil }}
		Expect(em.Digest(ctx, s)).To(Succeed())
		Expect(em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{{Kind: OutcomeUpdated, ID: "x", Name: "x"}}})).To(Succeed())
		Expect(em.CheckFailed(ctx, []string{"gallery"})).To(Succeed())
		var none *Emitter
		Expect(none.Digest(ctx, s)).To(Succeed())
	})

	It("does nothing when the administrators cannot be listed", func() {
		f := newFrontend(memStore(), nil)
		f.em.Admins = func(context.Context) ([]string, error) { return nil, errors.New("database is down") }
		Expect(f.em.Digest(ctx, s)).To(Succeed())
		Expect(f.pushed).To(BeEmpty())
	})

	It("tells the anonymous principal that the caller names when authentication is off", func() {
		f := newFrontend(memStore(), nil, "anonymous")
		Expect(f.em.Digest(ctx, s)).To(Succeed())
		Expect(list(f, "anonymous")).To(HaveLen(1))
	})
})

var _ = Describe("the result of an automatic pass", func() {
	ctx := context.Background()
	upd := Outcome{Kind: OutcomeUpdated, ID: "gallery:llama-cpp@0.9.7", Name: "llama-cpp", From: "0.9.4", To: "0.9.7"}
	upd2 := Outcome{Kind: OutcomeUpdated, ID: "gallery:piper@1.1", Name: "piper"}
	bad := Outcome{Kind: OutcomeFailed, ID: "gallery:whisper@2", Name: "whisper", Err: "download\ninterrupted"}
	held := Outcome{Kind: OutcomeHeld, ID: "tables@sha256:c", Name: "Tables", To: "2.4.0"}

	It("posts one item for the whole pass with the counts and the names", func() {
		f := newFrontend(memStore(), nil, "a")
		Expect(f.em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{upd, upd2, bad}})).To(Succeed())
		got := list(f, "a")
		Expect(got).To(HaveLen(1), "one item, not one for each backend")
		Expect(got[0].Title).To(Equal("Backends: 2 updated automatically, 1 failed"))
		Expect(got[0].Body).To(Equal("Updated: llama-cpp 0.9.4 to 0.9.7, piper. Failed: whisper (download interrupted)."))
		Expect(got[0].Status).To(Equal(notify.StatusFailed))
		Expect(got[0].Kind).To(Equal(notify.KindNeedsAttention))
		Expect(got[0].Link).To(Equal(LinkNeedsYou))
		Expect(got[0].Code).To(Equal(CodeResult))
		Expect(got[0].Params).To(HaveKeyWithValue("family", "backends"))
		Expect(got[0].Params).To(HaveKeyWithValue("updated", BeNumerically("==", 2)))
		Expect(got[0].Params).To(HaveKeyWithValue("failed", BeNumerically("==", 1)))
		Expect(f.pushed).To(HaveLen(1))
	})

	It("is an ok item that links to the page when everything updated", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{upd}})
		got := list(f, "a")[0]
		Expect(got.Title).To(Equal("Backends: 1 updated automatically"))
		Expect(got.Status).To(Equal(notify.StatusOK))
		Expect(got.Kind).To(Equal(notify.KindRunEnded))
		Expect(got.Link).To(Equal(LinkPage))
	})

	It("counts a held add-on as needing a person without calling it a failure", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Result(ctx, Result{Family: FamilyAddons, Outcomes: []Outcome{held}})
		got := list(f, "a")[0]
		Expect(got.Title).To(Equal("Add-ons: 1 waiting for approval"))
		Expect(got.Body).To(Equal("Waiting for your approval: Tables 2.4.0."))
		Expect(got.Status).To(Equal(notify.StatusOK))
		Expect(got.Kind).To(Equal(notify.KindNeedsAttention))
		Expect(got.Link).To(Equal(LinkNeedsYou))
	})

	It("keeps each family in its own item", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{upd}})
		_ = f.em.Result(ctx, Result{Family: FamilyAddons, Outcomes: []Outcome{{Kind: OutcomeUpdated, ID: "tables@2.4.0", Name: "Tables"}}})
		Expect(list(f, "a")).To(HaveLen(2))
	})

	It("posts nothing for a pass that did nothing", func() {
		f := newFrontend(memStore(), nil, "a")
		Expect(f.em.Result(ctx, Result{Family: FamilyBackends})).To(Succeed())
		Expect(f.em.Result(ctx, Result{Family: "models", Outcomes: []Outcome{upd}})).To(Succeed())
		Expect(list(f, "a")).To(BeEmpty())
	})

	It("is silent for the same failure at the next pass, also after it was deleted", func() {
		f := newFrontend(memStore(), nil, "a")
		pass := Result{Family: FamilyBackends, Outcomes: []Outcome{bad}}
		_ = f.em.Result(ctx, pass)
		_ = f.em.Result(ctx, pass)
		Expect(list(f, "a")).To(HaveLen(1))
		Expect(f.pushed).To(HaveLen(1))
		Expect(f.svc.Delete(ctx, "a", []int64{list(f, "a")[0].ID})).To(Succeed())
		_ = f.em.Result(ctx, pass)
		Expect(list(f, "a")).To(BeEmpty(), "a dismissed report does not come back")
		Expect(f.pushed).To(HaveLen(1))
	})

	It("posts again when the failed target is another release", func() {
		f := newFrontend(memStore(), nil, "a")
		_ = f.em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{bad}})
		next := bad
		next.ID = "gallery:whisper@3"
		_ = f.em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{next}})
		Expect(list(f, "a")).To(HaveLen(2))
		Expect(f.pushed).To(HaveLen(2))
	})

	It("posts once per administrator", func() {
		f := newFrontend(memStore(), nil, "a", "b")
		_ = f.em.Result(ctx, Result{Family: FamilyBackends, Outcomes: []Outcome{upd}})
		Expect(f.pushed).To(HaveLen(2))
	})
})

var _ = Describe("the notice of a check that keeps failing", func() {
	ctx := context.Background()

	It("is written once for the failing sources, goes when they recover, and comes back for a new source", func() {
		f := newFrontend(memStore(), nil, "a")
		Expect(f.em.CheckFailed(ctx, []string{"gallery"})).To(Succeed())
		got := list(f, "a")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Status).To(Equal(notify.StatusFailed))
		Expect(got[0].Body).To(ContainSubstring("Could not check: backend gallery."))
		Expect(got[0].Link).To(Equal(LinkPage))

		_ = f.em.CheckFailed(ctx, []string{"gallery"})
		Expect(f.pushed).To(HaveLen(1), "the same source failing again is silent")

		_ = f.em.CheckFailed(ctx, []string{"library", "gallery"})
		Expect(f.pushed).To(HaveLen(2), "a second source failing is new")
		Expect(list(f, "a")[0].Body).To(ContainSubstring("backend gallery, backend library"))

		_ = f.em.CheckFailed(ctx, nil)
		Expect(list(f, "a")).To(BeEmpty())
		_ = f.em.CheckFailed(ctx, []string{"gallery"})
		Expect(f.pushed).To(HaveLen(3), "failing again after a recovery is new")
	})
})

// Several frontends share the store and the bus. The inbox does not coordinate
// them: the caller must be the one runner. These specs show what the layers do
// when that holds, and that a second runner by mistake cannot push twice.
var _ = Describe("two frontends", func() {
	ctx := context.Background()

	It("shows the leader's item to the clients of the other frontend, with one push", func() {
		bus := testutil.NewFakeBus()
		st := memStore()
		leader := newFrontend(st, notify.NewBroadcastFanout(bus, nil), "a")
		other := newFrontend(st, notify.NewBroadcastFanout(bus, nil), "a")
		sub, cancel := other.svc.Subscribe("a")
		defer cancel()

		Expect(leader.em.Digest(ctx, snap(false, item("a", "llama-cpp", "2")))).To(Succeed())

		var heard notify.Notification
		Eventually(sub).Should(Receive(&heard))
		Expect(heard.Title).To(Equal("1 backend update available"))
		Expect(list(other, "a")).To(HaveLen(1), "the shared store carries it")
		Expect(leader.pushed).To(HaveLen(1))
		Expect(other.pushed).To(BeEmpty(), "only the frontend that stores it pushes")
	})

	It("stays silent when the other frontend repeats the same digest or the same result", func() {
		bus := testutil.NewFakeBus()
		st := memStore()
		leader := newFrontend(st, notify.NewBroadcastFanout(bus, nil), "a")
		other := newFrontend(st, notify.NewBroadcastFanout(bus, nil), "a")
		s := snap(false, item("a", "llama-cpp", "2"))
		pass := Result{Family: FamilyBackends, Outcomes: []Outcome{{Kind: OutcomeFailed, ID: "x@1", Name: "x", Err: "no"}}}
		_ = leader.em.Digest(ctx, s)
		_ = leader.em.Result(ctx, pass)
		_ = other.em.Digest(ctx, s)
		_ = other.em.Result(ctx, pass)
		Expect(leader.pushed).To(HaveLen(2))
		Expect(other.pushed).To(BeEmpty(), "a second runner by mistake pushes nothing")
		Expect(list(other, "a")).To(HaveLen(2))
	})
})
