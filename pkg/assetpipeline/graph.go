// SPDX-License-Identifier: MIT

// Package assetpipeline defines the recorded inputs and transitions of an asset
// workflow. It has no inference dependency: adapters consume resolved arguments
// and report outputs, while human decisions remain explicit history events.
package assetpipeline

import "github.com/mudler/LocalAI/pkg/assetpipeline/schema"

type Kind = schema.Kind

const (
	Source   Kind = schema.Source
	Review   Kind = schema.Review
	Image    Kind = schema.Image
	Extract  Kind = schema.Extract
	Mesh     Kind = schema.Mesh
	Remesh   Kind = schema.Remesh
	Rig      Kind = schema.Rig
	Skin     Kind = schema.Skin
	Motion   Kind = schema.Motion
	Retarget Kind = schema.Retarget
)

type Value struct {
	Type     ValueType
	Text     string
	Artifact string
}
type ValueType = schema.ValueType
type Binding struct {
	From  string
	Value *Value
}
type NodeConfig struct {
	ID     string
	Kind   Kind
	Model  string
	Input  Binding
	Inputs map[string]Binding
	Pause  bool
	Count  int
	Seed   int64
	Params map[string]string
}

// Profile records selected model capabilities and prompt guidance, never LLM output.
// Advice is advisory: changing it does not invalidate an accepted prompt.
type Profile struct {
	Model   string
	Inputs  []string
	Advice  string
	Version string
}

type Graph struct {
	Nodes   []NodeConfig
	Primary string
	Target  Profile
}

func DefaultGraph() Graph {
	return Graph{Primary: "mesh", Target: Profile{
		Inputs:  []string{"image"},
		Version: "1",
		Advice:  "A single complete game asset, centered on a plain black background; no text, scenery, cast shadows or cropped parts.",
	}, Nodes: []NodeConfig{
		{ID: "source", Kind: Source, Input: Binding{Value: &Value{Type: schema.TextValue}}},
		{ID: "review", Kind: Review, Input: Binding{From: "source"}, Pause: true},
		{ID: "image", Kind: Image, Input: Binding{From: "review"}, Count: 1, Pause: true},
		{
			ID:     "extract",
			Kind:   Extract,
			Input:  Binding{From: "image"},
			Pause:  true,
			Params: map[string]string{"mode": "keep"},
		},
		{ID: "mesh", Kind: Mesh, Input: Binding{From: "extract"}},
		{
			ID:     "remesh",
			Kind:   Remesh,
			Input:  Binding{From: "mesh"},
			Params: map[string]string{"mode": "wrap_quad"},
		},
		{ID: "rig", Kind: Rig, Input: Binding{From: "remesh"}},
		{ID: "motion-description", Kind: Source, Input: Binding{Value: &Value{Type: schema.TextValue}}},
		{
			ID:     "motion-review",
			Kind:   Review,
			Input:  Binding{From: "motion-description"},
			Pause:  true,
			Params: map[string]string{"purpose": "motion"},
		},
		{ID: "motion", Kind: Motion, Input: Binding{From: "motion-review"}},
		{
			ID:     "skin",
			Kind:   Skin,
			Input:  Binding{From: "remesh"},
			Inputs: map[string]Binding{"skeleton": {From: "motion"}},
		},
		{
			ID:     "retarget",
			Kind:   Retarget,
			Input:  Binding{From: "rig"},
			Inputs: map[string]Binding{"motion": {From: "motion"}},
		},
	}}
}
