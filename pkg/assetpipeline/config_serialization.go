// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

func valueDocument(v Value) schema.Value { return schema.Value(v) }

func restoreValue(v schema.Value) Value { return Value(v) }

func valuesDocument(v []Value) []schema.Value {
	if v == nil {
		return nil
	}
	out := make([]schema.Value, len(v))
	for i, x := range v {
		out[i] = valueDocument(x)
	}
	return out
}

func restoreValues(v []schema.Value) []Value {
	if v == nil {
		return nil
	}
	out := make([]Value, len(v))
	for i, x := range v {
		out[i] = restoreValue(x)
	}
	return out
}

func bindingDocument(b Binding) schema.Binding {
	out := schema.Binding{From: b.From}
	if b.Value != nil {
		v := valueDocument(*b.Value)
		out.Value = &v
	}
	return out
}

func restoreBinding(b schema.Binding) Binding {
	out := Binding{From: b.From}
	if b.Value != nil {
		v := restoreValue(*b.Value)
		out.Value = &v
	}
	return out
}

func convertMap[A, B any](in map[string]A, convert func(A) B) map[string]B {
	if in == nil {
		return nil
	}
	out := make(map[string]B, len(in))
	for k, v := range in {
		out[k] = convert(v)
	}
	return out
}

func nodeDocument(n NodeConfig) schema.Node {
	return schema.Node{
		ID:     n.ID,
		Kind:   n.Kind,
		Model:  n.Model,
		Input:  bindingDocument(n.Input),
		Inputs: convertMap(n.Inputs, bindingDocument),
		Pause:  n.Pause,
		Count:  n.Count,
		Seed:   n.Seed,
		Params: n.Params,
	}
}

func restoreNode(n schema.Node) NodeConfig {
	return NodeConfig{
		ID:     n.ID,
		Kind:   n.Kind,
		Model:  n.Model,
		Input:  restoreBinding(n.Input),
		Inputs: convertMap(n.Inputs, restoreBinding),
		Pause:  n.Pause,
		Count:  n.Count,
		Seed:   n.Seed,
		Params: n.Params,
	}
}

func graphDocument(g Graph) schema.Graph {
	var nodes []schema.Node
	if g.Nodes != nil {
		nodes = make([]schema.Node, len(g.Nodes))
		for i, n := range g.Nodes {
			nodes[i] = nodeDocument(n)
		}
	}
	return schema.Graph{Nodes: nodes, Primary: g.Primary, Target: schema.Profile(g.Target)}
}

func restoreGraph(g schema.Graph) Graph {
	var nodes []NodeConfig
	if g.Nodes != nil {
		nodes = make([]NodeConfig, len(g.Nodes))
		for i, n := range g.Nodes {
			nodes[i] = restoreNode(n)
		}
	}
	return Graph{Nodes: nodes, Primary: g.Primary, Target: Profile(g.Target)}
}

func (v Value) MarshalJSON() ([]byte, error) { return json.Marshal(valueDocument(v)) }

func (v *Value) UnmarshalJSON(raw []byte) error {
	var d schema.Value
	if err := decodeStrict(raw, &d); err != nil {
		return err
	}
	*v = restoreValue(d)
	return nil
}

func (v Binding) MarshalJSON() ([]byte, error) { return json.Marshal(bindingDocument(v)) }

func (v *Binding) UnmarshalJSON(raw []byte) error {
	var d schema.Binding
	if err := decodeStrict(raw, &d); err != nil {
		return err
	}
	*v = restoreBinding(d)
	return nil
}

func (v NodeConfig) MarshalJSON() ([]byte, error) { return json.Marshal(nodeDocument(v)) }

func (v *NodeConfig) UnmarshalJSON(raw []byte) error {
	var d schema.Node
	if err := decodeStrict(raw, &d); err != nil {
		return err
	}
	*v = restoreNode(d)
	return nil
}

func (v Profile) MarshalJSON() ([]byte, error) { return json.Marshal(schema.Profile(v)) }

func (v *Profile) UnmarshalJSON(raw []byte) error {
	var d schema.Profile
	if err := decodeStrict(raw, &d); err != nil {
		return err
	}
	*v = Profile(d)
	return nil
}

func (v Graph) MarshalJSON() ([]byte, error) { return json.Marshal(graphDocument(v)) }

func (v *Graph) UnmarshalJSON(raw []byte) error {
	var d schema.Graph
	if err := decodeStrict(raw, &d); err != nil {
		return err
	}
	*v = restoreGraph(d)
	return nil
}
