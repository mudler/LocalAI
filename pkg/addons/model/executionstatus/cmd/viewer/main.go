// SPDX-License-Identifier: MIT

// Command viewer exports steps computed by the real executionstatus reducer.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	es "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
)

//go:embed viewer.tmpl.html
var page string

const dataStart = `<script id="data" type="application/json">`
const dataEnd = `</script>`
const placeholder = "/*__DATA__*/null"
const explorationDepth = 6

type stepJSON struct {
	Number  int      `json:"step"`
	Event   string   `json:"event"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Effects []string `json:"effects"`
	Rule    es.Rule  `json:"rule"`
	Guard   string   `json:"guard"`
}
type traceJSON struct {
	Family int        `json:"family"`
	Name   string     `json:"name"`
	Steps  []stepJSON `json:"steps"`
	Value  es.Value   `json:"value"`
}
type dataJSON struct {
	Traces  []traceJSON    `json:"traces"`
	Rules   []es.Rule      `json:"rules"`
	Explore es.Exploration `json:"explore"`
}

// describe retains the concrete union tag, including empty constructors. Using
// Go's structural formatting also exposes Reply's private validation payload;
// JSON-marshalling the model interfaces directly would silently lose it.
func describe(v any) string { return fmt.Sprintf("%T %+v", v, v) }

// collect verifies the independent fixture oracle before exporting reducer data.
// Function arguments let tests prove failures are propagated, not decorated as
// successful traces. Production always supplies the shared model functions.
func collect(traces []es.Trace, runTrace func(es.State, []es.Event) []es.Transition, explore func(int) (es.Exploration, error)) (dataJSON, error) {
	d := dataJSON{Rules: es.Rules()}
	families := map[int]bool{}
	rules := map[es.Rule]bool{}
	states := map[reflect.Type]bool{reflect.TypeOf(es.Unbound{}): true}
	effects := map[reflect.Type]bool{}
	for _, tr := range traces {
		families[tr.Family] = true
		steps := runTrace(es.Unbound{}, tr.Events)
		if len(steps) == 0 || len(steps) != len(tr.Want) || len(steps) != len(tr.Events) {
			return d, fmt.Errorf("trace %q: incomplete steps", tr.Name)
		}
		t := traceJSON{Family: tr.Family, Name: tr.Name, Value: tr.Value}
		var before es.State = es.Unbound{}
		for i, s := range steps {
			want := tr.Want[i]
			if s.Rule != want.Rule || !reflect.DeepEqual(s.State, want.State) || !reflect.DeepEqual(s.Effects, want.Effects) || s.Guard == "" {
				return d, fmt.Errorf("trace %q step %d: rule/state/ordered effects/guard mismatch: got %+v, want %+v", tr.Name, i+1, s, want)
			}
			rules[s.Rule] = true
			states[reflect.TypeOf(s.State)] = true
			fx := []string{}
			for _, e := range s.Effects {
				effects[reflect.TypeOf(e)] = true
				fx = append(fx, describe(e))
			}
			t.Steps = append(t.Steps, stepJSON{i + 1, describe(tr.Events[i]), describe(before), describe(s.State), fx, s.Rule, s.Guard})
			before = s.State
		}
		if es.Project(before) != tr.Value {
			return d, fmt.Errorf("trace %q: final projection mismatch", tr.Name)
		}
		d.Traces = append(d.Traces, t)
	}
	if len(families) != 11 {
		return d, fmt.Errorf("incomplete trace families: %d", len(families))
	}
	for i := 1; i <= 11; i++ {
		if !families[i] {
			return d, fmt.Errorf("missing trace family %d", i)
		}
	}
	if len(rules) != len(d.Rules) {
		return d, fmt.Errorf("incomplete rule coverage")
	}
	for _, r := range d.Rules {
		if !rules[r] {
			return d, fmt.Errorf("missing rule %s", r)
		}
	}
	if len(states) != len(es.AllStates()) {
		return d, fmt.Errorf("incomplete state coverage")
	}
	for _, s := range es.AllStates() {
		if !states[reflect.TypeOf(s)] {
			return d, fmt.Errorf("missing state %T", s)
		}
	}
	for _, e := range []es.Effect{es.Read{}, es.Cancel{}, es.Publish{}} {
		if !effects[reflect.TypeOf(e)] {
			return d, fmt.Errorf("missing effect %T", e)
		}
	}
	rep, err := explore(explorationDepth)
	if err != nil {
		return d, fmt.Errorf("exploration: %w", err)
	}
	if rep.States == 0 || rep.Transitions == 0 || rep.LivenessChecks != rep.States || len(rep.Cells) != len(es.AllStates())*len(es.AllEvents()) {
		return d, fmt.Errorf("incomplete invariant exploration")
	}
	for _, s := range es.AllStates() {
		for _, e := range es.AllEvents() {
			if !rep.Cells[fmt.Sprintf("%T/%T", s, e)] {
				return d, fmt.Errorf("missing matrix cell %T/%T", s, e)
			}
		}
	}
	d.Explore = rep
	return d, nil
}
func computedData() ([]byte, error) {
	d, err := collect(es.Traces(), es.RunTrace, es.Explore)
	if err != nil {
		return nil, err
	}
	// encoding/json sorts map keys and escapes HTML, including script terminators.
	return json.Marshal(d)
}
func generate() ([]byte, error) {
	data, err := computedData()
	if err != nil {
		return nil, err
	}
	if strings.Count(page, placeholder) != 1 {
		return nil, fmt.Errorf("template must contain exactly one data placeholder")
	}
	return []byte(strings.Replace(page, placeholder, string(data), 1)), nil
}
func validatePage(html []byte) error {
	start := bytes.Index(html, []byte(dataStart))
	if start < 0 {
		return fmt.Errorf("missing embedded data")
	}
	start += len(dataStart)
	end := bytes.Index(html[start:], []byte(dataEnd))
	if end < 0 {
		return fmt.Errorf("unterminated embedded data")
	}
	expected, err := computedData()
	if err != nil {
		return err
	}
	if !bytes.Equal(html[start:start+end], expected) {
		return fmt.Errorf("embedded data differs from real reducer traces")
	}
	return nil
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("viewer", flag.ContinueOnError)
	flags.SetOutput(out)
	defaultPath := "viewer.html"
	path := flags.String("o", defaultPath, "output file (with -check, verify this existing file)")
	check := flags.Bool("check", false, "verify traces, invariants and deterministic embedded data without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	explicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "o" {
			explicit = true
		}
	})
	html, err := generate()
	if err != nil {
		return err
	}
	if *check {
		again, err := generate()
		if err != nil {
			return err
		}
		if !bytes.Equal(html, again) {
			return fmt.Errorf("nondeterministic generation")
		}
		if err := validatePage(html); err != nil {
			return err
		}
		if explicit {
			existing, err := os.ReadFile(*path)
			if err != nil {
				return err
			}
			if err := validatePage(existing); err != nil {
				return err
			}
			if !bytes.Equal(existing, html) {
				return fmt.Errorf("stale viewer: %s", *path)
			}
		}
		_, err = fmt.Fprintln(out, "executionstatus: traces, rule/state/effect coverage, invariants and deterministic embedded data verified")
		return err
	}
	if err := os.WriteFile(*path, html, 0600); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "wrote %s\n", *path)
	return err
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
