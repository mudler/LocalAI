package hints_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/hints"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

func TestHints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hints Suite")
}

const alice = "alice"

// clock is a fake clock the specs move by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time         { return c.t }
func (c *clock) add(d time.Duration)    { c.t = c.t.Add(d) }
func (c *clock) set(s string) time.Time { c.t, _ = time.Parse(time.RFC3339, s); return c.t }

type env struct {
	svc     *hints.Service
	wk      *wellknown.Service
	clk     *clock
	created time.Time
	busy    bool
	refused bool
	server  bool
}

func newEnv() *env {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	f := files.New(ws, files.WithBlobs(blobs))
	Expect(f.EnsureHome(context.Background(), alice)).To(Succeed())
	e := &env{wk: wellknown.New(f), clk: &clock{}, server: true}
	// The account is a day old: the quiet start is over unless a spec says so.
	e.created = e.clk.set("2026-10-30T09:00:00Z").Add(-24 * time.Hour)
	e.svc = hints.New(hints.Deps{
		WellKnown: e.wk,
		Now:       e.clk.now,
		DefaultOn: func() bool { return e.server },
		FirstSeen: func(context.Context, string) time.Time { return e.created },
		Busy:      func(context.Context, string, string) bool { return e.busy },
		Refused:   func(context.Context, string, string) bool { return e.refused },
	})
	return e
}

func (e *env) offer(chat string) (string, hints.Reason) {
	return e.svc.Offer(context.Background(), alice, hints.Chat{ID: chat, Person: true, Turns: 2})
}

func (e *env) seen() map[string]any {
	doc, err := e.wk.Read(context.Background(), alice, wellknown.User, "")
	Expect(err).NotTo(HaveOccurred())
	s, _ := doc.Data["seen"].(map[string]any)
	return s
}

func (e *env) setUser(values map[string]any) {
	_, err := e.wk.Write(context.Background(), alice, wellknown.WriteRequest{Name: wellknown.User, Values: values})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("offers from the assistant", func() {
	var e *env
	BeforeEach(func() { e = newEnv() })

	It("allows one offer, tells the model in one line, and records it under seen", func() {
		line, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		Expect(line).To(ContainSubstring("profile, shortcut"))
		Expect(line).NotTo(ContainSubstring("schedule"), "Home has no widget that reads a data file")
		s := e.seen()
		Expect(s).To(HaveKeyWithValue("last_offer", "2026-10-30T09:00:00Z"))
		Expect(s["offer_chats"]).To(ConsistOf("c1"))
	})

	It("costs few tokens when allowed, and none otherwise", func() {
		all := hints.Line([]hints.Kind{hints.Profile, hints.Shortcut, hints.Schedule})
		// About four characters a token for English text.
		Expect(len(all)/4).To(BeNumerically("<", 60), all)
		_, _ = e.offer("c1")
		line, _ := e.offer("c2")
		Expect(line).To(BeEmpty())
	})

	DescribeTable("never in a chat that is not a person's, or too early, or after a failure",
		func(c hints.Chat, want hints.Reason) {
			line, why := e.svc.Offer(context.Background(), alice, c)
			Expect(line).To(BeEmpty())
			Expect(why).To(Equal(want))
			Expect(e.seen()).To(BeEmpty(), "nothing is recorded")
		},
		Entry("a connector, trigger or page chat", hints.Chat{ID: "c", Turns: 5}, hints.NotPerson),
		Entry("the skill switched off", hints.Chat{ID: "c", Person: true, Turns: 5, SkillOff: true}, hints.SkillOff),
		Entry("the first turn", hints.Chat{ID: "c", Person: true, Turns: 0}, hints.EarlyTurn),
		Entry("the second turn", hints.Chat{ID: "c", Person: true, Turns: 1}, hints.EarlyTurn),
		Entry("after a failed turn", hints.Chat{ID: "c", Person: true, Turns: 3, Failed: true}, hints.FailedTurn),
	)

	It("allows at most one offer a day, by the user's own calendar", func() {
		e.setUser(map[string]any{"timezone": "Europe/Rome"})
		e.clk.set("2026-10-30T21:00:00Z") // 22:00 in Rome
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		e.clk.add(90 * time.Minute) // 23:30 in Rome, the same day
		_, why = e.offer("c2")
		Expect(why).To(Equal(hints.SameDay))
		e.clk.add(time.Hour) // 00:30 in Rome: a new day, though not in UTC
		_, why = e.offer("c2")
		Expect(why).To(Equal(hints.Allowed))
	})

	It("gives the same answer when the hook of one turn runs twice", func() {
		first, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		again, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		Expect(again).To(Equal(first))
		_, why = e.svc.Offer(context.Background(), alice, hints.Chat{ID: "c1", Person: true, Turns: 3})
		Expect(why).To(Equal(hints.SameDay), "the next turn has no offer")
	})

	It("allows at most one offer in a chat, ever", func() {
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		e.clk.add(72 * time.Hour)
		_, why = e.svc.Offer(context.Background(), alice, hints.Chat{ID: "c1", Person: true, Turns: 9})
		Expect(why).To(Equal(hints.SameChat))
	})

	It("keeps quiet in the first 10 minutes of the account", func() {
		e.created = e.clk.now().Add(-9 * time.Minute)
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.QuietStarted))
		e.clk.add(2 * time.Minute)
		_, why = e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
	})

	It("starts the quiet start the first time it meets a user it knows nothing about", func() {
		e.created = time.Time{}
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.QuietStarted))
		Expect(e.seen()).To(HaveKeyWithValue("since", "2026-10-30T09:00:00Z"))
		e.clk.add(5 * time.Minute)
		_, why = e.offer("c1")
		Expect(why).To(Equal(hints.QuietStarted))
		e.clk.add(6 * time.Minute)
		_, why = e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
	})

	It("follows the user's switch first, then the server's default", func() {
		e.setUser(map[string]any{"seen": map[string]any{"offers_off": true}})
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.OffByUser))

		e.server = false
		e.setUser(map[string]any{"seen": map[string]any{}})
		_, why = e.offer("c1")
		Expect(why).To(Equal(hints.OffByServer))

		e.setUser(map[string]any{"seen": map[string]any{"offers_off": false}})
		_, why = e.offer("c1")
		Expect(why).To(Equal(hints.Allowed), "a user who turned them on keeps them")
	})

	It("never offers again what the user declined, and stops when every kind is answered", func() {
		e.setUser(map[string]any{"seen": map[string]any{"offers": map[string]any{"profile": map[string]any{"state": "declined"}}}})
		line, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		Expect(line).NotTo(ContainSubstring("profile"))

		e.clk.add(48 * time.Hour)
		e.setUser(map[string]any{"seen": map[string]any{"offers": map[string]any{
			"profile": map[string]any{"state": "declined"}, "shortcut": map[string]any{"state": "accepted"},
		}}})
		_, why = e.offer("c2")
		Expect(why).To(Equal(hints.NoKind))
	})

	It("reads a decline kept in the older flat record", func() {
		e.setUser(map[string]any{"seen": map[string]any{"profile": map[string]any{"state": "declined"}}})
		line, _ := e.offer("c1")
		Expect(line).NotTo(ContainSubstring("profile"))
	})

	It("brings a put-off offer back after 14 days, and a second \"not now\" counts as no", func() {
		e.setUser(map[string]any{"seen": map[string]any{"offers": map[string]any{
			"shortcut": map[string]any{"state": "later", "at": "2026-10-29", "count": 1},
			"profile":  map[string]any{"state": "declined"},
		}}})
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.NoKind))
		e.clk.add(14 * 24 * time.Hour)
		line, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		Expect(line).To(ContainSubstring("shortcut"))

		e.clk.add(30 * 24 * time.Hour)
		e.setUser(map[string]any{"seen": map[string]any{"offers": map[string]any{
			"shortcut": map[string]any{"state": "later", "at": "2026-11-13", "count": 2},
			"profile":  map[string]any{"state": "declined"},
		}}})
		_, why = e.offer("c2")
		Expect(why).To(Equal(hints.NoKind))
	})

	It("offers no profile once the name and the bio are saved", func() {
		e.setUser(map[string]any{"name": "Marta", "bio": "Designer"})
		line, _ := e.offer("c1")
		Expect(line).NotTo(ContainSubstring("profile"))
	})

	It("offers a schedule only when Home has a widget that reads a data file", func() {
		_, err := e.wk.Write(context.Background(), alice, wellknown.WriteRequest{Name: wellknown.Home, Values: map[string]any{
			"widgets": []any{map[string]any{"id": "tasks", "type": "list", "title": "Tasks", "source": "data/tasks.json"}},
		}})
		Expect(err).NotTo(HaveOccurred())
		line, _ := e.offer("c1")
		Expect(line).To(ContainSubstring("schedule"))
	})

	It("never offers during a task or after a refusal, and records nothing then", func() {
		e.busy = true
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.InTask))
		e.busy, e.refused = false, true
		_, why = e.offer("c1")
		Expect(why).To(Equal(hints.AfterRefusal))
		Expect(e.seen()).NotTo(HaveKey("last_offer"))
	})

	It("keeps the other keys of user.json and of seen when it records", func() {
		e.setUser(map[string]any{"name": "Marta", "seen": map[string]any{"home_examples": map[string]any{"hidden": "2026-10-29"}}})
		_, why := e.offer("c1")
		Expect(why).To(Equal(hints.Allowed))
		doc, err := e.wk.Read(context.Background(), alice, wellknown.User, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Marta"))
		Expect(e.seen()).To(HaveKey("home_examples"))
	})

	It("remembers the last 30 chats only", func() {
		for i := range 35 {
			_, why := e.offer("chat-" + strings.Repeat("x", i))
			Expect(why).To(Equal(hints.Allowed))
			e.clk.add(25 * time.Hour)
		}
		Expect(e.seen()["offer_chats"]).To(HaveLen(30))
	})
})
