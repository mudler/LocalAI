// SPDX-License-Identifier: MIT

// Package schema contains the pipeline's JSON vocabulary, not runtime nodes.
// Configuration documents never contain execution state.
package schema

type Kind string

const (
	Source   Kind = "source"
	Review   Kind = "review"
	Image    Kind = "image"
	Extract  Kind = "extract"
	Mesh     Kind = "mesh"
	Remesh   Kind = "remesh"
	Rig      Kind = "rig"
	Skin     Kind = "skin"
	Motion   Kind = "motion"
	Retarget Kind = "retarget"
)

type ValueType string

const (
	TextValue   ValueType = "text"
	ImageValue  ValueType = "image"
	MeshValue   ValueType = "mesh"
	MotionValue ValueType = "motion"
)

type Value struct {
	Type     ValueType `json:"type"`
	Text     string    `json:"text,omitempty"`
	Artifact string    `json:"artifact,omitempty"`
}

type Binding struct {
	From  string `json:"from,omitempty"`
	Value *Value `json:"value,omitempty"`
}

type Node struct {
	ID     string             `json:"id"`
	Kind   Kind               `json:"kind"`
	Model  string             `json:"model,omitempty"`
	Input  Binding            `json:"input"`
	Inputs map[string]Binding `json:"inputs,omitempty"`
	Pause  bool               `json:"pause"`
	Count  int                `json:"count,omitempty"`
	Seed   int64              `json:"seed"`
	Params map[string]string  `json:"params,omitempty"`
}

type MeshFormat string

// GLB is currently the only mesh container accepted by the asset pipeline.
const GLB MeshFormat = "glb"

func (f MeshFormat) Valid() bool {
	return f == GLB
}

func (f MeshFormat) MIME() string {
	if f == GLB {
		return "model/gltf-binary"
	}

	return ""
}
