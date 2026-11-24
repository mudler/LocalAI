// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mudler/LocalAI/pkg/xslices"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite" // pure Go, so the add-on needs no C toolchain
)

// Store is the contact file. Every call names the owner; one owner never
// reaches the contacts of another.
type Store struct {
	db  *sql.DB
	Now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS contacts (
  owner TEXT NOT NULL,
  id TEXT NOT NULL,
  name TEXT NOT NULL,
  org TEXT NOT NULL DEFAULT '',
  phones TEXT NOT NULL DEFAULT '[]',
  emails TEXT NOT NULL DEFAULT '[]',
  birthday TEXT NOT NULL DEFAULT '',
  address TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL,
  updated TEXT NOT NULL,
  PRIMARY KEY (owner, id)
);
PRAGMA user_version = 1;
`

// Open opens the contact file in dir, and makes it when it is missing.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "contacts.sqlite")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One connection serializes the writes, which is all a contact book needs.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the file.
func (s *Store) Close() error { return s.db.Close() }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ct-" + hex.EncodeToString(b)
}

func (s *Store) stamp() string { return s.Now().UTC().Format(time.RFC3339) }

const cols = `id, name, org, phones, emails, birthday, address, notes, created, updated`

type scanner interface{ Scan(...any) error }

func scan(r scanner) (Contact, error) {
	var c Contact
	var phones, emails string
	if err := r.Scan(&c.ID, &c.Name, &c.Org, &phones, &emails, &c.Birthday, &c.Address, &c.Notes, &c.Created, &c.Updated); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(phones), &c.Phones); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(emails), &c.Emails); err != nil {
		return c, err
	}
	if c.Phones == nil {
		c.Phones = []Entry{}
	}
	if c.Emails == nil {
		c.Emails = []Entry{}
	}
	return c, nil
}

func (s *Store) put(ctx context.Context, owner string, c Contact, replace bool) error {
	phones, _ := json.Marshal(xslices.NonNil(c.Phones))
	emails, _ := json.Marshal(xslices.NonNil(c.Emails))
	verb := "INSERT"
	if replace {
		verb = "INSERT OR REPLACE"
	}
	_, err := s.db.ExecContext(ctx, verb+` INTO contacts(owner, `+cols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		owner, c.ID, c.Name, c.Org, string(phones), string(emails), c.Birthday, c.Address, c.Notes, c.Created, c.Updated)
	return err
}

func apply(c *Contact, in Input) {
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.Org != nil {
		c.Org = *in.Org
	}
	if in.Phones != nil {
		c.Phones = *in.Phones
	}
	if in.Emails != nil {
		c.Emails = *in.Emails
	}
	if in.Birthday != nil {
		c.Birthday = *in.Birthday
	}
	if in.Address != nil {
		c.Address = *in.Address
	}
	if in.Notes != nil {
		c.Notes = *in.Notes
	}
}

// Create adds a contact to the book of the owner.
func (s *Store) Create(ctx context.Context, owner string, in Input) (Contact, error) {
	c := Contact{ID: newID()}
	apply(&c, in)
	if err := normalize(&c); err != nil {
		return c, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE owner = ?`, owner).Scan(&n); err != nil {
		return c, err
	}
	if n >= MaxContacts {
		return c, ErrFull
	}
	c.Created = s.stamp()
	c.Updated = c.Created
	return c, s.put(ctx, owner, c, false)
}

// Get returns one contact.
func (s *Store) Get(ctx context.Context, owner, id string) (Contact, error) {
	c, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM contacts WHERE owner = ? AND id = ?`, owner, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// Update changes the fields the input names.
func (s *Store) Update(ctx context.Context, owner, id string, in Input) (Contact, error) {
	c, err := s.Get(ctx, owner, id)
	if err != nil {
		return c, err
	}
	apply(&c, in)
	if err := normalize(&c); err != nil {
		return c, err
	}
	c.Updated = s.stamp()
	return c, s.put(ctx, owner, c, true)
}

// Delete removes a contact.
func (s *Store) Delete(ctx context.Context, owner, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM contacts WHERE owner = ? AND id = ?`, owner, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// All returns every contact of the owner, by name.
func (s *Store) All(ctx context.Context, owner string) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM contacts WHERE owner = ?`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Contact
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := fold(out[i].Name), fold(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// fold lowers a text and takes the accents off, so Jose finds José.
func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, strings.ToLower(s))
	if err != nil {
		return strings.ToLower(s)
	}
	return out
}

// Summary is the short form of a contact in a list or a search.
type Summary struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Org    string  `json:"organisation,omitempty"`
	Phones []Entry `json:"phones"`
	Emails []Entry `json:"emails"`
}

func summary(c Contact) Summary {
	return Summary{ID: c.ID, Name: c.Name, Org: c.Org, Phones: c.Phones, Emails: c.Emails}
}

// Search finds the contacts that match every word of the query in the name,
// organisation, notes, address, a phone number or an email address. A word
// of three or more digits also matches a phone number whatever its spacing.
// An empty query matches everyone. It returns at most limit summaries and
// whether there were more.
func (s *Store) Search(ctx context.Context, owner, query string, limit int) ([]Summary, bool, error) {
	all, err := s.All(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	words := strings.Fields(fold(query))
	out := []Summary{}
	more := false
	for _, c := range all {
		if !matches(c, words) {
			continue
		}
		if len(out) >= limit {
			more = true
			break
		}
		out = append(out, summary(c))
	}
	return out, more, nil
}

func matches(c Contact, words []string) bool {
	text := []string{fold(c.Name), fold(c.Org), fold(c.Notes), fold(c.Address), fold(c.Birthday)}
	var nums []string
	for _, p := range c.Phones {
		text = append(text, fold(p.Value), fold(p.Label))
		nums = append(nums, digits(p.Value))
	}
	for _, e := range c.Emails {
		text = append(text, fold(e.Value), fold(e.Label))
	}
	for _, w := range words {
		ok := false
		for _, t := range text {
			if strings.Contains(t, w) {
				ok = true
				break
			}
		}
		if !ok {
			if d := digits(w); len(d) >= 3 && len(d) == len(strings.Trim(w, "+-()./ ")) {
				for _, n := range nums {
					if strings.Contains(n, d) {
						ok = true
						break
					}
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
