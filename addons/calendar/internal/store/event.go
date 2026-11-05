// SPDX-License-Identifier: MIT

// Package store keeps the calendar of each owner in one SQLite file.
//
// Times are kept the way a person writes them: a local date and time with the
// name of an IANA zone. A repeating event keeps its local time across a change
// of daylight saving time, so "every Thursday at 20:00" stays at 20:00.
package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // the zone names work on a host without a zone database
	"unicode/utf8"
)

// The layouts of a local start or end. An all-day event has a date only.
const (
	dateLayout = "2006-01-02"
	timeLayout = "2006-01-02T15:04"
)

// Bounds of an event. They keep one owner's calendar and one answer small.
const (
	MaxTitle     = 200
	MaxNotes     = 4000
	MaxPlace     = 200
	MaxAttendees = 500
	MaxEvents    = 5000
	MaxInterval  = 366
	// MaxRangeDays is the longest range one listing expands.
	MaxRangeDays = 400
	// MaxOccurrences is the most occurrences one listing returns.
	MaxOccurrences = 500
)

// Repeat frequencies.
const (
	Daily   = "daily"
	Weekly  = "weekly"
	Monthly = "monthly"
	Yearly  = "yearly"
)

// Errors a caller can show to a person. Each is a problem with the request.
var (
	ErrInvalid  = errors.New("invalid event")
	ErrNotFound = errors.New("no such event")
	ErrFull     = errors.New("the calendar holds the most events it can")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Repeat says how an event comes again: every Interval days, weeks, months or
// years from its start, until the last day Until (a date, empty for no end).
type Repeat struct {
	Freq     string `json:"freq"`
	Interval int    `json:"interval,omitempty"`
	Until    string `json:"until,omitempty"`
}

// Event is one entry of the calendar. Start and End are local to Zone: a date
// for an all-day event (End is its last day) and a date and time otherwise.
type Event struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Start     string  `json:"start"`
	End       string  `json:"end"`
	AllDay    bool    `json:"allDay"`
	Zone      string  `json:"zone"`
	Notes     string  `json:"notes,omitempty"`
	Place     string  `json:"place,omitempty"`
	Attendees string  `json:"attendees,omitempty"`
	Repeat    *Repeat `json:"repeat,omitempty"`
	Created   string  `json:"created,omitempty"`
	Updated   string  `json:"updated,omitempty"`
}

// Input is what a person or a model gives to create or change an event. A nil
// field on a change keeps the value. A repeat with an empty Freq removes the
// repeat.
type Input struct {
	Title     *string `json:"title,omitempty"`
	Start     *string `json:"start,omitempty"`
	End       *string `json:"end,omitempty"`
	AllDay    *bool   `json:"allDay,omitempty"`
	Zone      *string `json:"zone,omitempty"`
	Notes     *string `json:"notes,omitempty"`
	Place     *string `json:"place,omitempty"`
	Attendees *string `json:"attendees,omitempty"`
	Repeat    *Repeat `json:"repeat,omitempty"`
}

// LoadZone returns the zone of an IANA name. "Local" and the empty name are
// refused, because they mean a different place on every host.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || strings.EqualFold(name, "local") || len(name) > 64 {
		return nil, invalid("the zone must be an IANA name such as Europe/Rome")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalid("%q is not an IANA zone name such as Europe/Rome", name)
	}
	return loc, nil
}

// ParseDate reads a date such as 2026-11-12.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, invalid("%q is not a date written YYYY-MM-DD", s)
	}
	return t, nil
}

// parseLocal reads a local date and time. A space may stand for the T and the
// seconds are dropped. The result is the wall clock as if it were UTC.
func parseLocal(s string, allDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if allDay {
		if len(s) > len(dateLayout) {
			s = s[:len(dateLayout)]
		}
		return ParseDate(s)
	}
	s = strings.Replace(s, " ", "T", 1)
	if len(s) == len(dateLayout) {
		return time.Time{}, invalid("%q has no time; write YYYY-MM-DDTHH:MM, or set allDay", s)
	}
	if len(s) > len(timeLayout) {
		s = s[:len(timeLayout)]
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, invalid("%q is not a time written YYYY-MM-DDTHH:MM", s)
	}
	return t, nil
}

func format(t time.Time, allDay bool) string {
	if allDay {
		return t.Format(dateLayout)
	}
	return t.Format(timeLayout)
}

func checkText(name, v string, max int) error {
	if utf8.RuneCountInString(v) > max {
		return invalid("%s is longer than %d characters", name, max)
	}
	if strings.ContainsRune(v, 0) {
		return invalid("%s holds a null character", name)
	}
	return nil
}

// normalize checks an event and writes its fields in the canonical form.
func normalize(e *Event) error {
	e.Title = strings.TrimSpace(e.Title)
	if e.Title == "" {
		return invalid("the title is empty")
	}
	for _, c := range []struct {
		name, v string
		max     int
	}{{"the title", e.Title, MaxTitle}, {"the notes", e.Notes, MaxNotes}, {"the place", e.Place, MaxPlace}, {"the attendees", e.Attendees, MaxAttendees}} {
		if err := checkText(c.name, c.v, c.max); err != nil {
			return err
		}
	}
	if _, err := LoadZone(e.Zone); err != nil {
		return err
	}
	start, err := parseLocal(e.Start, e.AllDay)
	if err != nil {
		return err
	}
	var end time.Time
	switch {
	case strings.TrimSpace(e.End) != "":
		if end, err = parseLocal(e.End, e.AllDay); err != nil {
			return err
		}
	case e.AllDay:
		end = start
	default:
		end = start.Add(time.Hour)
	}
	if end.Before(start) {
		return invalid("the event ends before it starts")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return invalid("an event lasts at most a year; use a repeat for something that comes again")
	}
	e.Start, e.End = format(start, e.AllDay), format(end, e.AllDay)
	if e.Repeat != nil {
		r := *e.Repeat
		r.Freq = strings.ToLower(strings.TrimSpace(r.Freq))
		if r.Freq == "" || r.Freq == "none" {
			// An empty repeat takes the repeat away.
			e.Repeat = nil
			return nil
		}
		switch r.Freq {
		case Daily, Weekly, Monthly, Yearly:
		default:
			return invalid("the repeat must be daily, weekly, monthly or yearly")
		}
		if r.Interval == 0 {
			r.Interval = 1
		}
		if r.Interval < 1 || r.Interval > MaxInterval {
			return invalid("the repeat interval must be between 1 and %d", MaxInterval)
		}
		if r.Until != "" {
			u, err := ParseDate(r.Until)
			if err != nil {
				return err
			}
			if u.Before(start.Truncate(24 * time.Hour)) {
				return invalid("the repeat ends before the event starts")
			}
			r.Until = u.Format(dateLayout)
		}
		e.Repeat = &r
	}
	return nil
}

// Occurrence is one time an event takes place.
type Occurrence struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Start     string  `json:"start"`
	End       string  `json:"end"`
	AllDay    bool    `json:"allDay"`
	Zone      string  `json:"zone"`
	Weekday   string  `json:"weekday"`
	Place     string  `json:"place,omitempty"`
	Attendees string  `json:"attendees,omitempty"`
	Notes     string  `json:"notes,omitempty"`
	Repeat    *Repeat `json:"repeat,omitempty"`
	// Occurrence is the date of the start of this occurrence. A repeating
	// event is skipped on one occurrence by naming this date.
	Occurrence string `json:"occurrence"`
}

// shift moves a date by n steps of a repeat, or reports false when the date
// does not exist (the 31st of a short month, the 29th of February).
func shift(start time.Time, r *Repeat, n int) (time.Time, bool) {
	k := n * r.Interval
	switch r.Freq {
	case Daily:
		return start.AddDate(0, 0, k), true
	case Weekly:
		return start.AddDate(0, 0, 7*k), true
	case Monthly:
		months := int(start.Month()) - 1 + k
		y, m := start.Year()+months/12, time.Month(months%12+1)
		if start.Day() > daysIn(y, m) {
			return time.Time{}, false
		}
		return time.Date(y, m, start.Day(), start.Hour(), start.Minute(), 0, 0, time.UTC), true
	default: // yearly
		y := start.Year() + k
		if start.Month() == time.February && start.Day() == 29 && daysIn(y, time.February) < 29 {
			return time.Time{}, false
		}
		return time.Date(y, start.Month(), start.Day(), start.Hour(), start.Minute(), 0, 0, time.UTC), true
	}
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// maxSteps bounds how far one expansion walks, so a repeat from long ago
// cannot cost much.
const maxSteps = 40000

// instant returns the moment of a local wall clock in a zone. A time that does
// not exist (the hour skipped when the clocks go forward) moves forward.
func instant(wall time.Time, loc *time.Location) time.Time {
	return time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, loc)
}

// Expand lists the occurrences of an event that overlap [from, to). qloc is the
// zone in which an all-day event holds its days. skipped holds the dates of
// the occurrences that were taken out. It stops at limit occurrences.
func Expand(e Event, from, to time.Time, qloc *time.Location, skipped map[string]bool, limit int) []Occurrence {
	loc, err := LoadZone(e.Zone)
	if err != nil {
		return nil
	}
	start, err1 := parseLocal(e.Start, e.AllDay)
	end, err2 := parseLocal(e.End, e.AllDay)
	if err1 != nil || err2 != nil {
		return nil
	}
	span := end.Sub(start)
	var until time.Time
	if e.Repeat != nil && e.Repeat.Until != "" {
		until, _ = ParseDate(e.Repeat.Until)
	}
	var out []Occurrence
	steps := 1
	if e.Repeat != nil {
		steps = maxSteps
	}
	first := 0
	if e.Repeat != nil && (e.Repeat.Freq == Daily || e.Repeat.Freq == Weekly) {
		// Jump close to the range instead of walking from the start.
		per := 24 * time.Hour * time.Duration(e.Repeat.Interval)
		if e.Repeat.Freq == Weekly {
			per *= 7
		}
		if gap := from.Sub(instant(start, loc)) - span - 48*time.Hour; gap > 0 {
			first = int(gap / per)
		}
	}
	for n := first; n < first+steps; n++ {
		ws := start
		if n > 0 {
			var ok bool
			if ws, ok = shift(start, e.Repeat, n); !ok {
				continue
			}
		}
		if !until.IsZero() && ws.Truncate(24*time.Hour).After(until) {
			break
		}
		we := ws.Add(span)
		var is, ie time.Time
		if e.AllDay {
			is = time.Date(ws.Year(), ws.Month(), ws.Day(), 0, 0, 0, 0, qloc)
			ie = time.Date(we.Year(), we.Month(), we.Day()+1, 0, 0, 0, 0, qloc)
		} else {
			is, ie = instant(ws, loc), instant(we, loc)
		}
		if !is.Before(to) {
			break
		}
		key := ws.Format(dateLayout)
		if skipped[key] || !(ie.After(from) || (ie.Equal(is) && !is.Before(from))) {
			if e.Repeat == nil {
				break
			}
			continue
		}
		out = append(out, Occurrence{
			ID: e.ID, Title: e.Title, Start: format(ws, e.AllDay), End: format(we, e.AllDay), AllDay: e.AllDay, Zone: e.Zone,
			Weekday: ws.Weekday().String(), Place: e.Place, Attendees: e.Attendees, Notes: e.Notes, Repeat: e.Repeat, Occurrence: key,
		})
		if len(out) >= limit || e.Repeat == nil {
			break
		}
	}
	return out
}

// IsOccurrence says whether date is the start date of an occurrence of the
// event (taken out or not).
func IsOccurrence(e Event, date string) bool {
	d, err := ParseDate(date)
	if err != nil {
		return false
	}
	start, err := parseLocal(e.Start, e.AllDay)
	if err != nil {
		return false
	}
	if e.Repeat == nil {
		return start.Format(dateLayout) == date
	}
	if d.Before(start.Truncate(24 * time.Hour)) {
		return false
	}
	for n := 0; n < maxSteps; n++ {
		ws, ok := shift(start, e.Repeat, n)
		if n == 0 {
			ws, ok = start, true
		}
		if !ok {
			continue
		}
		day := ws.Truncate(24 * time.Hour)
		if day.After(d) {
			return false
		}
		if day.Equal(d) {
			return true
		}
	}
	return false
}
