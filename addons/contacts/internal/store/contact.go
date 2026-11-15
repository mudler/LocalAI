// SPDX-License-Identifier: MIT
package store

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limits. The host bounds one payload, so a book is bounded too.
const (
	MaxContacts = 2000
	MaxName     = 200
	MaxOrg      = 200
	MaxLabel    = 40
	MaxPhone    = 40
	MaxEmail    = 254
	MaxEntries  = 10
	MaxAddress  = 500
	MaxNotes    = 4000
)

var (
	ErrInvalid  = errors.New("invalid contact")
	ErrNotFound = errors.New("contact not found")
	ErrFull     = errors.New("the contact book is full")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Entry is a phone number or an email address with a free label such as
// mobile, home or work.
type Entry struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
}

// Contact is one person or organisation of the book.
type Contact struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Org      string  `json:"organisation,omitempty"`
	Phones   []Entry `json:"phones"`
	Emails   []Entry `json:"emails"`
	Birthday string  `json:"birthday,omitempty"`
	Address  string  `json:"address,omitempty"`
	Notes    string  `json:"notes,omitempty"`
	Created  string  `json:"created"`
	Updated  string  `json:"updated"`
}

// Input names the fields to set. A nil field is left as it is on an update and
// empty on a create. A non-nil list replaces the whole list.
type Input struct {
	Name     *string
	Org      *string
	Phones   *[]Entry
	Emails   *[]Entry
	Birthday *string
	Address  *string
	Notes    *string
}

var (
	birthdayFull = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	birthdayYear = regexp.MustCompile(`^--\d{2}-\d{2}$`)
	emailShape   = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	phoneShape   = regexp.MustCompile(`^[0-9+()\-./ #*]+$`)
)

func controlFree(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return false
		}
	}
	return true
}

func checkText(field, s string, max int, multiline bool) error {
	if utf8.RuneCountInString(s) > max {
		return invalid("%s has more than %d characters", field, max)
	}
	if !multiline && strings.ContainsAny(s, "\n\r") {
		return invalid("%s must be one line", field)
	}
	if !controlFree(strings.ReplaceAll(s, "\r", "")) {
		return invalid("%s holds a control character", field)
	}
	return nil
}

func checkBirthday(s string) error {
	if s == "" {
		return nil
	}
	if birthdayFull.MatchString(s) {
		y, m, d := atoi(s[0:4]), atoi(s[5:7]), atoi(s[8:10])
		if y < 1 || !validDay(y, m, d) {
			return invalid("birthday is not a date: use YYYY-MM-DD, or --MM-DD when the year is not known")
		}
		return nil
	}
	if birthdayYear.MatchString(s) {
		// 2000 is a leap year, so --02-29 is accepted.
		if !validDay(2000, atoi(s[2:4]), atoi(s[5:7])) {
			return invalid("birthday is not a date: use YYYY-MM-DD, or --MM-DD when the year is not known")
		}
		return nil
	}
	return invalid("birthday must be YYYY-MM-DD, or --MM-DD when the year is not known")
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func validDay(y, m, d int) bool {
	if m < 1 || m > 12 || d < 1 {
		return false
	}
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	n := days[m-1]
	if m == 2 && (y%4 == 0 && y%100 != 0 || y%400 == 0) {
		n = 29
	}
	return d <= n
}

func normalizeEntries(kind string, in []Entry, max int, shape func(string) bool) ([]Entry, error) {
	if len(in) > MaxEntries {
		return nil, invalid("at most %d %s", MaxEntries, kind)
	}
	out := make([]Entry, 0, len(in))
	for _, e := range in {
		e.Label, e.Value = strings.TrimSpace(e.Label), strings.TrimSpace(e.Value)
		if e.Value == "" {
			return nil, invalid("a %s entry has no value", kind)
		}
		if err := checkText(kind+" label", e.Label, MaxLabel, false); err != nil {
			return nil, err
		}
		if err := checkText(kind, e.Value, max, false); err != nil {
			return nil, err
		}
		if !shape(e.Value) {
			return nil, invalid("%q is not a valid %s", e.Value, kind[:len(kind)-1])
		}
		out = append(out, e)
	}
	return out, nil
}

// normalize trims and checks a contact that is about to be stored.
func normalize(c *Contact) error {
	c.Name, c.Org = strings.TrimSpace(c.Name), strings.TrimSpace(c.Org)
	c.Address, c.Notes = strings.TrimSpace(c.Address), strings.TrimSpace(c.Notes)
	c.Birthday = strings.TrimSpace(c.Birthday)
	if c.Name == "" {
		return invalid("a contact needs a name")
	}
	for _, f := range []struct {
		n, v string
		max  int
		ml   bool
	}{{"name", c.Name, MaxName, false}, {"organisation", c.Org, MaxOrg, false}, {"address", c.Address, MaxAddress, true}, {"notes", c.Notes, MaxNotes, true}} {
		if err := checkText(f.n, f.v, f.max, f.ml); err != nil {
			return err
		}
	}
	if err := checkBirthday(c.Birthday); err != nil {
		return err
	}
	var err error
	if c.Phones, err = normalizeEntries("phones", c.Phones, MaxPhone, func(v string) bool { return phoneShape.MatchString(v) && len(digits(v)) >= 2 }); err != nil {
		return err
	}
	if c.Emails, err = normalizeEntries("emails", c.Emails, MaxEmail, emailShape.MatchString); err != nil {
		return err
	}
	return nil
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
