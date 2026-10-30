package wsfeed_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
	"github.com/mudler/LocalAI/core/services/studio/wsfeed"
)

func TestWsfeed(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Workspace Feed Suite")
}

const admin = "admin"

var adminActor = workspace.Actor{Principal: admin}

func gormStore() workspace.Store {
	GinkgoHelper()
	db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "ws.db"))
	Expect(err).ToNot(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).ToNot(HaveOccurred())
	s, err := gormstore.New(db, blobs, gormstore.Options{Admin: admin})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return s
}

func jsonStore() workspace.Store {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	Expect(err).ToNot(HaveOccurred())
	s, err := jsonstore.New(filepath.Join(dir, "index"), blobs, jsonstore.Options{Admin: admin})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	return s
}

// home gives a user write access to their home folder, as the files service does.
func home(s workspace.Store, principal string) {
	GinkgoHelper()
	_, err := s.Mkdir(context.Background(), adminActor, session.HomePath(principal))
	Expect(err).ToNot(HaveOccurred())
	Expect(s.Grant(context.Background(), adminActor, principal, session.HomePath(principal), workspace.ModeWrite)).To(Succeed())
}

func put(s workspace.Store, a workspace.Actor, p, content string) workspace.Node {
	GinkgoHelper()
	n, err := s.Put(context.Background(), a, p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).ToNot(HaveOccurred())
	return n
}

func newFeed(s workspace.Store, o wsfeed.Options) *wsfeed.Feed {
	GinkgoHelper()
	o.Store, o.Admin = s, adminActor
	if o.Interval == 0 {
		o.Interval = 10 * time.Millisecond
	}
	f := wsfeed.New(o)
	DeferCleanup(f.Close)
	return f
}

func subscribe(f *wsfeed.Feed, principal string, after int64, resume bool) <-chan wsfeed.Event {
	GinkgoHelper()
	ch, cancel, err := f.Subscribe(context.Background(), principal, after, resume)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(cancel)
	return ch
}

// collect reads events until quiet passes without one.
func collect(ch <-chan wsfeed.Event, quiet time.Duration) []wsfeed.Event {
	var out []wsfeed.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-time.After(quiet):
			return out
		}
	}
}

func pathsOf(evs []wsfeed.Event) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Paths...)
	}
	return out
}

const alice, bob = "alice", "bob"

var aliceActor = workspace.Actor{Principal: alice}

func describeFeed(name string, newStore func() workspace.Store) {
	Describe(name, func() {
		var s workspace.Store
		BeforeEach(func() {
			s = newStore()
			home(s, alice)
			home(s, bob)
		})

		It("delivers a change made outside the feed, with paths inside the home", func() {
			f := newFeed(s, wsfeed.Options{})
			ch := subscribe(f, alice, 0, false)
			n := put(s, aliceActor, "/users/alice/home.json", "{}")
			var ev wsfeed.Event
			Eventually(ch, 2*time.Second).Should(Receive(&ev))
			Expect(ev.Resync).To(BeFalse())
			Expect(ev.Revision).To(Equal(n.Rev))
			Expect(ev.Paths).To(Equal([]string{"home.json"}))
			Expect(ev.Kinds).To(ContainElement(string(workspace.ChangeUpload)))
		})

		It("delivers a change that another frontend's writer made", func() {
			// Two feeds on one store stand for two frontends: the journal is
			// the only thing they share, and the writer is neither of them.
			writerSide := newFeed(s, wsfeed.Options{})
			readerSide := newFeed(s, wsfeed.Options{})
			_ = subscribe(writerSide, alice, 0, false)
			ch := subscribe(readerSide, alice, 0, false)
			put(s, workspace.Actor{Principal: alice, ChatID: "c1"}, "/users/alice/pages/x/index.html", "<p>")
			var ev wsfeed.Event
			Eventually(ch, 2*time.Second).Should(Receive(&ev))
			Expect(ev.Paths).To(ContainElement("pages/x/index.html"))
		})

		It("never delivers the paths of another user", func() {
			f := newFeed(s, wsfeed.Options{})
			ach := subscribe(f, alice, 0, false)
			bch := subscribe(f, bob, 0, false)
			put(s, adminActor, "/users/bob/secret.txt", "b")
			put(s, adminActor, "/shared/x.txt", "x")
			Eventually(bch, 2*time.Second).Should(Receive())
			Consistently(ach, 200*time.Millisecond).ShouldNot(Receive())
			put(s, aliceActor, "/users/alice/a.txt", "a")
			evs := collect(ach, 300*time.Millisecond)
			Expect(pathsOf(evs)).To(Equal([]string{"a.txt"}))
		})

		It("collapses a burst into few events", func() {
			// The test starts the reads, so the count does not depend on how
			// fast the store writes: the first read runs when the loop starts
			// and may see part of the burst, the second one sees the rest.
			ticks := make(chan time.Time)
			f := newFeed(s, wsfeed.Options{})
			wsfeed.SetTicks(f, ticks)
			ch := subscribe(f, alice, 0, false)
			for i := range 20 {
				put(s, aliceActor, fmt.Sprintf("/users/alice/pages/p/%d.json", i), "x")
			}
			ticks <- time.Now()
			var ev wsfeed.Event
			Eventually(ch, 3*time.Second).Should(Receive(&ev))
			evs := append([]wsfeed.Event{ev}, collect(ch, 500*time.Millisecond)...)
			Expect(len(evs)).To(BeNumerically("<=", 2), "a burst of 20 change sets gave %d events", len(evs))
			// The folders the first write created are changes too.
			Expect(pathsOf(evs)).To(HaveLen(22))
			Expect(pathsOf(evs)).To(ContainElements("pages", "pages/p", "pages/p/0.json", "pages/p/19.json"))
		})

		It("resumes after a revision, and only with the user's changes", func() {
			first := put(s, aliceActor, "/users/alice/one.txt", "1")
			put(s, adminActor, "/users/bob/b.txt", "b")
			put(s, aliceActor, "/users/alice/two.txt", "2")
			f := newFeed(s, wsfeed.Options{})
			ch := subscribe(f, alice, first.Rev, true)
			var ev wsfeed.Event
			Eventually(ch, 2*time.Second).Should(Receive(&ev))
			Expect(ev.Paths).To(Equal([]string{"two.txt"}))
		})

		It("sends a resync when the resume point is too far back", func() {
			for i := range 6 {
				put(s, aliceActor, fmt.Sprintf("/users/alice/%d.txt", i), "x")
			}
			f := newFeed(s, wsfeed.Options{MaxCatchUp: 3})
			ch := subscribe(f, alice, 0, true)
			var ev wsfeed.Event
			Eventually(ch, 2*time.Second).Should(Receive(&ev))
			Expect(ev.Resync).To(BeTrue())
		})

		It("starts from now without a resume point", func() {
			put(s, aliceActor, "/users/alice/old.txt", "x")
			f := newFeed(s, wsfeed.Options{})
			ch := subscribe(f, alice, 0, false)
			Consistently(ch, 200*time.Millisecond).ShouldNot(Receive())
		})

		It("collapses a slow subscriber's backlog into one resync without stopping the others", func() {
			f := newFeed(s, wsfeed.Options{Buffer: 1, Interval: 5 * time.Millisecond})
			slow := subscribe(f, alice, 0, false)
			fast := subscribe(f, alice, 0, false)
			var got []wsfeed.Event
			for i := range 8 {
				put(s, aliceActor, fmt.Sprintf("/users/alice/%d.txt", i), "x")
				var ev wsfeed.Event
				Eventually(fast, 2*time.Second).Should(Receive(&ev))
				got = append(got, ev)
			}
			Expect(got).To(HaveLen(8))
			var ev wsfeed.Event
			Expect(slow).To(Receive(&ev))
			Expect(ev.Resync).To(BeTrue())
			Expect(slow).ToNot(Receive())
		})

		It("bounds the subscribers of one user", func() {
			f := newFeed(s, wsfeed.Options{MaxPerUser: 2})
			_ = subscribe(f, alice, 0, false)
			_ = subscribe(f, alice, 0, false)
			_, _, err := f.Subscribe(context.Background(), alice, 0, false)
			Expect(err).To(MatchError(wsfeed.ErrTooManySubscribers))
			_ = subscribe(f, bob, 0, false)
		})

		It("closes every stream on Close and refuses new ones", func() {
			f := newFeed(s, wsfeed.Options{})
			ch := subscribe(f, alice, 0, false)
			f.Close()
			Eventually(ch).Should(BeClosed())
			_, _, err := f.Subscribe(context.Background(), alice, 0, false)
			Expect(err).To(MatchError(wsfeed.ErrClosed))
		})

		It("closes a stream on cancel", func() {
			f := newFeed(s, wsfeed.Options{})
			ch, cancel, err := f.Subscribe(context.Background(), alice, 0, false)
			Expect(err).ToNot(HaveOccurred())
			cancel()
			cancel()
			Eventually(ch).Should(BeClosed())
		})
	})
}

var _ = Describe("The workspace feed", func() {
	describeFeed("on the SQL store", gormStore)
	describeFeed("on the JSON store", jsonStore)
})
