// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

type Status = schema.Status

const (
	Stale       Status = schema.Stale
	Running     Status = schema.Running
	Waiting     Status = schema.Waiting
	Succeeded   Status = schema.Succeeded
	Failed      Status = schema.Failed
	Interrupted Status = schema.Interrupted
	Cancelled   Status = schema.Cancelled
)

type NodeState struct {
	Stale    bool
	Attempt  *Attempt
	Selected *Value
}

// Status is branch readiness, not the outcome of its historical invocation.
func (n NodeState) Status() Status {
	if n.Stale || n.Attempt == nil {
		return Stale
	}
	if n.Attempt.Status == Succeeded && n.Selected == nil {
		return Waiting
	}
	if n.Attempt.Status == Cancelled {
		return Failed
	}

	return n.Attempt.Status
}

type Snapshot struct {
	RunAll       bool                 `json:"runAll,omitempty"`
	Graph        Graph                `json:"graph"`
	Nodes        map[string]NodeState `json:"nodes"`
	Last         string               `json:"last,omitempty"`
	Attempts     map[string]*Attempt  `json:"-"`
	Observations map[string]any       `json:"observations,omitempty"`
}

type Branch struct {
	ID         string
	Parent     *Branch
	checkpoint *Event
	Created    time.Time
	events     []*Event
	current    State
}

type Workspace struct {
	ResultBranch string
	Name         string
	Revision     uint64
	Branches     []*Branch
}

func New(name string, graph Graph, now time.Time) Workspace {
	now = now.UTC().Round(0)
	b := Branch{ID: uuid.NewString(), Created: now}
	events := configurationEvents(Graph{}, graph)
	for _, n := range graph.Nodes {
		events = append(events, Event{Type: schema.ExecutionEvent, Action: schema.Invalidated, Node: n.ID})
	}

	if err := b.appendEvents(CreatedKind, "Pipeline created", now, events...); err != nil {
		panic(err)
	}

	return Workspace{Name: name, Revision: 1, Branches: []*Branch{&b}}
}

func (w *Workspace) Branch(id string) (*Branch, error) {
	for i := range w.Branches {
		if w.Branches[i].ID == id {
			return w.Branches[i], nil
		}
	}

	return nil, errors.New("execution branch not found")
}

func (b *Branch) Current() Snapshot {
	return b.current.Snapshot()
}

func (s Snapshot) Busy() bool {
	for _, n := range s.Nodes {
		if n.Status() == Running {
			return true
		}
	}

	return false
}

func (s Snapshot) Next() (string, error) {
	g, err := BuildGraph(s.Graph, s.Nodes)
	if err != nil {
		return "", err
	}

	return nextNode(g)
}

func (w *Workspace) Fork(branch, checkpoint string, now time.Time) (string, error) {
	b, err := w.Branch(branch)
	if err != nil {
		return "", err
	}

	b = b.eventOwner(checkpoint)
	if b == nil {
		return "", errors.New("checkpoint not found")
	}

	for _, e := range b.events {
		if e.ID != checkpoint {
			continue
		}

		s, err := b.replayState(e.ID)
		if err != nil {
			return "", err
		}

		next := &Branch{ID: uuid.NewString(), Parent: b, checkpoint: e, Created: now, current: s}
		projection := s.snapshot()
		var events []Event
		// A historical running job belongs to its original branch. Forking its
		// checkpoint cannot claim that job or manufacture a completed output.
		for id, n := range projection.Nodes {
			if n.Status() == Running {
				events = append(events, Event{Type: schema.ExecutionEvent, Action: schema.Invalidated, Node: id})
			}
		}

		if err := next.appendEvents(ForkedKind, "Execution forked from checkpoint", now, events...); err != nil {
			return "", err
		}

		w.Branches = append(w.Branches, next)
		w.Revision++
		return next.ID, nil
	}

	return "", errors.New("checkpoint not found")
}

func (w *Workspace) Configure(branch string, graph Graph, now time.Time) error {
	b, err := w.Branch(branch)
	if err != nil {
		return err
	}

	s := b.Current()
	if s.Busy() {
		return errors.New("wait for the running node or cancel before editing")
	}
	if reflect.DeepEqual(s.Graph, graph) {
		return nil
	}

	old := s.Graph
	events := configurationEvents(old, graph)
	dirty := map[string]bool{}
	for _, n := range graph.Nodes {
		prev, ok := old.Node(n.ID)
		if !ok || !reflect.DeepEqual(prev, n) {
			// Pause only changes interaction policy; it does not change inputs.
			prev.Pause = n.Pause
			if ok && reflect.DeepEqual(prev, n) {
				continue
			}
			maps.Copy(dirty, graph.Descendants(n.ID))
		}
	}

	events = append(events, invalidationEvents(graph, dirty)...)
	for _, n := range old.Nodes {
		if _, exists := graph.Node(n.ID); !exists {
			events = append(events, Event{Type: schema.ExecutionEvent, Action: schema.Removed, Node: n.ID})
		}
	}

	message := configurationMessage(old, graph)
	if err := graph.Validate(); err != nil {
		message += "\nGraph cannot run: " + err.Error()
	}

	if err := b.appendEvents(ConfigurationKind, message, now, events...); err != nil {
		return err
	}
	if old.Target.Advice != graph.Target.Advice {
		if err := b.appendEvents(WarningKind, "Prompting advice changed for the selected 3D engine. Existing prompts were retained; review them before continuing.", now); err != nil {
			return err
		}
	}
	w.Revision++
	return nil
}

func (w *Workspace) Begin(branch string, now time.Time) (Attempt, error) {
	b, err := w.Branch(branch)
	if err != nil {
		return Attempt{}, err
	}

	s := b.Current()
	id, err := b.current.Next()
	if err != nil {
		return Attempt{}, err
	}
	if id == "" {
		return Attempt{}, errors.New("no node is ready to execute")
	}

	if status := s.Nodes[id].Status(); (status == Failed || status == Interrupted) && b.events[len(b.events)-1].Kind != ForkedKind {
		return Attempt{}, errors.New("use Retry to fork the failed or interrupted execution")
	}

	return w.beginNode(branch, id, now)
}

func (w *Workspace) beginNode(branch, id string, now time.Time) (Attempt, error) {
	b, err := w.Branch(branch)
	if err != nil {
		return Attempt{}, err
	}

	s := b.Current()
	if s.Busy() {
		return Attempt{}, errors.New("execution already active")
	}

	n, _ := s.Graph.Node(id)
	if b.current.Graph == nil {
		return Attempt{}, s.Graph.Validate()
	}

	runtime := b.current.node(id)
	if runtime == nil {
		return Attempt{}, errors.New("node not found")
	}

	input, inputs, err := runtime.ResolveInputs()
	if err != nil {
		return Attempt{}, err
	}
	if n.Kind != Source && !(n.Kind == Extract && n.Params["mode"] == "keep") && n.Model == "" {
		return Attempt{}, errors.New("select a model before executing")
	}

	a := Attempt{
		ID:        uuid.NewString(),
		Arguments: canonicalArguments(Arguments{Node: copyNode(n), Input: input, Inputs: inputs, Target: copyProfile(s.Graph.Target)}),
		Status:    Running,
		Started:   now,
	}
	if err := b.appendEvents(StartedKind, "Running "+string(n.Kind), now, Event{
		Type:      schema.ExecutionEvent,
		Action:    schema.Started,
		Node:      id,
		AttemptID: a.ID,
		Arguments: &a.Arguments,
	}); err != nil {
		return Attempt{}, err
	}
	w.Revision++
	return a, nil
}

func running(s Snapshot, attempt string) (string, NodeState, error) {
	for id, n := range s.Nodes {
		if n.Status() == Running && n.Attempt != nil && n.Attempt.ID == attempt {
			return id, n, nil
		}
	}

	return "", NodeState{}, errors.New("attempt is no longer running")
}

func (w *Workspace) Progress(branch, attempt, job, message string, now time.Time) error {
	b, err := w.Branch(branch)
	if err != nil {
		return err
	}

	s := b.Current()
	id, n, err := running(s, attempt)
	if err != nil {
		return err
	}

	if err := b.appendEvents(ProgressKind, message, now, Event{
		Type:      schema.ExecutionEvent,
		Action:    schema.Progress,
		Node:      id,
		AttemptID: n.Attempt.ID,
		JobID:     job,
		Progress:  message,
	}); err != nil {
		return err
	}
	w.Revision++
	return nil
}

func (w *Workspace) Complete(branch, attempt string, outputs []Value, explanation string, diagnostics []string, failure error, now time.Time) error {
	b, err := w.Branch(branch)
	if err != nil {
		return err
	}

	s := b.Current()
	id, n, err := running(s, attempt)
	if err != nil {
		return err
	}

	node := n.Attempt.Arguments.Node
	if failure == nil {
		if len(outputs) == 0 {
			failure = &Failure{Code: schema.InvalidOutputFailure, Message: "Backend returned no output"}
		}

		for _, v := range outputs {
			if v.Type != outputType(node) {
				failure = &Failure{
					Code:    schema.InvalidOutputFailure,
					Message: fmt.Sprintf("Backend returned unexpected output type %q", v.Type),
				}
			}

			if _, err := checkedValue(v); err != nil {
				failure = &Failure{Code: schema.InvalidOutputFailure, Message: err.Error()}
			}
		}
	}

	status := Succeeded
	message := "Node completed"
	if failure != nil {
		status, message = Failed, failure.Error()
		if errors.Is(failure, context.Canceled) {
			status = Cancelled
		}
	} else if node.Pause || len(outputs) > 1 {
		message = "Output ready — awaiting your decision"
	}

	problem := failureFromError(failure)
	events := []Event{{
		Type:        schema.ExecutionEvent,
		Action:      schema.Finished,
		Node:        id,
		AttemptID:   attempt,
		Status:      status,
		Outputs:     outputs,
		Explanation: explanation,
		Diagnostics: diagnostics,
		Failure:     problem,
	}}
	if status == Succeeded && !node.Pause && len(outputs) == 1 {
		events = append(events, Event{
			Type:      schema.ExecutionEvent,
			Action:    schema.Selected,
			Node:      id,
			AttemptID: attempt,
			Selection: &outputs[0],
		})
	}

	if err := b.appendEvents(CompletedKind, message, now, events...); err != nil {
		return err
	}

	if err := b.finishRunAll(now); err != nil {
		return err
	}
	w.Revision++
	return nil
}

func (w *Workspace) Select(branch, node string, index int, editedText *string, now time.Time) error {
	b, err := w.Branch(branch)
	if err != nil {
		return err
	}

	s := b.Current()
	if s.Busy() {
		return errors.New("wait for the running node before changing selection")
	}

	n := s.Nodes[node]
	if n.Attempt == nil || (n.Status() != Waiting && n.Status() != Succeeded) || index < 0 || index >= len(n.Attempt.Outputs) {
		return errors.New("output is not available for selection")
	}

	v := n.Attempt.Outputs[index]
	if editedText != nil {
		if v.Type != schema.TextValue || *editedText == "" {
			return errors.New("only nonempty text outputs can be edited")
		}

		v.Text = *editedText
	}

	events := b.current.invalidateFrom(node, false)
	events = append(events, Event{
		Type:      schema.ExecutionEvent,
		Action:    schema.Selected,
		Node:      node,
		AttemptID: n.Attempt.ID,
		Selection: &v,
	})
	if err := b.appendEvents(SelectionKind, "Output accepted", now, events...); err != nil {
		return err
	}

	if err := b.finishRunAll(now); err != nil {
		return err
	}
	w.Revision++
	return nil
}

func (w *Workspace) Retry(branch, node string, now time.Time) (string, error) {
	b, err := w.Branch(branch)
	if err != nil {
		return "", err
	}

	if _, ok := b.Current().Graph.Node(node); !ok {
		return "", errors.New("node not found")
	}

	id, err := w.Fork(branch, b.events[len(b.events)-1].ID, now)
	if err != nil {
		return "", err
	}

	b, _ = w.Branch(id)
	if err := b.appendEvents(RetryKind, "Retry from this node", now, b.current.invalidateFrom(node, true)...); err != nil {
		return "", err
	}

	return id, nil
}
