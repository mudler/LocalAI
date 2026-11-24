// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go, so the add-on needs no C toolchain
)

// Store is the calendar file. Every call names the owner; one owner never
// reaches the events of another.
type Store struct {
	db  *sql.DB
	Now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS settings (
  owner TEXT PRIMARY KEY,
  zone  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  owner TEXT NOT NULL,
  id TEXT NOT NULL,
  title TEXT NOT NULL,
  start TEXT NOT NULL,
  "end" TEXT NOT NULL,
  allDay INTEGER NOT NULL,
  zone TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  place TEXT NOT NULL DEFAULT '',
  attendees TEXT NOT NULL DEFAULT '',
  freq TEXT NOT NULL DEFAULT '',
  interval INTEGER NOT NULL DEFAULT 1,
  until TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL,
  updated TEXT NOT NULL,
  PRIMARY KEY (owner, id)
);
CREATE TABLE IF NOT EXISTS skips (
  owner TEXT NOT NULL,
  event_id TEXT NOT NULL,
  day TEXT NOT NULL,
  PRIMARY KEY (owner, event_id, day)
);
PRAGMA user_version = 1;
`

// Open opens the calendar file in dir, and makes it when it is missing.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "calendar.sqlite")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One connection serializes the writes, which is all a calendar needs.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the file.
func (s *Store) Close() error { return s.db.Close() }

// HostZone is the zone a new calendar starts with: the system zone when it is
// an IANA name, else UTC.
func HostZone() string {
	if name := time.Local.String(); name != "Local" {
		if _, err := LoadZone(name); err == nil {
			return name
		}
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(link, "zoneinfo/"); i >= 0 {
			if name := link[i+len("zoneinfo/"):]; name != "" {
				if _, err := LoadZone(name); err == nil {
					return name
				}
			}
		}
	}
	return "UTC"
}

// Zone returns the default zone of the owner.
func (s *Store) Zone(ctx context.Context, owner string) (string, error) {
	var z string
	err := s.db.QueryRowContext(ctx, `SELECT zone FROM settings WHERE owner = ?`, owner).Scan(&z)
	if errors.Is(err, sql.ErrNoRows) {
		return HostZone(), nil
	}
	return z, err
}

// SetZone sets the default zone of the owner. It does not move any event: an
// event keeps the zone it was written in.
func (s *Store) SetZone(ctx context.Context, owner, zone string) error {
	if _, err := LoadZone(zone); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(owner, zone) VALUES(?, ?) ON CONFLICT(owner) DO UPDATE SET zone = excluded.zone`, owner, zone)
	return err
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ev-" + hex.EncodeToString(b)
}

func (s *Store) stamp() string { return s.Now().UTC().Format(time.RFC3339) }

const cols = `id, title, start, "end", allDay, zone, notes, place, attendees, freq, interval, until, created, updated`

type scanner interface{ Scan(...any) error }

func scan(r scanner) (Event, error) {
	var e Event
	var allDay, interval int
	var freq, until string
	err := r.Scan(&e.ID, &e.Title, &e.Start, &e.End, &allDay, &e.Zone, &e.Notes, &e.Place, &e.Attendees, &freq, &interval, &until, &e.Created, &e.Updated)
	e.AllDay = allDay == 1
	if freq != "" {
		e.Repeat = &Repeat{Freq: freq, Interval: interval, Until: until}
	}
	return e, err
}

// Create adds an event. The zone defaults to the default zone of the owner.
func (s *Store) Create(ctx context.Context, owner string, in Input) (Event, error) {
	e := Event{ID: newID()}
	if in.Title != nil {
		e.Title = *in.Title
	}
	if in.Start != nil {
		e.Start = *in.Start
	}
	if in.End != nil {
		e.End = *in.End
	}
	if in.AllDay != nil {
		e.AllDay = *in.AllDay
	}
	if in.Notes != nil {
		e.Notes = *in.Notes
	}
	if in.Place != nil {
		e.Place = *in.Place
	}
	if in.Attendees != nil {
		e.Attendees = *in.Attendees
	}
	e.Repeat = in.Repeat
	if in.Zone != nil && *in.Zone != "" {
		e.Zone = *in.Zone
	} else {
		z, err := s.Zone(ctx, owner)
		if err != nil {
			return e, err
		}
		e.Zone = z
	}
	if err := normalize(&e); err != nil {
		return e, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE owner = ?`, owner).Scan(&n); err != nil {
		return e, err
	}
	if n >= MaxEvents {
		return e, ErrFull
	}
	e.Created = s.stamp()
	e.Updated = e.Created
	return e, s.put(ctx, s.db, owner, e, false)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) put(ctx context.Context, x execer, owner string, e Event, replace bool) error {
	var freq, until string
	interval := 1
	if e.Repeat != nil {
		freq, interval, until = e.Repeat.Freq, e.Repeat.Interval, e.Repeat.Until
	}
	verb := "INSERT"
	if replace {
		verb = "INSERT OR REPLACE"
	}
	allDay := 0
	if e.AllDay {
		allDay = 1
	}
	_, err := x.ExecContext(ctx, verb+` INTO events(owner, `+cols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		owner, e.ID, e.Title, e.Start, e.End, allDay, e.Zone, e.Notes, e.Place, e.Attendees, freq, interval, until, e.Created, e.Updated)
	return err
}

// Get returns one event.
func (s *Store) Get(ctx context.Context, owner, id string) (Event, error) {
	e, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM events WHERE owner = ? AND id = ?`, owner, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// Update changes the fields the input names, on the whole series of a
// repeating event.
func (s *Store) Update(ctx context.Context, owner, id string, in Input) (Event, error) {
	e, err := s.Get(ctx, owner, id)
	if err != nil {
		return e, err
	}
	if in.Title != nil {
		e.Title = *in.Title
	}
	if in.AllDay != nil && *in.AllDay != e.AllDay {
		// The start and end change shape with the flag, so both must come with it.
		if in.Start == nil {
			return e, invalid("changing allDay needs a new start")
		}
		e.AllDay = *in.AllDay
		e.End = ""
	}
	if in.Start != nil {
		e.Start = *in.Start
		if in.End == nil && !(in.AllDay != nil) {
			// Keep the length of the event when only the start moves.
			if old, err := s.Get(ctx, owner, id); err == nil {
				os, e1 := parseLocal(old.Start, old.AllDay)
				oe, e2 := parseLocal(old.End, old.AllDay)
				ns, e3 := parseLocal(*in.Start, e.AllDay)
				if e1 == nil && e2 == nil && e3 == nil {
					e.End = format(ns.Add(oe.Sub(os)), e.AllDay)
				}
			}
		}
	}
	if in.End != nil {
		e.End = *in.End
	}
	if in.Zone != nil {
		e.Zone = *in.Zone
	}
	if in.Notes != nil {
		e.Notes = *in.Notes
	}
	if in.Place != nil {
		e.Place = *in.Place
	}
	if in.Attendees != nil {
		e.Attendees = *in.Attendees
	}
	if in.Repeat != nil {
		e.Repeat = in.Repeat
	}
	if err := normalize(&e); err != nil {
		return e, err
	}
	e.Updated = s.stamp()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return e, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.put(ctx, tx, owner, e, true); err != nil {
		return e, err
	}
	// A skipped date that is no longer an occurrence is dropped.
	rows, err := tx.QueryContext(ctx, `SELECT day FROM skips WHERE owner = ? AND event_id = ?`, owner, id)
	if err != nil {
		return e, err
	}
	var gone []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			_ = rows.Close()
			return e, err
		}
		if !IsOccurrence(e, d) {
			gone = append(gone, d)
		}
	}
	_ = rows.Close()
	for _, d := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM skips WHERE owner = ? AND event_id = ? AND day = ?`, owner, id, d); err != nil {
			return e, err
		}
	}
	return e, tx.Commit()
}

// Delete removes an event and all of its occurrences.
func (s *Store) Delete(ctx context.Context, owner, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE owner = ? AND id = ?`, owner, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM skips WHERE owner = ? AND event_id = ?`, owner, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SkipOccurrence takes one occurrence out of a repeating event. day is the
// start date of the occurrence.
func (s *Store) SkipOccurrence(ctx context.Context, owner, id, day string) error {
	e, err := s.Get(ctx, owner, id)
	if err != nil {
		return err
	}
	if e.Repeat == nil {
		return invalid("the event does not repeat; delete it instead")
	}
	if _, err := ParseDate(day); err != nil {
		return err
	}
	if !IsOccurrence(e, day) {
		return invalid("%s is not a date on which the event takes place", day)
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO skips(owner, event_id, day) VALUES(?,?,?)`, owner, id, day)
	return err
}

func (s *Store) skips(ctx context.Context, owner string) (map[string]map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, day FROM skips WHERE owner = ?`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var id, day string
		if err := rows.Scan(&id, &day); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][day] = true
	}
	return out, rows.Err()
}

// All returns every event of the owner, by start.
func (s *Store) All(ctx context.Context, owner string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM events WHERE owner = ? ORDER BY start, id`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Skipped returns the skipped dates of an event, sorted.
func (s *Store) Skipped(ctx context.Context, owner, id string) ([]string, error) {
	m, err := s.skips(ctx, owner)
	if err != nil {
		return nil, err
	}
	var out []string
	for d := range m[id] {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// List returns the occurrences that overlap the days from to to (both
// included) in the zone qzone, in time order. more is true when the answer was
// cut at MaxOccurrences.
func (s *Store) List(ctx context.Context, owner, from, to, qzone string) (occ []Occurrence, more bool, err error) {
	loc, err := LoadZone(qzone)
	if err != nil {
		return nil, false, err
	}
	f, err := ParseDate(from)
	if err != nil {
		return nil, false, err
	}
	t, err := ParseDate(to)
	if err != nil {
		return nil, false, err
	}
	if t.Before(f) {
		return nil, false, invalid("the range ends before it starts")
	}
	if t.Sub(f) > MaxRangeDays*24*time.Hour {
		return nil, false, invalid("the range is longer than %d days", MaxRangeDays)
	}
	fi := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc)
	ti := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
	events, err := s.All(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	sk, err := s.skips(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	for _, e := range events {
		occ = append(occ, Expand(e, fi, ti, loc, sk[e.ID], MaxOccurrences+1)...)
	}
	sortOccurrences(occ, loc)
	if len(occ) > MaxOccurrences {
		occ, more = occ[:MaxOccurrences], true
	}
	return occ, more, nil
}

// sortOccurrences orders by the moment of the start; all-day events come
// first on their day.
func sortOccurrences(o []Occurrence, qloc *time.Location) {
	key := func(x Occurrence) time.Time {
		if x.AllDay {
			d, _ := ParseDate(x.Start)
			return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, qloc)
		}
		w, _ := parseLocal(x.Start, false)
		l, _ := LoadZone(x.Zone)
		return instant(w, l)
	}
	sort.SliceStable(o, func(i, j int) bool {
		a, b := key(o[i]), key(o[j])
		if !a.Equal(b) {
			return a.Before(b)
		}
		return o[i].Title < o[j].Title
	})
}

// Search finds events whose title, notes, place or attendees hold every word
// of the query, ignoring case. Each result carries the next occurrence from
// now, when there is one.
func (s *Store) Search(ctx context.Context, owner, query string, limit int) ([]SearchHit, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, invalid("the search text is empty")
	}
	events, err := s.All(ctx, owner)
	if err != nil {
		return nil, err
	}
	sk, err := s.skips(ctx, owner)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	var hits []SearchHit
	for _, e := range events {
		hay := strings.ToLower(e.Title + "\n" + e.Notes + "\n" + e.Place + "\n" + e.Attendees)
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		h := SearchHit{Event: e}
		if next := Expand(e, now, now.AddDate(5, 0, 0), time.UTC, sk[e.ID], 1); len(next) > 0 {
			h.Next = next[0].Start
		}
		hits = append(hits, h)
	}
	// Upcoming events first, soonest first; then the past, latest first.
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch {
		case a.Next != "" && b.Next != "":
			return a.Next < b.Next
		case a.Next != "" || b.Next != "":
			return a.Next != ""
		}
		return a.Event.Start > b.Event.Start
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// SearchHit is an event found by Search. Next is the start of its next
// occurrence, empty for an event that is over.
type SearchHit struct {
	Event Event  `json:"event"`
	Next  string `json:"next,omitempty"`
}
