package pages_test

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var _ = Describe("the runtime errors of a page", func() {
	var (
		ctx   = context.Background()
		svc   *pages.Service
		f     *files.Service
		clock time.Time
	)
	BeforeEach(func() {
		clock = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
		f = newFiles()
		for _, u := range []string{"alice", "bob"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		put(f, "alice", "/pages/tasks/index.html", "<p>tasks</p>")
		put(f, "alice", "/pages/clock/index.html", "<p>clock</p>")
		put(f, "bob", "/pages/tasks/index.html", "<p>bob</p>")
		svc = pages.New(f, pages.WithClock(func() time.Time { return clock }))
	})
	tick := func(d time.Duration) { clock = clock.Add(d) }
	boom := func(msg string, line int) pages.ErrorReport {
		return pages.ErrorReport{Message: msg, Line: line, Column: 3}
	}

	It("keeps an error with its place and counts the same error once", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("ReferenceError: x is not defined", 12))).Error().NotTo(HaveOccurred())
		tick(time.Minute)
		Expect(svc.RecordError(ctx, "alice", "tasks", pages.ErrorReport{Message: "ReferenceError: x is not defined", Line: 12, Column: 3, Count: 40})).Error().NotTo(HaveOccurred())
		got, err := svc.Errors(ctx, "alice", "tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Message).To(Equal("ReferenceError: x is not defined"))
		Expect(got[0].Line).To(Equal(12))
		Expect(got[0].Column).To(Equal(3))
		Expect(got[0].Count).To(Equal(41))
		Expect(got[0].First).To(Equal(clock.Add(-time.Minute)))
		Expect(got[0].Last).To(Equal(clock))
	})

	It("tells the same message at another line apart", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("TypeError: y", 1))).Error().NotTo(HaveOccurred())
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("TypeError: y", 2))).Error().NotTo(HaveOccurred())
		got, _ := svc.Errors(ctx, "alice", "tasks")
		Expect(got).To(HaveLen(2))
	})

	It("keeps at most 10 errors and drops the oldest", func() {
		for i := range 14 {
			tick(time.Second)
			Expect(svc.RecordError(ctx, "alice", "tasks", boom("error number "+string(rune('a'+i)), 1))).Error().NotTo(HaveOccurred())
			if i%5 == 4 {
				tick(time.Minute) // keep the burst limit away
			}
		}
		got, err := svc.Errors(ctx, "alice", "tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(pages.MaxErrors))
		Expect(got[0].Message).To(Equal("error number n"), "newest first")
		for _, e := range got {
			Expect(e.Message).NotTo(Equal("error number a"))
		}
	})

	It("cuts long texts and drops control characters", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", pages.ErrorReport{
			Message: strings.Repeat("é", 2000) + "\x00",
			Stack:   strings.Repeat("at f (x.js:1:1)\n", 500),
		})).Error().NotTo(HaveOccurred())
		got, _ := svc.Errors(ctx, "alice", "tasks")
		Expect(got).To(HaveLen(1))
		Expect(len(got[0].Message)).To(BeNumerically("<=", pages.MaxErrorMessage))
		Expect(got[0].Message).NotTo(ContainSubstring("\x00"))
		Expect(len(got[0].Stack)).To(BeNumerically("<=", pages.MaxErrorStack))
		Expect(got[0].Stack).To(ContainSubstring("\n"), "a stack keeps its lines")
	})

	It("drops the browser's own noise and refuses an empty report", func() {
		for _, msg := range []string{
			"ResizeObserver loop limit exceeded",
			"ResizeObserver loop completed with undelivered notifications.",
			"Script error.",
		} {
			recorded, err := svc.RecordError(ctx, "alice", "tasks", boom(msg, 1))
			Expect(err).NotTo(HaveOccurred())
			Expect(recorded).To(BeFalse(), msg)
		}
		got, _ := svc.Errors(ctx, "alice", "tasks")
		Expect(got).To(BeEmpty())
		Expect(svc.RecordError(ctx, "alice", "tasks", pages.ErrorReport{Message: "  \n "})).Error().To(MatchError(pages.ErrBadError))
	})

	It("limits a page that throws on every frame", func() {
		var limited int
		for i := range 200 {
			_, err := svc.RecordError(ctx, "alice", "tasks", boom("loop "+string(rune('a'+i%26)), i))
			if err != nil {
				Expect(err).To(MatchError(pages.ErrRateLimited))
				limited++
			}
		}
		Expect(limited).To(BeNumerically(">", 150))
		tick(10 * time.Second)
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("later", 1))).Error().NotTo(HaveOccurred())
	})

	It("clears the errors of a page when any of its files changes, and only that page", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("broken", 1))).Error().NotTo(HaveOccurred())
		Expect(svc.RecordError(ctx, "alice", "clock", boom("also broken", 1))).Error().NotTo(HaveOccurred())
		got, _ := svc.Errors(ctx, "alice", "tasks")
		Expect(got).To(HaveLen(1))

		Expect(svc.Write(ctx, "alice", "tasks", "data.json", []byte("[]"), 0)).Error().NotTo(HaveOccurred())
		got, err := svc.Errors(ctx, "alice", "tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty(), "a file of the page changed")
		other, _ := svc.Errors(ctx, "alice", "clock")
		Expect(other).To(HaveLen(1), "another page keeps its errors")

		// A new error after the change is kept again.
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("fresh", 1))).Error().NotTo(HaveOccurred())
		got, _ = svc.Errors(ctx, "alice", "tasks")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Message).To(Equal("fresh"))
	})

	It("clears them when the code of the page is rewritten", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("broken", 1))).Error().NotTo(HaveOccurred())
		put(f, "alice", "/pages/tasks/index.html", "<p>fixed</p>")
		got, _ := svc.Errors(ctx, "alice", "tasks")
		Expect(got).To(BeEmpty())
		list, err := svc.ErrorPages(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("keeps each user's errors apart, and records only for the owner's own page", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("alice broke it", 1))).Error().NotTo(HaveOccurred())
		got, _ := svc.Errors(ctx, "bob", "tasks")
		Expect(got).To(BeEmpty(), "bob has a page of the same name")
		Expect(svc.RecordError(ctx, "bob", "clock", boom("not bob's page", 1))).Error().To(MatchError(pages.ErrNotFound))
		Expect(svc.RecordError(ctx, "bob", "Bad Name", boom("x", 1))).Error().To(MatchError(pages.ErrBadName))
		_, err := svc.Errors(ctx, "bob", "clock")
		Expect(err).To(MatchError(pages.ErrNotFound))
		list, _ := svc.ErrorPages(ctx, "bob")
		Expect(list).To(BeEmpty())
	})

	It("lists the pages that have errors, the newest first", func() {
		Expect(svc.RecordError(ctx, "alice", "tasks", boom("one", 1))).Error().NotTo(HaveOccurred())
		tick(time.Hour)
		Expect(svc.RecordError(ctx, "alice", "clock", pages.ErrorReport{Message: "two", Line: 1, Count: 5})).Error().NotTo(HaveOccurred())
		list, err := svc.ErrorPages(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].Page).To(Equal("clock"))
		Expect(list[0].Count).To(Equal(5))
		Expect(list[1].Page).To(Equal("tasks"))
	})

	Describe("the stores", func() {
		run := func(mk func() pages.ErrorStore) {
			st := mk()
			set := pages.ErrorSet{Rev: 4, Errors: []pages.PageError{{Message: "m", Count: 2, First: clock, Last: clock}}}
			Expect(st.Update(ctx, "alice", "tasks", func(pages.ErrorSet) pages.ErrorSet { return set })).To(Succeed())
			got, err := st.Get(ctx, "alice", "tasks")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Rev).To(Equal(int64(4)))
			Expect(got.Errors).To(HaveLen(1))
			Expect(got.Errors[0].Message).To(Equal("m"))
			other, _ := st.Get(ctx, "bob", "tasks")
			Expect(other.Errors).To(BeEmpty())
			all, _ := st.All(ctx, "alice")
			Expect(all).To(HaveKey("tasks"))
			Expect(st.Update(ctx, "alice", "tasks", func(pages.ErrorSet) pages.ErrorSet { return pages.ErrorSet{} })).To(Succeed())
			all, _ = st.All(ctx, "alice")
			Expect(all).To(BeEmpty())
		}
		It("keeps them in memory", func() { run(func() pages.ErrorStore { st, _ := pages.NewJSONErrors(""); return st }) })
		It("keeps them in a file that a restart reads", func() {
			dir := GinkgoT().TempDir()
			st, err := pages.NewJSONErrors(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Update(ctx, "alice", "tasks", func(pages.ErrorSet) pages.ErrorSet {
				return pages.ErrorSet{Rev: 1, Errors: []pages.PageError{{Message: "kept", Count: 1}}}
			})).To(Succeed())
			again, err := pages.NewJSONErrors(dir)
			Expect(err).NotTo(HaveOccurred())
			got, _ := again.Get(ctx, "alice", "tasks")
			Expect(got.Errors).To(HaveLen(1))
			run(func() pages.ErrorStore { st, _ := pages.NewJSONErrors(GinkgoT().TempDir()); return st })
		})
		It("keeps them in the database", func() {
			run(func() pages.ErrorStore {
				db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "e.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
				Expect(err).NotTo(HaveOccurred())
				st, err := pages.NewGORMErrors(db)
				Expect(err).NotTo(HaveOccurred())
				return st
			})
		})
	})
})
