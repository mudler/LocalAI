// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
)

func s(v string) *string { return &v }
func b(v bool) *bool     { return &v }

var _ = Describe("calendar store", func() {
	var (
		st  *store.Store
		ctx = context.Background()
	)
	BeforeEach(func() {
		var err error
		st, err = store.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		st.Now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) }
		DeferCleanup(func() { _ = st.Close() })
		Expect(st.SetZone(ctx, "ann", "Europe/Rome")).To(Succeed())
	})

	It("creates, reads, changes and deletes an event", func() {
		e, err := st.Create(ctx, "ann", store.Input{Title: s("Dinner with Sam"), Start: s("2026-11-12T20:00"), Place: s("Trattoria")})
		Expect(err).NotTo(HaveOccurred())
		Expect(e.Zone).To(Equal("Europe/Rome"))
		Expect(e.End).To(Equal("2026-11-12T21:00"))
		got, err := st.Get(ctx, "ann", e.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Dinner with Sam"))
		// Moving the start keeps the length.
		up, err := st.Update(ctx, "ann", e.ID, store.Input{Start: s("2026-11-12T19:30")})
		Expect(err).NotTo(HaveOccurred())
		Expect(up.End).To(Equal("2026-11-12T20:30"))
		Expect(st.Delete(ctx, "ann", e.ID)).To(Succeed())
		_, err = st.Get(ctx, "ann", e.ID)
		Expect(err).To(MatchError(store.ErrNotFound))
		Expect(st.Delete(ctx, "ann", e.ID)).To(MatchError(store.ErrNotFound))
	})

	It("keeps one owner's events from another owner", func() {
		e, err := st.Create(ctx, "ann", store.Input{Title: s("Private"), Start: s("2026-11-12T09:00")})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.Get(ctx, "bob", e.ID)
		Expect(err).To(MatchError(store.ErrNotFound))
		occ, _, err := st.List(ctx, "bob", "2026-11-01", "2026-11-30", "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(occ).To(BeEmpty())
		Expect(st.Delete(ctx, "bob", e.ID)).To(MatchError(store.ErrNotFound))
		z, _ := st.Zone(ctx, "bob")
		Expect(z).NotTo(Equal("Europe/Rome"))
	})

	It("refuses bad input", func() {
		for _, in := range []store.Input{
			{Title: s(""), Start: s("2026-11-12T09:00")},
			{Title: s("x"), Start: s("tomorrow")},
			{Title: s("x"), Start: s("2026-11-12")},
			{Title: s("x"), Start: s("2026-11-12T09:00"), End: s("2026-11-12T08:00")},
			{Title: s("x"), Start: s("2026-11-12T09:00"), Zone: s("Mars/Base")},
			{Title: s("x"), Start: s("2026-11-12T09:00"), Zone: s("Local")},
			{Title: s("x"), Start: s("2026-11-12T09:00"), Repeat: &store.Repeat{Freq: "hourly"}},
			{Title: s("x"), Start: s("2026-11-12T09:00"), Repeat: &store.Repeat{Freq: "daily", Interval: 9999}},
			{Title: s(strings.Repeat("a", 201)), Start: s("2026-11-12T09:00")},
		} {
			_, err := st.Create(ctx, "ann", in)
			Expect(err).To(MatchError(store.ErrInvalid))
		}
	})

	It("lists a day, with all-day events and a range in another zone", func() {
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Late"), Start: s("2026-11-12T23:30"), End: s("2026-11-13T00:30")})
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Holiday"), Start: s("2026-11-12"), AllDay: b(true)})
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Early"), Start: s("2026-11-12T08:00")})
		occ, more, err := st.List(ctx, "ann", "2026-11-12", "2026-11-12", "Europe/Rome")
		Expect(err).NotTo(HaveOccurred())
		Expect(more).To(BeFalse())
		var titles []string
		for _, o := range occ {
			titles = append(titles, o.Title)
		}
		Expect(titles).To(Equal([]string{"Holiday", "Early", "Late"}))
		Expect(occ[0].Weekday).To(Equal("Thursday"))
		// 23:30 in Rome is 07:30 on the next day in Tokyo.
		occ, _, _ = st.List(ctx, "ann", "2026-11-13", "2026-11-13", "Asia/Tokyo")
		Expect(occ).To(HaveLen(1))
		Expect(occ[0].Title).To(Equal("Late"))
	})

	It("expands repeats with an interval and an end date", func() {
		e, err := st.Create(ctx, "ann", store.Input{Title: s("Standup"), Start: s("2026-11-02T09:00"), Repeat: &store.Repeat{Freq: "weekly", Interval: 2, Until: "2026-12-31"}})
		Expect(err).NotTo(HaveOccurred())
		occ, _, _ := st.List(ctx, "ann", "2026-11-01", "2027-01-31", "Europe/Rome")
		var days []string
		for _, o := range occ {
			days = append(days, o.Occurrence)
		}
		Expect(days).To(Equal([]string{"2026-11-02", "2026-11-16", "2026-11-30", "2026-12-14", "2026-12-28"}))
		Expect(occ[0].ID).To(Equal(e.ID))
	})

	It("skips one occurrence and keeps the others", func() {
		e, _ := st.Create(ctx, "ann", store.Input{Title: s("Gym"), Start: s("2026-11-02T18:00"), Repeat: &store.Repeat{Freq: "daily"}})
		Expect(st.SkipOccurrence(ctx, "ann", e.ID, "2026-11-03")).To(Succeed())
		occ, _, _ := st.List(ctx, "ann", "2026-11-02", "2026-11-04", "Europe/Rome")
		Expect(occ).To(HaveLen(2))
		Expect(occ[1].Occurrence).To(Equal("2026-11-04"))
		Expect(st.SkipOccurrence(ctx, "ann", e.ID, "2026-11-03x")).To(MatchError(store.ErrInvalid))
		Expect(st.SkipOccurrence(ctx, "ann", e.ID, "2025-01-01")).To(MatchError(store.ErrInvalid))
		single, _ := st.Create(ctx, "ann", store.Input{Title: s("Once"), Start: s("2026-11-08T10:00")})
		Expect(st.SkipOccurrence(ctx, "ann", single.ID, "2026-11-08")).To(MatchError(store.ErrInvalid))
		// Deleting the series drops its skips.
		Expect(st.Delete(ctx, "ann", e.ID)).To(Succeed())
		sk, _ := st.Skipped(ctx, "ann", e.ID)
		Expect(sk).To(BeEmpty())
	})

	It("keeps the local time of a repeat across daylight saving time", func() {
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Call"), Start: s("2026-10-22T20:00"), Repeat: &store.Repeat{Freq: "weekly"}})
		// Rome leaves summer time on 2026-10-25.
		occ, _, _ := st.List(ctx, "ann", "2026-10-22", "2026-10-30", "Europe/Rome")
		Expect(occ).To(HaveLen(2))
		for _, o := range occ {
			Expect(o.Start).To(HaveSuffix("T20:00"))
		}
	})

	It("skips months that lack the day", func() {
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Rent"), Start: s("2026-01-31T09:00"), Repeat: &store.Repeat{Freq: "monthly"}})
		occ, _, _ := st.List(ctx, "ann", "2026-01-01", "2026-06-30", "UTC")
		var days []string
		for _, o := range occ {
			days = append(days, o.Occurrence)
		}
		Expect(days).To(Equal([]string{"2026-01-31", "2026-03-31", "2026-05-31"}))
		_, _ = st.Create(ctx, "dee", store.Input{Title: s("Leap"), Start: s("2024-02-29T09:00"), Repeat: &store.Repeat{Freq: "yearly"}})
		occ, _, _ = st.List(ctx, "dee", "2028-01-01", "2028-12-31", "UTC")
		Expect(occ).To(HaveLen(1))
		Expect(occ[0].Occurrence).To(Equal("2028-02-29"))
	})

	It("finds a repeat that started long ago", func() {
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Old"), Start: s("2010-01-04T09:00"), Repeat: &store.Repeat{Freq: "weekly"}})
		occ, _, err := st.List(ctx, "ann", "2026-11-09", "2026-11-15", "Europe/Rome")
		Expect(err).NotTo(HaveOccurred())
		Expect(occ).To(HaveLen(1))
		Expect(occ[0].Occurrence).To(Equal("2026-11-09"))
	})

	It("refuses a range that is too long", func() {
		_, _, err := st.List(ctx, "ann", "2020-01-01", "2026-01-01", "UTC")
		Expect(err).To(MatchError(store.ErrInvalid))
	})

	It("searches text, upcoming first", func() {
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Dentist"), Start: s("2026-03-01T09:00"), Notes: s("check-up")})
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Dentist again"), Start: s("2026-12-01T09:00"), Attendees: s("Dr Rossi")})
		hits, err := st.Search(ctx, "ann", "DENTIST", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(2))
		Expect(hits[0].Event.Title).To(Equal("Dentist again"))
		Expect(hits[0].Next).To(Equal("2026-12-01T09:00"))
		Expect(hits[1].Next).To(BeEmpty())
		hits, _ = st.Search(ctx, "ann", "dentist rossi", 10)
		Expect(hits).To(HaveLen(1))
		_, err = st.Search(ctx, "ann", "  ", 10)
		Expect(err).To(MatchError(store.ErrInvalid))
	})

	It("writes one iCalendar file", func() {
		e, _ := st.Create(ctx, "ann", store.Input{Title: s("Plan, review; go"), Start: s("2026-11-02T09:00"), Zone: s("Europe/Rome"), Repeat: &store.Repeat{Freq: "weekly", Until: "2026-12-01"}})
		_ = st.SkipOccurrence(ctx, "ann", e.ID, "2026-11-09")
		_, _ = st.Create(ctx, "ann", store.Input{Title: s("Trip"), Start: s("2026-11-20"), End: s("2026-11-22"), AllDay: b(true)})
		_, _ = st.Create(ctx, "bob", store.Input{Title: s("Not mine"), Start: s("2026-11-20T10:00")})
		out, err := st.ICS(ctx, "ann")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("BEGIN:VCALENDAR\r\n"))
		Expect(out).To(HaveSuffix("END:VCALENDAR\r\n"))
		Expect(out).To(ContainSubstring(`SUMMARY:Plan\, review\; go`))
		Expect(out).To(ContainSubstring("DTSTART;TZID=Europe/Rome:20261102T090000"))
		Expect(out).To(ContainSubstring("RRULE:FREQ=WEEKLY;INTERVAL=1;UNTIL=20261201T"))
		Expect(out).To(ContainSubstring("EXDATE;TZID=Europe/Rome:20261109T090000"))
		Expect(out).To(ContainSubstring("DTSTART;VALUE=DATE:20261120"))
		Expect(out).To(ContainSubstring("DTEND;VALUE=DATE:20261123"))
		Expect(out).NotTo(ContainSubstring("Not mine"))
		for _, l := range strings.Split(out, "\r\n") {
			Expect(len(l)).To(BeNumerically("<=", 75))
		}
	})

	It("keeps the zone setting per owner and refuses a bad zone", func() {
		Expect(st.SetZone(ctx, "ann", "Nowhere/Land")).To(MatchError(store.ErrInvalid))
		z, _ := st.Zone(ctx, "ann")
		Expect(z).To(Equal("Europe/Rome"))
	})

	It("stops at the most events", func() {
		for i := 0; i < store.MaxEvents; i++ {
			_, err := st.Create(ctx, "carl", store.Input{Title: s("e"), Start: s("2026-11-12T09:00")})
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := st.Create(ctx, "carl", store.Input{Title: s("e"), Start: s("2026-11-12T09:00")})
		Expect(err).To(MatchError(store.ErrFull))
	})
})
