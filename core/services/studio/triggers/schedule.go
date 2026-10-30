package triggers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed schedule text. The zero value never fires.
type Schedule struct {
	kind  string // "daily", "weekdays", "weekly", "monthly", "once" or "cron"
	text  string // the canonical text
	mins  []int
	hours []int
	dom   uint32 // bit d for day d, 1 to 31
	month uint16 // bit m for month m, 1 to 12
	dow   uint8  // bit d for weekday d, 0 is Sunday
	// domStar and dowStar are true when the cron field starts with "*"; the
	// standard cron rule joins the two day fields with OR only when neither
	// does.
	domStar, dowStar bool
	// once is the wall-clock date and time of a "once" schedule.
	onceY, onceM, onceD int
}

var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

var dayWords = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

const scheduleHelp = `schedule must be "daily HH:MM", "weekdays HH:MM", "weekly DAY[,DAY] HH:MM", "monthly N HH:MM", "once YYYY-MM-DD HH:MM", or five cron fields`

// ParseSchedule reads the text of a schedule. The forms are in the package
// documentation of Schedule.Next. Words and day names are not case sensitive.
func ParseSchedule(text string) (Schedule, error) {
	f := strings.Fields(strings.ToLower(text))
	if len(f) == 0 {
		return Schedule{}, errors.New(scheduleHelp)
	}
	var s Schedule
	var err error
	switch f[0] {
	case "daily", "weekdays":
		if len(f) != 2 {
			return Schedule{}, fmt.Errorf("%s needs one time: %q", f[0], f[0]+" 08:00")
		}
		s.kind = f[0]
		s.dom, s.month = allDays, allMonths
		s.domStar, s.dowStar = true, true
		s.dow = 0x7f
		if f[0] == "weekdays" {
			s.dow, s.dowStar = 0x3e, false
		}
		err = s.setTime(f[1])
	case "weekly":
		if len(f) != 3 {
			return Schedule{}, fmt.Errorf("weekly needs days and a time: %q", "weekly mon,thu 09:30")
		}
		s.kind = "weekly"
		s.dom, s.month, s.domStar = allDays, allMonths, true
		for _, d := range strings.Split(f[1], ",") {
			i := dayIndex(d)
			if i < 0 {
				return Schedule{}, fmt.Errorf("day must be one of %s, not %q", strings.Join(dayNames[1:], ", ")+", sun", d)
			}
			s.dow |= 1 << uint(i)
		}
		err = s.setTime(f[2])
	case "monthly":
		if len(f) != 3 {
			return Schedule{}, fmt.Errorf("monthly needs a day and a time: %q", "monthly 15 08:00")
		}
		s.kind = "monthly"
		n, e := atoiDigits(f[1])
		if e != nil || n < 1 || n > 31 {
			return Schedule{}, errors.New("day of month must be 1 to 31")
		}
		s.dom, s.month, s.dow, s.dowStar = 1<<uint(n), allMonths, 0x7f, true
		err = s.setTime(f[2])
	case "once":
		if len(f) != 3 {
			return Schedule{}, fmt.Errorf("once needs a date and a time: %q", "once 2026-12-24 18:00")
		}
		s.kind = "once"
		d, e := time.Parse("2006-01-02", f[1])
		if e != nil {
			return Schedule{}, errors.New("date must look like YYYY-MM-DD and exist")
		}
		s.onceY, s.onceM, s.onceD = d.Year(), int(d.Month()), d.Day()
		err = s.setTime(f[2])
	default:
		if len(f) != 5 {
			return Schedule{}, errors.New(scheduleHelp)
		}
		s, err = parseCron(f)
	}
	if err != nil {
		return Schedule{}, err
	}
	s.text = s.canonical(f)
	return s, nil
}

// atoiDigits reads a number that has digits only: strconv.Atoi would also
// accept a sign.
func atoiDigits(s string) (int, error) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%q is not a number", s)
		}
	}
	return strconv.Atoi(s)
}

const (
	allDays   uint32 = 0xfffffffe // bits 1 to 31
	allMonths uint16 = 0x1ffe     // bits 1 to 12
)

func dayIndex(name string) int {
	for i, n := range dayNames {
		if n == name {
			return i
		}
	}
	return -1
}

func (s *Schedule) setTime(text string) error {
	h, m, ok := strings.Cut(text, ":")
	if !ok || len(m) != 2 || h == "" {
		return errors.New("time must look like HH:MM")
	}
	hh, e1 := atoiDigits(h)
	mm, e2 := atoiDigits(m)
	if e1 != nil || hh > 23 {
		return errors.New("hour must be 0 to 23")
	}
	if e2 != nil || mm > 59 {
		return errors.New("minute must be 0 to 59")
	}
	s.hours, s.mins = []int{hh}, []int{mm}
	return nil
}

func parseCron(f []string) (Schedule, error) {
	s := Schedule{kind: "cron"}
	var v uint64
	var star bool
	var err error
	if v, _, err = cronField("minute", f[0], 0, 59); err != nil {
		return Schedule{}, err
	}
	s.mins = bits(v, 0, 59)
	if v, _, err = cronField("hour", f[1], 0, 23); err != nil {
		return Schedule{}, err
	}
	s.hours = bits(v, 0, 23)
	if v, star, err = cronField("day of month", f[2], 1, 31); err != nil {
		return Schedule{}, err
	}
	s.dom, s.domStar = uint32(v), star
	if v, _, err = cronField("month", f[3], 1, 12); err != nil {
		return Schedule{}, err
	}
	s.month = uint16(v)
	if v, star, err = cronField("day of week", f[4], 0, 7); err != nil {
		return Schedule{}, err
	}
	if v&(1<<7) != 0 { // 7 is Sunday too
		v |= 1
	}
	s.dow, s.dowStar = uint8(v&0x7f), star
	if !s.domStar && s.dowStar && !monthHasDay(s.month, s.dom) {
		return Schedule{}, errors.New("day of month never occurs in the chosen months")
	}
	return s, nil
}

// monthHasDay tells whether some chosen month has one of the chosen days.
func monthHasDay(months uint16, days uint32) bool {
	longest := [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		if months&(1<<uint(m)) == 0 {
			continue
		}
		for d := 1; d <= longest[m]; d++ {
			if days&(1<<uint(d)) != 0 {
				return true
			}
		}
	}
	return false
}

func bits(v uint64, lo, hi int) []int {
	var out []int
	for i := lo; i <= hi; i++ {
		if v&(1<<uint(i)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// cronField reads one cron field: a comma list of "*", "a", "a-b", each with
// an optional "/step". It returns the set of values as bits, and whether the
// field starts with "*".
func cronField(name, text string, lo, hi int) (uint64, bool, error) {
	var set uint64
	for _, part := range strings.Split(text, ",") {
		base, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := atoiDigits(stepText)
			if err != nil {
				return 0, false, fmt.Errorf("%s: cannot read the step %q", name, stepText)
			}
			if n < 1 {
				return 0, false, fmt.Errorf("%s step must be 1 or more", name)
			}
			step = n
		}
		from, to := lo, hi
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			a, b, _ := strings.Cut(base, "-")
			var err error
			if from, err = cronNumber(name, a, lo, hi); err != nil {
				return 0, false, err
			}
			if to, err = cronNumber(name, b, lo, hi); err != nil {
				return 0, false, err
			}
			if from > to {
				return 0, false, fmt.Errorf("%s range %s starts after it ends", name, base)
			}
		default:
			n, err := cronNumber(name, base, lo, hi)
			if err != nil {
				return 0, false, err
			}
			from = n
			if !hasStep {
				to = n
			}
		}
		for v := from; v <= to; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, strings.HasPrefix(text, "*"), nil
}

func cronNumber(name, text string, lo, hi int) (int, error) {
	n, err := atoiDigits(text)
	if err != nil {
		return 0, fmt.Errorf("%s: cannot read %q, use a number from %d to %d", name, text, lo, hi)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%s must be %d to %d", name, lo, hi)
	}
	return n, nil
}

// canonical gives the normalised text of the schedule.
func (s Schedule) canonical(f []string) string {
	hm := fmt.Sprintf("%02d:%02d", s.hours[0], s.mins[0])
	switch s.kind {
	case "daily", "weekdays":
		return s.kind + " " + hm
	case "weekly":
		var days []string
		for _, i := range s.weekdays() {
			days = append(days, dayNames[i])
		}
		return "weekly " + strings.Join(days, ",") + " " + hm
	case "monthly":
		return fmt.Sprintf("monthly %d %s", firstBit(uint64(s.dom)), hm)
	case "once":
		return fmt.Sprintf("once %04d-%02d-%02d %s", s.onceY, s.onceM, s.onceD, hm)
	}
	return strings.Join(f, " ")
}

func firstBit(v uint64) int {
	for i := 0; i < 64; i++ {
		if v&(1<<uint(i)) != 0 {
			return i
		}
	}
	return 0
}

// weekdays lists the weekdays of the set, Monday first.
func (s Schedule) weekdays() []int {
	var out []int
	for _, i := range []int{1, 2, 3, 4, 5, 6, 0} {
		if s.dow&(1<<uint(i)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// Text gives the canonical text of the schedule, which ParseSchedule reads
// back to the same schedule.
func (s Schedule) Text() string { return s.text }

// String gives a plain English summary, for example "Every weekday at 08:00".
func (s Schedule) String() string {
	if s.kind == "" {
		return "Never"
	}
	hm := func() string { return fmt.Sprintf("%02d:%02d", s.hours[0], s.mins[0]) }
	switch s.kind {
	case "daily":
		return "Every day at " + hm()
	case "weekdays":
		return "Every weekday at " + hm()
	case "weekly":
		var names []string
		for _, i := range s.weekdays() {
			names = append(names, dayWords[i])
		}
		return "Every " + joinWords(names) + " at " + hm()
	case "monthly":
		return fmt.Sprintf("On day %d of every month at %s", firstBit(uint64(s.dom)), hm())
	case "once":
		return fmt.Sprintf("Once, on %04d-%02d-%02d at %s", s.onceY, s.onceM, s.onceD, hm())
	}
	return fmt.Sprintf("Cron %q", s.text)
}

func joinWords(w []string) string {
	switch len(w) {
	case 0:
		return ""
	case 1:
		return w[0]
	}
	return strings.Join(w[:len(w)-1], ", ") + " and " + w[len(w)-1]
}

// maxYears is how far Next looks for a day that matches.
const maxYears = 9

// Next gives the first time after the given time at which the schedule fires,
// read in loc (nil means UTC). It reports false when there is none: a "once"
// schedule that has passed, or a cron schedule with no day in the next nine
// years (29 February needs up to eight).
//
// A wall-clock time that does not exist in loc (the gap of a daylight-saving
// change) moves to the first minute after the gap. A time that occurs twice
// (the repeated hour) fires once, at its first occurrence.
//
// The forms are: "daily HH:MM"; "weekdays HH:MM" (Monday to Friday);
// "weekly DAY[,DAY...] HH:MM" with days mon to sun; "monthly N HH:MM" (day N,
// 1 to 31, of each month; a month without day N is skipped);
// "once YYYY-MM-DD HH:MM"; and five cron fields (minute, hour, day of month,
// month, day of week) with "*", lists, ranges and steps, where 0 and 7 are
// Sunday. When both day fields of a cron schedule are restricted, a day
// matches if either field matches.
func (s Schedule) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	if s.kind == "" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	after = after.In(loc)
	y, m, d := after.Date()
	if s.kind == "once" {
		t := wall(s.onceY, time.Month(s.onceM), s.onceD, s.hours[0], s.mins[0], loc)
		return t, t.After(after)
	}
	for i := 0; i <= 366*maxYears; i++ {
		day := time.Date(y, m, d+i, 0, 0, 0, 0, time.UTC)
		if !s.matchDay(day) {
			continue
		}
		cy, cm, cd := day.Date()
		for _, h := range s.hours {
			if i == 0 && h < after.Hour()-3 {
				continue // far before after, and a gap moves a time by hours at most
			}
			for _, mi := range s.mins {
				t := wall(cy, cm, cd, h, mi, loc)
				if t.After(after) {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

func (s Schedule) matchDay(day time.Time) bool {
	if s.month&(1<<uint(day.Month())) == 0 {
		return false
	}
	domOK := s.dom&(1<<uint(day.Day())) != 0
	dowOK := s.dow&(1<<uint(day.Weekday())) != 0
	if !s.domStar && !s.dowStar {
		return domOK || dowOK
	}
	return domOK && dowOK
}

// wall gives the instant of a wall-clock time in loc, with the rules for a gap
// and for a repeated hour that Next describes.
func wall(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	t := time.Date(y, mo, d, h, mi, 0, 0, loc)
	same := func(u time.Time) bool {
		uy, um, ud := u.Date()
		return uy == y && um == mo && ud == d && u.Hour() == h && u.Minute() == mi
	}
	if !same(t) { // the time does not exist
		for k := 1; k <= 24*60; k++ {
			w := time.Date(y, mo, d, h, mi+k, 0, 0, time.UTC)
			u := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), 0, 0, loc)
			uy, um, ud := u.Date()
			if uy == w.Year() && um == w.Month() && ud == w.Day() && u.Hour() == w.Hour() && u.Minute() == w.Minute() {
				return u
			}
		}
		return t
	}
	best := t
	for k := 1; k <= 6; k++ { // a repeated hour: take the first occurrence
		if u := t.Add(-time.Duration(k) * 30 * time.Minute); same(u) && u.Before(best) {
			best = u
		}
	}
	return best
}

// lastDue counts the times in (after, upTo] and gives the last of them.
// It stops counting at limit times.
func (s Schedule) lastDue(after, upTo time.Time, loc *time.Location, limit int) (last time.Time, n int) {
	t := after
	for n < limit {
		next, ok := s.Next(t, loc)
		if !ok || next.After(upTo) {
			break
		}
		last, t = next, next
		n++
	}
	return last, n
}

// gapSamples is how many next times MinGap looks at: enough to see a burst of
// a cron schedule (minutes or hours of one day) between sparse days.
const gapSamples = 120

// MinGap is the shortest time between two runs of the schedule, looking at
// the next runs after from in loc. A schedule that runs once, or not at all
// any more, has no gap: MinGap returns 0.
func (s Schedule) MinGap(from time.Time, loc *time.Location) time.Duration {
	prev, ok := s.Next(from, loc)
	if !ok {
		return 0
	}
	var gap time.Duration
	for i := 1; i < gapSamples; i++ {
		next, ok := s.Next(prev, loc)
		if !ok {
			break
		}
		if d := next.Sub(prev); gap == 0 || d < gap {
			gap = d
		}
		prev = next
	}
	return gap
}
