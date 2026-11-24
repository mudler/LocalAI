// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"strings"
)

// vcardEscape escapes a text value as RFC 6350 asks.
func vcardEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// fold cuts a content line at 75 octets, without splitting a character.
func foldLine(line string) string {
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

// typeParam turns a free label into a safe TYPE parameter, or "".
func typeParam(label string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func entryLine(prop string, e Entry) string {
	if t := typeParam(e.Label); t != "" {
		return prop + ";TYPE=" + t + ":" + vcardEscape(e.Value)
	}
	return prop + ":" + vcardEscape(e.Value)
}

// VCard writes one contact as a vCard 3.0 card.
func VCard(c Contact) string {
	lines := []string{"BEGIN:VCARD", "VERSION:3.0", "FN:" + vcardEscape(c.Name), "N:" + vcardEscape(c.Name) + ";;;;"}
	if c.Org != "" {
		lines = append(lines, "ORG:"+vcardEscape(c.Org))
	}
	for _, p := range c.Phones {
		lines = append(lines, entryLine("TEL", p))
	}
	for _, e := range c.Emails {
		lines = append(lines, entryLine("EMAIL", e))
	}
	if c.Birthday != "" {
		lines = append(lines, "BDAY:"+c.Birthday)
	}
	if c.Address != "" {
		// The address is free text, so it goes in the street part, as one value.
		lines = append(lines, "ADR:;;"+vcardEscape(c.Address)+";;;;")
	}
	if c.Notes != "" {
		lines = append(lines, "NOTE:"+vcardEscape(c.Notes))
	}
	lines = append(lines, "UID:"+c.ID, "END:VCARD")
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(foldLine(l))
		b.WriteString("\r\n")
	}
	return b.String()
}

// VCards writes the whole book of the owner as one file of vCards.
func (s *Store) VCards(ctx context.Context, owner string) (string, error) {
	all, err := s.All(ctx, owner)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range all {
		b.WriteString(VCard(c))
	}
	return b.String(), nil
}
