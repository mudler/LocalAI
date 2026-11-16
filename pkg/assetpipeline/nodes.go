// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"errors"
	"fmt"
	"maps"

	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

// Node is a live projection. Inputs carry producer pointers, never lookup keys.
// Schema records and the executor arguments are boundary representations only.
type Node interface {
	ID() string
	Execution() *NodeState
	Dependencies() []Node
	ResolveInputs() (Value, map[string]Value, error)
	Configuration() NodeConfig
}

type TextProducer interface {
	Node
	SelectedTextOutput() (Value, error)
}

type ImageProducer interface {
	Node
	SelectedImageOutput() (*ImageAsset, error)
}

type MeshProducer interface {
	Node
	SelectedMeshOutput() (*MeshAsset, error)
}

type MotionProducer interface {
	Node
	SelectedMotionOutput() (Value, error)
}

// Immutable storage references. Full metadata is fetched by the authorized
// executor, not by the replay interpreter.
type ImageAsset struct{ ID string }
type MeshAsset struct {
	ID     string
	Format schema.MeshFormat
}

type Input interface {
	Resolve() (Value, error)
	Producer() Node
	Binding() Binding
}

type TextBinding interface {
	Input
	textBinding()
	visualBinding()
}

type ImageBinding interface {
	Input
	imageBinding()
	visualBinding()
}

type MeshBinding interface {
	Input
	meshBinding()
	skeletonBinding()
}

type MotionBinding interface {
	Input
	motionBinding()
	skeletonBinding()
}

type VisualBinding interface {
	Input
	visualBinding()
}

type SkeletonBinding interface {
	Input
	skeletonBinding()
}

type TextLiteral struct{ LiteralInput }

func (TextLiteral) textBinding()   {}
func (TextLiteral) visualBinding() {}

type ImageLiteral struct{ LiteralInput }

func (ImageLiteral) imageBinding()  {}
func (ImageLiteral) visualBinding() {}

type MeshLiteral struct{ LiteralInput }

func (MeshLiteral) meshBinding()     {}
func (MeshLiteral) skeletonBinding() {}

type MotionLiteral struct{ LiteralInput }

func (MotionLiteral) motionBinding()   {}
func (MotionLiteral) skeletonBinding() {}

type LiteralInput struct{ Value Value }

func (i LiteralInput) Producer() Node {
	return nil
}

func (i LiteralInput) Binding() Binding {
	v := i.Value
	return Binding{Value: &v}
}

func (i LiteralInput) Resolve() (Value, error) {
	return checkedValue(i.Value)
}

type TextInput struct{ Source TextProducer }

func (TextInput) textBinding()   {}
func (TextInput) visualBinding() {}

func (i TextInput) Producer() Node {
	return i.Source
}

func (i TextInput) Binding() Binding {
	return Binding{From: i.Source.ID()}
}

func (i TextInput) Resolve() (Value, error) {
	return i.Source.SelectedTextOutput()
}

type ImageInput struct{ Source ImageProducer }

func (ImageInput) imageBinding()  {}
func (ImageInput) visualBinding() {}

func (i ImageInput) Producer() Node {
	return i.Source
}

func (i ImageInput) Binding() Binding {
	return Binding{From: i.Source.ID()}
}

func (i ImageInput) Resolve() (Value, error) {
	a, err := i.Source.SelectedImageOutput()
	if err != nil {
		return Value{}, err
	}

	return Value{Type: schema.ImageValue, Artifact: a.ID}, nil
}

type MeshInput struct{ Source MeshProducer }

func (MeshInput) meshBinding()     {}
func (MeshInput) skeletonBinding() {}

func (i MeshInput) Producer() Node {
	return i.Source
}

func (i MeshInput) Binding() Binding {
	return Binding{From: i.Source.ID()}
}

func (i MeshInput) Resolve() (Value, error) {
	a, err := i.Source.SelectedMeshOutput()
	if err != nil {
		return Value{}, err
	}
	if !a.Format.Valid() {
		return Value{}, errors.New("unsupported mesh format")
	}

	return Value{Type: schema.MeshValue, Artifact: a.ID}, nil
}

type MotionInput struct{ Source MotionProducer }

func (MotionInput) motionBinding()   {}
func (MotionInput) skeletonBinding() {}

func (i MotionInput) Producer() Node {
	return i.Source
}

func (i MotionInput) Binding() Binding {
	return Binding{From: i.Source.ID()}
}

func (i MotionInput) Resolve() (Value, error) {
	return i.Source.SelectedMotionOutput()
}

type ProcessingOptions struct {
	Model  string
	Pause  bool
	Seed   int64
	Params map[string]string
}

type nodeInputs interface {
	primary() Input
	additional() map[string]Input
}

type SingleInput[T Input] struct{ Input T }

func (c *SingleInput[T]) primary() Input {
	return c.Input
}

func (c *SingleInput[T]) additional() map[string]Input {
	return nil
}

type ImageGenerationConfig struct {
	SingleInput[TextBinding]
	Count     int
	Reference ImageBinding
}

const ReferenceInput = "reference"

func (c *ImageGenerationConfig) additional() map[string]Input {
	if c.Reference == nil {
		return nil
	}
	return map[string]Input{ReferenceInput: c.Reference}
}

type SkinningConfig struct {
	SingleInput[MeshBinding]
	Skeleton SkeletonBinding
}

func (c *SkinningConfig) additional() map[string]Input {
	return map[string]Input{"skeleton": c.Skeleton}
}

type RetargetingConfig struct {
	SingleInput[MeshBinding]
	Motion MotionBinding
}

func (c *RetargetingConfig) additional() map[string]Input {
	return map[string]Input{"motion": c.Motion}
}

type BaseNode struct {
	id            string
	kind          Kind
	Options       ProcessingOptions
	inputs        nodeInputs
	inputsPresent bool
	State         *NodeState
}

func (n *BaseNode) ID() string {
	return n.id
}

func (n *BaseNode) Execution() *NodeState {
	return n.State
}

func (n *BaseNode) Dependencies() []Node {
	var result []Node
	if n.inputs.primary() != nil && n.inputs.primary().Producer() != nil {
		result = append(result, n.inputs.primary().Producer())
	}

	for _, input := range n.inputs.additional() {
		if input.Producer() != nil {
			result = append(result, input.Producer())
		}
	}

	return result
}

func (n *BaseNode) ResolveInputs() (Value, map[string]Value, error) {
	if n.inputs == nil || n.inputs.primary() == nil {
		return Value{}, nil, errors.New("input is not configured")
	}

	input, err := n.inputs.primary().Resolve()
	if err != nil {
		return Value{}, nil, err
	}

	var additional map[string]Value
	if len(n.inputs.additional()) != 0 {
		additional = map[string]Value{}
	}

	for name, binding := range n.inputs.additional() {
		value, err := binding.Resolve()
		if err != nil {
			return Value{}, nil, fmt.Errorf("%s: %w", name, err)
		}

		additional[name] = value
	}

	return input, additional, nil
}

func (n *BaseNode) Configuration() NodeConfig {
	c := n.Options
	out := NodeConfig{ID: n.id, Kind: n.kind, Model: c.Model, Pause: c.Pause, Seed: c.Seed, Params: maps.Clone(c.Params)}
	if image, ok := n.inputs.(*ImageGenerationConfig); ok {
		out.Count = image.Count
	}
	if n.inputs.primary() != nil {
		out.Input = n.inputs.primary().Binding()
	}
	if n.inputs.additional() != nil || n.inputsPresent {
		out.Inputs = map[string]Binding{}
		for name, input := range n.inputs.additional() {
			out.Inputs[name] = input.Binding()
		}
	}

	return out
}

func (n *BaseNode) selected(typ schema.ValueType) (Value, error) {
	if n.State.Status() != Succeeded || n.State.Selected == nil {
		return Value{}, errors.New("input is stale or has not been accepted")
	}
	if n.State.Selected.Type != typ {
		return Value{}, errors.New("producer output type mismatch")
	}

	return checkedValue(*n.State.Selected)
}

func checkedValue(v Value) (Value, error) {
	if v.Type == schema.TextValue {
		if v.Text == "" {
			return Value{}, errors.New("input text is empty")
		}
	} else if v.Artifact == "" {
		return Value{}, fmt.Errorf("input %s is missing", v.Type)
	}

	return v, nil
}

type textNode struct{ BaseNode }

func (n *textNode) SelectedTextOutput() (Value, error) {
	return n.selected(schema.TextValue)
}

type imageNode struct{ BaseNode }

func (n *imageNode) SelectedImageOutput() (*ImageAsset, error) {
	v, err := n.selected(schema.ImageValue)
	if err != nil {
		return nil, err
	}

	return &ImageAsset{ID: v.Artifact}, nil
}

type meshNode struct{ BaseNode }

func (n *meshNode) SelectedMeshOutput() (*MeshAsset, error) {
	v, err := n.selected(schema.MeshValue)
	if err != nil {
		return nil, err
	}

	return &MeshAsset{ID: v.Artifact, Format: schema.GLB}, nil
}

type motionNode struct{ BaseNode }

func (n *motionNode) SelectedMotionOutput() (Value, error) {
	return n.selected(schema.MotionValue)
}

type DescriptionNode struct {
	textNode
	Config SingleInput[TextBinding]
}

type PromptReviewNode struct {
	textNode
	Config SingleInput[TextBinding]
}

type ImageGenerationNode struct {
	imageNode
	Config ImageGenerationConfig
}

type ExtractionNode struct {
	imageNode
	Config SingleInput[ImageBinding]
}

type MeshGenerationNode struct {
	meshNode
	Config SingleInput[VisualBinding]
}

type RemeshingNode struct {
	meshNode
	Config SingleInput[MeshBinding]
}

type RiggingNode struct {
	meshNode
	Config SingleInput[MeshBinding]
}

type SkinningNode struct {
	meshNode
	Config SkinningConfig
}

type MotionGenerationNode struct {
	motionNode
	Config SingleInput[TextBinding]
}

type RetargetingNode struct {
	meshNode
	Config RetargetingConfig
}

type RuntimeGraph struct {
	Nodes   []Node
	Primary Node
	Target  Profile
	index   map[string]Node
}

// BuildGraph resolves external IDs once. Invalid editable configurations remain
// serializable, but cannot produce an executable runtime graph.
func BuildGraph(config Graph, states map[string]NodeState) (*RuntimeGraph, error) {
	s := copyState(Snapshot{Graph: config, Nodes: states})
	return buildGraph(s.Graph, executionStates(s.Nodes))
}

// The owner passes already isolated projection state. Public callers use BuildGraph.
func buildGraph(config Graph, states map[string]*NodeState) (*RuntimeGraph, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	g := &RuntimeGraph{Target: copyProfile(config.Target)}
	nodes := map[string]Node{}
	for _, c := range config.Nodes {
		if states[c.ID] == nil {
			states[c.ID] = &NodeState{}
		}
		base := BaseNode{
			id:            c.ID,
			kind:          c.Kind,
			Options:       ProcessingOptions{Model: c.Model, Pause: c.Pause, Seed: c.Seed, Params: maps.Clone(c.Params)},
			State:         states[c.ID],
			inputsPresent: c.Inputs != nil,
		}
		primary, err := connectInput(c.Input, nodes)
		if err != nil {
			return nil, err
		}

		secondary := map[string]Input{}
		for name, b := range c.Inputs {
			input, err := connectInput(b, nodes)
			if err != nil {
				return nil, err
			}

			secondary[name] = input
		}

		var n Node
		switch c.Kind {
		case Source:
			v := &DescriptionNode{textNode: textNode{base}, Config: SingleInput[TextBinding]{primary.(TextBinding)}}
			v.inputs = &v.Config
			n = v
		case Review:
			v := &PromptReviewNode{textNode: textNode{base}, Config: SingleInput[TextBinding]{primary.(TextBinding)}}
			v.inputs = &v.Config
			n = v
		case Image:
			v := &ImageGenerationNode{
				imageNode: imageNode{base},
				Config:    ImageGenerationConfig{SingleInput: SingleInput[TextBinding]{primary.(TextBinding)}, Count: c.Count},
			}
			if reference := secondary[ReferenceInput]; reference != nil {
				v.Config.Reference = reference.(ImageBinding)
			}
			v.inputs = &v.Config
			n = v
		case Extract:
			v := &ExtractionNode{imageNode: imageNode{base}, Config: SingleInput[ImageBinding]{primary.(ImageBinding)}}
			v.inputs = &v.Config
			n = v
		case Mesh:
			v := &MeshGenerationNode{meshNode: meshNode{base}, Config: SingleInput[VisualBinding]{primary.(VisualBinding)}}
			v.inputs = &v.Config
			n = v
		case Remesh:
			v := &RemeshingNode{meshNode: meshNode{base}, Config: SingleInput[MeshBinding]{primary.(MeshBinding)}}
			v.inputs = &v.Config
			n = v
		case Rig:
			v := &RiggingNode{meshNode: meshNode{base}, Config: SingleInput[MeshBinding]{primary.(MeshBinding)}}
			v.inputs = &v.Config
			n = v
		case Skin:
			v := &SkinningNode{
				meshNode: meshNode{base},
				Config:   SkinningConfig{SingleInput[MeshBinding]{primary.(MeshBinding)}, secondary["skeleton"].(SkeletonBinding)},
			}
			v.inputs = &v.Config
			n = v
		case Motion:
			v := &MotionGenerationNode{motionNode: motionNode{base}, Config: SingleInput[TextBinding]{primary.(TextBinding)}}
			v.inputs = &v.Config
			n = v
		case Retarget:
			v := &RetargetingNode{
				meshNode: meshNode{base},
				Config:   RetargetingConfig{SingleInput[MeshBinding]{primary.(MeshBinding)}, secondary["motion"].(MotionBinding)},
			}
			v.inputs = &v.Config
			n = v
		default:
			return nil, fmt.Errorf("unknown node kind %q", c.Kind)
		}

		nodes[c.ID] = n
		g.Nodes = append(g.Nodes, n)
	}

	g.Primary = nodes[config.Primary]
	g.index = nodes
	return g, nil
}

func connectInput(b Binding, nodes map[string]Node) (Input, error) {
	if b.Value != nil {
		literal := LiteralInput{*b.Value}
		switch b.Value.Type {
		case schema.TextValue:
			return TextLiteral{literal}, nil
		case schema.ImageValue:
			return ImageLiteral{literal}, nil
		case schema.MeshValue:
			return MeshLiteral{literal}, nil
		case schema.MotionValue:
			return MotionLiteral{literal}, nil
		default:
			return nil, errors.New("unsupported literal input type")
		}
	}

	switch producer := nodes[b.From].(type) {
	case TextProducer:
		return TextInput{producer}, nil
	case ImageProducer:
		return ImageInput{producer}, nil
	case MeshProducer:
		return MeshInput{producer}, nil
	case MotionProducer:
		return MotionInput{producer}, nil
	default:
		return nil, errors.New("input producer does not exist")
	}
}

func (g *RuntimeGraph) Order() []Node {
	seen := map[Node]bool{}
	var order []Node
	var visit func(Node)
	visit = func(n Node) {
		if seen[n] {
			return
		}

		seen[n] = true
		for _, p := range n.Dependencies() {
			visit(p)
		}

		order = append(order, n)
	}
	visit(g.Primary)
	return order
}

func (g *RuntimeGraph) Configuration() Graph {
	c := Graph{Primary: g.Primary.ID(), Target: copyProfile(g.Target)}
	for _, n := range g.Nodes {
		c.Nodes = append(c.Nodes, n.Configuration())
	}

	return c
}

func (g *RuntimeGraph) Descendants(root Node) []Node {
	if root == nil {
		return nil
	}

	dirty := map[Node]bool{root: true}
	var result []Node
	for _, n := range g.Nodes {
		for _, dependency := range n.Dependencies() {
			if dirty[dependency] {
				dirty[n] = true
			}
		}
		if dirty[n] {
			result = append(result, n)
		}
	}

	return result
}
