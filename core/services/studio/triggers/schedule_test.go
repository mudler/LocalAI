package triggers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	Expect(err).NotTo(HaveOccurred(), "time zone %s is not available", name)
	return loc
}

func mustParse(text string) Schedule {
	s, err := ParseSchedule(text)
	Expect(err).NotTo(HaveOccurred(), "ParseSchedule(%q)", text)
	return s
}

func at(value string) time.Time {
	tm, err := time.Parse(time.RFC3339, value)
	Expect(err).NotTo(HaveOccurred())
	return tm
}

var _ = Describe("Schedule", func() {
	It("parses the text and summary of a schedule", func() {
		for _, c := range []struct{ in, text, summary string }{
			{"daily 08:00", "daily 08:00", "Every day at 08:00"},
			{"  DAILY   8:05 ", "daily 08:05", "Every day at 08:05"},
			{"weekdays 07:30", "weekdays 07:30", "Every weekday at 07:30"},
			{"weekly mon 09:00", "weekly mon 09:00", "Every Monday at 09:00"},
			{"weekly thu,mon 09:00", "weekly mon,thu 09:00", "Every Monday and Thursday at 09:00"},
			{"weekly sun,wed,fri 18:45", "weekly wed,fri,sun 18:45", "Every Wednesday, Friday and Sunday at 18:45"},
			{"monthly 15 08:00", "monthly 15 08:00", "On day 15 of every month at 08:00"},
			{"once 2026-12-24 18:00", "once 2026-12-24 18:00", "Once, on 2026-12-24 at 18:00"},
			{"0 8 * * 1-5", "0 8 * * 1-5", `Cron "0 8 * * 1-5"`},
			{"*/15 9-17 * * *", "*/15 9-17 * * *", `Cron "*/15 9-17 * * *"`},
		} {
			s := mustParse(c.in)
			Expect(s.Text()).To(Equal(c.text), "%q: Text()", c.in)
			Expect(s.String()).To(Equal(c.summary), "%q: String()", c.in)
			Expect(mustParse(s.Text()).Text()).To(Equal(s.Text()), "%q: the canonical text must read back", c.in)
		}
		Expect(Schedule{}.String()).To(Equal("Never"), "zero schedule")
	})

	It("refuses a bad schedule", func() {
		for _, c := range []struct{ in, want string }{
			{"", "schedule must be"},
			{"sometimes 08:00", "schedule must be"},
			{"1 2 3", "schedule must be"},
			{"daily", "daily needs one time"},
			{"daily 24:00", "hour must be 0 to 23"},
			{"daily 08:60", "minute must be 0 to 59"},
			{"daily 8", "time must look like HH:MM"},
			{"daily 08:5", "time must look like HH:MM"},
			{"weekly 09:00", "weekly needs days and a time"},
			{"weekly funday 09:00", `day must be one of`},
			{"monthly 0 08:00", "day of month must be 1 to 31"},
			{"monthly 32 08:00", "day of month must be 1 to 31"},
			{"monthly x 08:00", "day of month must be 1 to 31"},
			{"once 2026-02-30 08:00", "date must look like YYYY-MM-DD and exist"},
			{"once tomorrow 08:00", "date must look like YYYY-MM-DD and exist"},
			{"60 * * * *", "minute must be 0 to 59"},
			{"* 24 * * *", "hour must be 0 to 23"},
			{"* * 0 * *", "day of month must be 1 to 31"},
			{"* * * 13 *", "month must be 1 to 12"},
			{"* * * * 8", "day of week must be 0 to 7"},
			{"x * * * *", `minute: cannot read "x"`},
			{"*/0 * * * *", "minute step must be 1 or more"},
			{"*/x * * * *", `minute: cannot read the step "x"`},
			{"30-10 * * * *", "minute range 30-10 starts after it ends"},
			{"0 0 30 2 *", "day of month never occurs in the chosen months"},
			{"0 0 31 4,6,9,11 *", "day of month never occurs in the chosen months"},
			{"daily +8:+5", "hour must be 0 to 23"},
			{"daily 08:-0", "minute must be 0 to 59"},
			{"daily 08:+5", "minute must be 0 to 59"},
			{"monthly +5 08:00", "day of month must be 1 to 31"},
			{"+5 * * * *", `minute: cannot read "+5"`},
			{"-0 * * * *", "minute: cannot read"},
			{"*/+5 * * * *", `minute: cannot read the step "+5"`},
			{"1-+5 * * * *", `minute: cannot read "+5"`},
		} {
			_, err := ParseSchedule(c.in)
			Expect(err).To(HaveOccurred(), "ParseSchedule(%q)", c.in)
			Expect(err.Error()).To(ContainSubstring(c.want), "ParseSchedule(%q)", c.in)
		}
	})

	It("finds the next time", func() {
		utc := time.UTC
		for _, c := range []struct {
			name, sched, after, want string
		}{
			{"daily later today", "daily 08:00", "2026-10-22T07:59:59Z", "2026-10-22T08:00:00Z"},
			{"daily is strictly after", "daily 08:00", "2026-10-22T08:00:00Z", "2026-10-23T08:00:00Z"},
			{"daily across the year", "daily 08:00", "2026-12-31T09:00:00Z", "2027-01-01T08:00:00Z"},
			{"weekdays on a Friday evening", "weekdays 08:00", "2026-10-23T20:00:00Z", "2026-10-26T08:00:00Z"},
			{"weekly picks the next listed day", "weekly mon,thu 09:00", "2026-10-22T10:00:00Z", "2026-10-26T09:00:00Z"},
			{"weekly sunday", "weekly sun 09:00", "2026-10-22T10:00:00Z", "2026-10-25T09:00:00Z"},
			{"monthly 31 skips short months", "monthly 31 08:00", "2026-01-31T08:00:00Z", "2026-03-31T08:00:00Z"},
			{"monthly 30 skips February", "monthly 30 08:00", "2026-01-30T09:00:00Z", "2026-03-30T08:00:00Z"},
			{"monthly 29 in a leap year", "monthly 29 08:00", "2028-01-29T09:00:00Z", "2028-02-29T08:00:00Z"},
			{"monthly 29 in a common year", "monthly 29 08:00", "2026-01-29T09:00:00Z", "2026-03-29T08:00:00Z"},
			{"once in the future", "once 2026-12-24 18:00", "2026-10-22T00:00:00Z", "2026-12-24T18:00:00Z"},
			{"cron every 15 minutes", "*/15 * * * *", "2026-10-22T10:07:00Z", "2026-10-22T10:15:00Z"},
			{"cron every 15 minutes at the hour end", "*/15 * * * *", "2026-10-22T10:45:00Z", "2026-10-22T11:00:00Z"},
			{"cron hours range", "0 9-11 * * *", "2026-10-22T11:30:00Z", "2026-10-23T09:00:00Z"},
			{"cron list of minutes", "5,35 * * * *", "2026-10-22T10:05:00Z", "2026-10-22T10:35:00Z"},
			{"cron step from a start", "10/20 * * * *", "2026-10-22T10:10:00Z", "2026-10-22T10:30:00Z"},
			{"cron 29 February", "0 0 29 2 *", "2026-10-22T00:00:00Z", "2028-02-29T00:00:00Z"},
			{"cron weekday names as numbers, 7 is Sunday", "0 8 * * 7", "2026-10-22T00:00:00Z", "2026-10-25T08:00:00Z"},
			{"cron 0 is Sunday too", "0 8 * * 0", "2026-10-22T00:00:00Z", "2026-10-25T08:00:00Z"},
			{"cron month restriction", "0 0 1 3,9 *", "2026-10-22T00:00:00Z", "2027-03-01T00:00:00Z"},
			// Both day fields restricted: the 13th OR a Friday (2026-10-23 is a Friday).
			{"cron OR rule picks the Friday", "0 0 13 * 5", "2026-10-22T00:00:00Z", "2026-10-23T00:00:00Z"},
			{"cron OR rule picks the 13th", "0 0 13 * 5", "2026-11-12T00:00:00Z", "2026-11-13T00:00:00Z"},
			// One star: the other field alone decides.
			// Starting after Friday 2026-10-23 separates AND from OR: under OR the
			// first match would be Saturday 2026-10-24 (any day) or Sunday 2026-10-25
			// (an odd day), so these answers show the star fields take the AND path.
			{"cron star day of month with a weekday", "0 0 * * 5", "2026-10-23T00:00:00Z", "2026-10-30T00:00:00Z"},
			{"cron star step counts as star", "0 0 */2 * 5", "2026-10-23T00:00:00Z", "2026-11-13T00:00:00Z"},
			// From 2096, the 2100 century year is not a leap year; the next 29 February is 2104.
			{"cron 29 February across the 2100 gap", "0 0 29 2 *", "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z"},
		} {
			got, ok := mustParse(c.sched).Next(at(c.after), utc)
			Expect(ok).To(BeTrue(), "%s: Next(%s)", c.name, c.after)
			Expect(got.Equal(at(c.want))).To(BeTrue(), "%s: Next(%s) = %v, want %s", c.name, c.after, got, c.want)
		}
	})

	It("has no next time when there is none", func() {
		_, ok := mustParse("once 2026-01-01 08:00").Next(at("2026-10-22T00:00:00Z"), time.UTC)
		Expect(ok).To(BeFalse(), "a passed once schedule has a next time")
		_, ok = mustParse("once 2026-01-01 08:00").Next(at("2026-01-01T08:00:00Z"), time.UTC)
		Expect(ok).To(BeFalse(), "a once schedule fires again at its own time")
		_, ok = (Schedule{}).Next(time.Now(), nil)
		Expect(ok).To(BeFalse(), "the zero schedule has a next time")
		// Both day fields restricted, so a day matches on either: every Sunday
		// matches, and the answer is soon. This checks only that a match is found,
		// not the nine-year window; the window is pinned by the 2096 case of the next-time spec.
		got, ok := mustParse("0 0 29 2 0").Next(at("2026-10-22T00:00:00Z"), time.UTC)
		Expect(ok).To(BeTrue(), "29 February or a Sunday")
		Expect(got.Year()).To(BeNumerically("<=", 2035), "29 February or a Sunday: %v", got)
	})

	It("finds the next time in a time zone", func() {
		ny := mustLoc("America/New_York")
		rome := mustLoc("Europe/Rome")
		for _, c := range []struct {
			name, sched string
			loc         *time.Location
			after, want string
		}{
			{"the schedule is read in the zone", "daily 08:00", ny, "2026-10-22T00:00:00Z", "2026-10-22T12:00:00Z"},
			{"the zone's winter offset", "daily 08:00", ny, "2026-12-01T00:00:00Z", "2026-12-01T13:00:00Z"},
			{"Rome in summer", "daily 08:00", rome, "2026-07-01T00:00:00Z", "2026-07-01T06:00:00Z"},
			// New York, 2026-03-08: 02:00 EST jumps to 03:00 EDT. 02:30 does not exist.
			{"a time in the spring gap moves after the gap", "daily 02:30", ny, "2026-03-08T00:00:00Z", "2026-03-08T07:00:00Z"},
			{"the day after the gap is normal", "daily 02:30", ny, "2026-03-08T07:00:00Z", "2026-03-09T06:30:00Z"},
			{"a time after the gap is not moved", "daily 03:30", ny, "2026-03-08T00:00:00Z", "2026-03-08T07:30:00Z"},
			// Rome, 2026-03-29: 02:00 CET jumps to 03:00 CEST.
			{"the gap in Rome", "daily 02:30", rome, "2026-03-29T00:00:00Z", "2026-03-29T01:00:00Z"},
			// New York, 2026-11-01: 02:00 EDT falls back to 01:00 EST. 01:30 occurs twice.
			{"a repeated hour runs at its first occurrence", "daily 01:30", ny, "2026-11-01T00:00:00Z", "2026-11-01T05:30:00Z"},
			{"a repeated hour does not run again", "daily 01:30", ny, "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z"},
			{"after the repeated hour", "daily 01:30", ny, "2026-11-01T06:30:00Z", "2026-11-02T06:30:00Z"},
			// Rome, 2026-10-25: 03:00 CEST falls back to 02:00 CET. 02:30 occurs twice.
			{"a repeated hour in Rome", "daily 02:30", rome, "2026-10-25T00:00:00Z", "2026-10-25T00:30:00Z"},
			{"a cron time in the gap", "30 2 * * *", ny, "2026-03-08T00:00:00Z", "2026-03-08T07:00:00Z"},
			{"every 30 minutes across the gap", "*/30 * * * *", ny, "2026-03-08T06:30:00Z", "2026-03-08T07:00:00Z"},
			{"once in the gap", "once 2026-03-08 02:30", ny, "2026-03-08T00:00:00Z", "2026-03-08T07:00:00Z"},
		} {
			got, ok := mustParse(c.sched).Next(at(c.after), c.loc)
			Expect(ok).To(BeTrue(), "%s: Next(%s)", c.name, c.after)
			Expect(got.Equal(at(c.want))).To(BeTrue(), "%s: Next(%s) = %v, want %s", c.name, c.after, got.UTC(), c.want)
		}
	})

	It("walks a week of every-minute runs fast", func() {
		s := mustParse("* * * * *")
		start := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
		_, n := s.lastDue(start, start.Add(7*24*time.Hour), time.UTC, 100000)
		Expect(n).To(Equal(7*24*60), "a week of minutes")
	})
})
