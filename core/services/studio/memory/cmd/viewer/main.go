// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mudler/LocalAI/core/services/studio/memory"
)

type step struct {
	Before, Event, After, Effects any
	Rule, Guard, Ignored          string
}
type replay struct {
	Name  string
	Steps []step
}

func generate() ([]byte, error) {
	traces := []replay{}
	for _, t := range memory.ExampleTraces() {
		r := replay{Name: t.Name}
		s := t.Initial
		for _, e := range t.Events {
			tr := memory.Reduce(s, e)
			r.Steps = append(r.Steps, step{tag(s), tag(e), tag(tr.State), tags(tr.Effects), tr.RuleID, memory.Rules[tr.RuleID], tr.IgnoredReason})
			s = tr.State
		}
		traces = append(traces, r)
	}
	data, err := json.Marshal(traces)
	if err != nil {
		return nil, err
	}
	return []byte(`<!doctype html>
<html lang="en"><meta charset="utf-8"><title>Agent memory reducer</title>
<style>body{font:16px system-ui;max-width:1000px;margin:2em auto}pre{white-space:pre-wrap;background:#eee;padding:1em}button,select{font:inherit;margin:.5em}</style>
<h1>Agent memory reducer</h1><p>Generated execution of Go Reduce. Backend fencing and atomic storage remain shell obligations, not model proofs.</p>
<select id="trace" aria-label="Trace"></select><button id="prev">Previous</button><button id="next">Next</button><span id="position"></span><pre id="step"></pre>
<script>const traces=` + string(data) + `;let n=0;const select=document.getElementById('trace');traces.forEach((t,i)=>select.add(new Option(t.Name,i)));function draw(){const t=traces[select.value||0];n=Math.max(0,Math.min(n,t.Steps.length-1));document.getElementById('position').textContent=(n+1)+' / '+t.Steps.length;document.getElementById('step').textContent=JSON.stringify(t.Steps[n],null,2)}select.onchange=()=>{n=0;draw()};document.getElementById('prev').onclick=()=>{n--;draw()};document.getElementById('next').onclick=()=>{n++;draw()};draw();</script></html>
`), nil
}
func tag(v any) any {
	return struct {
		Type string
		Data any
	}{fmt.Sprintf("%T", v), v}
}
func tags(e []memory.Effect) []any {
	out := []any{}
	for _, v := range e {
		out = append(out, tag(v))
	}
	return out
}
func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("viewer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "docs/development/agent-memory-reducer.html", "generated output path")
	check := flags.Bool("check", false, "fail if output is missing or stale")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	data, err := generate()
	if err != nil {
		return err
	}
	if *check {
		old, err := os.ReadFile(*output)
		if err != nil {
			return err
		}
		if !bytes.Equal(old, data) {
			return fmt.Errorf("stale viewer: regenerate %s", *output)
		}
		return nil
	}
	return os.WriteFile(*output, data, 0600)
}
func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
