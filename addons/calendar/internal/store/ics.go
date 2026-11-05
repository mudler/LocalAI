// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// icsEscape escapes a text value as RFC 5545 asks.
func icsEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// fold cuts a content line at 75 octets, without splitting a character.
func fold(line string) string {
	var b strings.Builder
	n := 0
	for _, r := range line {
		w := len(string(r))
		if n+w > 75 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}

// ICS writes the whole calendar of the owner as one iCalendar file. A timed
// event that does not repeat is written in UTC. A repeating one keeps its zone
// name so the repeat follows daylight saving time.
func (s *Store) ICS(ctx context.Context, owner string) (string, error) {
	events, err := s.All(ctx, owner)
	if err != nil {
		return "", err
	}
	sk, err := s.skips(ctx, owner)
	if err != nil {
		return "", err
	}
	var lines []string
	add := func(l string) { lines = append(lines, fold(l)) }
	add("BEGIN:VCALENDAR")
	add("VERSION:2.0")
	add("PRODID:-//LocalAI//Calendar add-on//EN")
	add("CALSCALE:GREGORIAN")
	stamp := s.Now().UTC().Format("20060102T150405Z")
	for _, e := range events {
		add("BEGIN:VEVENT")
		add("UID:" + e.ID + "@localai-calendar")
		add("DTSTAMP:" + stamp)
		start, _ := parseLocal(e.Start, e.AllDay)
		end, _ := parseLocal(e.End, e.AllDay)
		loc, _ := LoadZone(e.Zone)
		compact := func(t time.Time) string { return t.Format("20060102T150405") }
		switch {
		case e.AllDay:
			add("DTSTART;VALUE=DATE:" + start.Format("20060102"))
			add("DTEND;VALUE=DATE:" + end.AddDate(0, 0, 1).Format("20060102"))
		case e.Repeat != nil:
			add("DTSTART;TZID=" + e.Zone + ":" + compact(start))
			add("DTEND;TZID=" + e.Zone + ":" + compact(end))
		default:
			add("DTSTART:" + instant(start, loc).UTC().Format("20060102T150405Z"))
			add("DTEND:" + instant(end, loc).UTC().Format("20060102T150405Z"))
		}
		add("SUMMARY:" + icsEscape(e.Title))
		if e.Place != "" {
			add("LOCATION:" + icsEscape(e.Place))
		}
		desc := e.Notes
		if e.Attendees != "" {
			if desc != "" {
				desc += "\n"
			}
			desc += "With: " + e.Attendees
		}
		if desc != "" {
			add("DESCRIPTION:" + icsEscape(desc))
		}
		if r := e.Repeat; r != nil {
			rule := "FREQ=" + strings.ToUpper(r.Freq) + ";INTERVAL=" + strconv.Itoa(r.Interval)
			if r.Until != "" {
				u, _ := ParseDate(r.Until)
				if e.AllDay {
					rule += ";UNTIL=" + u.Format("20060102")
				} else {
					// The last day counts whole, so end it at the end of that day.
					rule += ";UNTIL=" + instant(u.Add(23*time.Hour+59*time.Minute), loc).UTC().Format("20060102T150405Z")
				}
			}
			add("RRULE:" + rule)
			for d := range sk[e.ID] {
				day, _ := ParseDate(d)
				if e.AllDay {
					add("EXDATE;VALUE=DATE:" + day.Format("20060102"))
				} else {
					add("EXDATE;TZID=" + e.Zone + ":" + compact(time.Date(day.Year(), day.Month(), day.Day(), start.Hour(), start.Minute(), 0, 0, time.UTC)))
				}
			}
		}
		add("END:VEVENT")
	}
	add("END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n", nil
}
