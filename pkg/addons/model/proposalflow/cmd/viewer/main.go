// SPDX-License-Identifier: MIT

// Command viewer writes a self-contained HTML page that steps through the
// traces of the proposal-flow model. It runs the real reducer: the page
// embeds the JSON this program computes, not a copy of the logic.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	pf "github.com/mudler/LocalAI/pkg/addons/model/proposalflow"
)

//go:embed viewer.tmpl.html
var page string

type stateJSON struct {
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Durable string `json:"durable"`
}

type stepJSON struct {
	Event    string    `json:"event"`
	Type     string    `json:"type"`
	Rule     string    `json:"rule"`
	RuleKind string    `json:"ruleKind"`
	RuleText string    `json:"ruleText"`
	Before   stateJSON `json:"before"`
	After    stateJSON `json:"after"`
	Effects  []string  `json:"effects"`
}

type traceJSON struct {
	Name  string     `json:"name"`
	About string     `json:"about"`
	Final stateJSON  `json:"final"`
	OK    bool       `json:"ok"`
	Bad   []string   `json:"bad"`
	Steps []stepJSON `json:"steps"`
}

type ruleJSON struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type exploreJSON struct {
	States     int            `json:"states"`
	Edges      int            `json:"edges"`
	Terminals  int            `json:"terminals"`
	DeadEnds   []string       `json:"deadEnds"`
	Violations []string       `json:"violations"`
	ByName     map[string]int `json:"byName"`
}

type dataJSON struct {
	Traces  []traceJSON `json:"traces"`
	Rules   []ruleJSON  `json:"rules"`
	Explore exploreJSON `json:"explore"`
}

// nonNil keeps empty lists as [] in the JSON, not null.
func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func st(s pf.State) stateJSON {
	d := strings.TrimPrefix(fmt.Sprintf("%+v", s), "{")
	d = strings.TrimSuffix(d, "}")
	return stateJSON{Name: s.Name(), Detail: d, Durable: s.Durable()}
}

func main() {
	out := flag.String("o", "viewer.html", "output file")
	flag.Parse()
	var data dataJSON
	for _, r := range pf.Rules {
		data.Rules = append(data.Rules, ruleJSON{ID: r.ID, Kind: string(r.Kind), Text: r.Text})
	}
	for _, tr := range pf.Traces() {
		bad := tr.Verify()
		t := traceJSON{Name: tr.Name, About: tr.About, Final: st(tr.Final), OK: len(bad) == 0, Bad: nonNil(bad)}
		for _, r := range pf.Run(pf.Unproposed{}, tr.Events()) {
			rule, _ := pf.RuleByID(r.Rule)
			t.Steps = append(t.Steps, stepJSON{
				Event: pf.Describe(r.Event), Type: r.Event.Name(), Rule: r.Rule, RuleKind: string(rule.Kind), RuleText: rule.Text,
				Before: st(r.Before), After: st(r.After), Effects: pf.EffectNames(r.Effects),
			})
		}
		data.Traces = append(data.Traces, t)
	}
	rep := pf.Explore()
	data.Explore = exploreJSON{States: rep.States, Edges: rep.Edges, Terminals: len(rep.Terminals), DeadEnds: nonNil(rep.DeadEnds), Violations: nonNil(rep.Violations), ByName: rep.ByName}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(data); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	html := strings.Replace(page, "/*__DATA__*/null", strings.TrimSpace(buf.String()), 1)
	if err := os.WriteFile(*out, []byte(html), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
