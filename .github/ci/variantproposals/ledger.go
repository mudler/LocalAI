package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Ledger records the grouping decisions a human has already made against the
// proposer, so a declined candidate stays declined instead of coming back every
// night until reviewers stop reading the job's pull requests.
//
// It is checked in next to the gallery and is meant to be edited inside the
// proposal pull request itself: declining a family is adding one flow-mapping
// line under pairs or groups and closing the PR.
type Ledger struct {
	// Tokens are name segments that mark a distinct model rather than another
	// build of the same one: finetune names, language codes, product suffixes.
	// A candidate whose two names differ by any of these is never proposed.
	Tokens []LedgerToken `yaml:"tokens"`
	// Pairs are individual candidates a human considered and declined. Order
	// does not matter: the pair is matched both ways round.
	Pairs []LedgerPair `yaml:"pairs"`
	// Groups decline every pair drawn from a set at once, for families like a
	// per-language release where listing each pair would be unreadable.
	Groups []LedgerGroup `yaml:"groups"`
}

type LedgerToken struct {
	Token  string `yaml:"token"`
	Reason string `yaml:"reason"`
}

type LedgerPair struct {
	Parent  string `yaml:"parent"`
	Variant string `yaml:"variant"`
	Reason  string `yaml:"reason"`
}

type LedgerGroup struct {
	Members []string `yaml:"members"`
	Reason  string   `yaml:"reason"`
}

// LoadLedger reads a ledger file. A missing file is not an error: a gallery
// that has declined nothing yet is a legitimate state, and failing the job over
// it would only teach people to keep an empty file around.
func LoadLedger(path string) (*Ledger, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Ledger{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseLedger(data)
}

func ParseLedger(data []byte) (*Ledger, error) {
	l := &Ledger{}
	if err := yaml.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("parsing ledger: %w", err)
	}
	for i, t := range l.Tokens {
		if strings.TrimSpace(t.Token) == "" {
			return nil, fmt.Errorf("ledger tokens[%d] has an empty token", i)
		}
	}
	for i, p := range l.Pairs {
		if strings.TrimSpace(p.Parent) == "" || strings.TrimSpace(p.Variant) == "" {
			return nil, fmt.Errorf("ledger pairs[%d] needs both parent and variant", i)
		}
	}
	return l, nil
}

// Suppression is a ledger hit: why a candidate was not proposed, in words a
// reviewer can check against the ledger file.
type Suppression struct {
	A      string
	B      string
	Reason string
}

func (s Suppression) String() string {
	return fmt.Sprintf("%s + %s: %s", s.A, s.B, s.Reason)
}

// Suppresses reports whether the ledger has already declined pairing these two
// entries, and why.
//
// The token rule is applied to the segments the two names do not share. Two
// builds of the same weights differ only in quantization markers, so any
// ledgered token showing up in that difference is by construction a claim that
// the entries are different models.
func (l *Ledger) Suppresses(a, b string) (Suppression, bool) {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	for _, p := range l.Pairs {
		lp, lv := strings.ToLower(p.Parent), strings.ToLower(p.Variant)
		if (lp == la && lv == lb) || (lp == lb && lv == la) {
			return Suppression{A: a, B: b, Reason: p.Reason}, true
		}
	}
	for _, g := range l.Groups {
		var seenA, seenB bool
		for _, m := range g.Members {
			lm := strings.ToLower(m)
			if lm == la {
				seenA = true
			}
			if lm == lb {
				seenB = true
			}
		}
		if seenA && seenB {
			return Suppression{A: a, B: b, Reason: g.Reason}, true
		}
	}
	diff := differingSegments(la, lb)
	for _, t := range l.Tokens {
		token := strings.ToLower(strings.TrimSpace(t.Token))
		if _, ok := diff[token]; ok {
			reason := t.Reason
			if reason == "" {
				reason = fmt.Sprintf("names differ by %q", token)
			}
			return Suppression{A: a, B: b, Reason: fmt.Sprintf("%s (token %q)", reason, token)}, true
		}
	}
	return Suppression{}, false
}

// segments splits a name into the atoms the token rules are written against.
func segments(name string) []string {
	fields := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ':' || r == '/'
	})
	return fields
}

// differingSegments returns the set of segments present in exactly one of the
// two names.
func differingSegments(a, b string) map[string]struct{} {
	setA := map[string]int{}
	for _, s := range segments(a) {
		setA[s]++
	}
	setB := map[string]int{}
	for _, s := range segments(b) {
		setB[s]++
	}
	diff := map[string]struct{}{}
	for s := range setA {
		if setB[s] == 0 {
			diff[s] = struct{}{}
		}
	}
	for s := range setB {
		if setA[s] == 0 {
			diff[s] = struct{}{}
		}
	}
	return diff
}

// SortedSuppressions gives the ledger's effect on one run in a stable order, so
// the pull request body reads the same way for the same gallery.
func SortedSuppressions(in []Suppression) []Suppression {
	out := append([]Suppression(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}
