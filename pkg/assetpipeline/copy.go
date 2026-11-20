// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"maps"
	"slices"
)

func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyBinding(b Binding) Binding {
	b.Value = copyPointer(b.Value)
	return b
}

func copyNode(n NodeConfig) NodeConfig {
	n.Input = copyBinding(n.Input)
	n.Inputs = maps.Clone(n.Inputs)
	for k, b := range n.Inputs {
		n.Inputs[k] = copyBinding(b)
	}
	n.Params = maps.Clone(n.Params)
	return n
}

func copyProfile(p Profile) Profile {
	p.Inputs = slices.Clone(p.Inputs)
	return p
}

func copyGraph(g Graph) Graph {
	g.Nodes = slices.Clone(g.Nodes)
	for i := range g.Nodes {
		g.Nodes[i] = copyNode(g.Nodes[i])
	}
	g.Target = copyProfile(g.Target)
	return g
}

func copyArguments(a Arguments) Arguments {
	a.Node = copyNode(a.Node)
	a.Inputs = maps.Clone(a.Inputs)
	a.Target = copyProfile(a.Target)
	return a
}

func copyFailure(f *Failure) *Failure {
	if f == nil {
		return nil
	}
	out := *f
	out.Cause = copyFailure(f.Cause)
	return &out
}

func copyAttempt(a *Attempt) *Attempt {
	if a == nil {
		return nil
	}
	out := *a
	out.Arguments = copyArguments(a.Arguments)
	out.Finished = copyPointer(a.Finished)
	out.Outputs = slices.Clone(a.Outputs)
	out.Diagnostics = slices.Clone(a.Diagnostics)
	out.Failure = copyFailure(a.Failure)
	return &out
}

func copyObservations(m map[string]any) map[string]any {
	out := maps.Clone(m)
	for k, v := range out {
		out[k] = copyObservation(v)
	}
	return out
}

func copyEvent(e Event) Event {
	if e.Arguments != nil {
		a := copyArguments(*e.Arguments)
		e.Arguments = &a
	}
	e.Outputs = slices.Clone(e.Outputs)
	e.Selection = copyPointer(e.Selection)
	e.Failure = copyFailure(e.Failure)
	e.Diagnostics = slices.Clone(e.Diagnostics)
	e.RunAll = copyPointer(e.RunAll)
	switch v := e.Value.(type) {
	case Binding:
		e.Value = copyBinding(v)
	case map[string]Binding:
		m := maps.Clone(v)
		for k, b := range m {
			m[k] = copyBinding(b)
		}
		e.Value = m
	case map[string]string:
		e.Value = maps.Clone(v)
	case []string:
		e.Value = slices.Clone(v)
	case Profile:
		e.Value = copyProfile(v)
	case map[string]any, []any:
		e.Value = copyObservation(v)
	}
	return e
}

func copyObservation(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = copyObservation(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = copyObservation(item)
		}
		return out
	default:
		return v
	}
}
