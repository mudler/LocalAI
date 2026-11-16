// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

func (b *Branch) finishRunAll(now time.Time) error {
	s := b.Current()
	if !s.RunAll {
		return nil
	}
	if b.current.Graph == nil {
		return nil
	}

	for _, node := range b.current.Graph.Order() {
		if node.Execution().Status() != Succeeded {
			return nil
		}
	}

	runAll := false
	return b.appendEvents(RunAllCompletedKind, "Run all completed", now, Event{Type: schema.ExecutionEvent, Action: schema.RunPolicy, RunAll: &runAll})
}

// Sources are saved data, not inference jobs. Materialize them in the same
// transaction as the command so a processing node never races its input.
func (w *Workspace) materializeSources(branch string, now time.Time) error {
	b, err := w.Branch(branch)
	if err != nil {
		return err
	}

	s := b.Current()
	if b.current.Graph == nil {
		return s.Graph.Validate()
	}

	for _, runtime := range b.current.Graph.Order() {
		n, id := runtime.Configuration(), runtime.ID()
		if n.Kind != Source || runtime.Execution().Status() == Succeeded {
			continue
		}
		if n.Input.Value == nil || n.Input.Value.Text == "" {
			return errors.New("input text is empty")
		}

		v := *n.Input.Value
		a := Attempt{
			ID: uuid.NewString(),
			Arguments: canonicalArguments(Arguments{
				Node:   copyNode(n),
				Input:  v,
				Target: copyProfile(s.Graph.Target),
			}),
			Status:   Succeeded,
			Started:  now,
			Finished: &now,
			Outputs:  []Value{v},
		}

		if err := b.appendEvents(SourceKind, "Saved description available as input", now,
			Event{
				Type:      schema.ExecutionEvent,
				Action:    schema.Started,
				Node:      id,
				AttemptID: a.ID,
				Arguments: &a.Arguments,
			},
			Event{
				Type:      schema.ExecutionEvent,
				Action:    schema.Finished,
				Node:      id,
				AttemptID: a.ID,
				Status:    Succeeded,
				Outputs:   []Value{v},
			},
			Event{Type: schema.ExecutionEvent, Action: schema.Selected, Node: id, AttemptID: a.ID, Selection: &v}); err != nil {
			return err
		}
		w.Revision++
	}

	return nil
}

// RunNode executes only the selected processing node. Its processing ancestors
// must already have accepted outputs; it cannot implicitly run stale inputs.
func (w *Workspace) RunNode(branch, node string, now time.Time) (string, error) {
	b, err := w.Branch(branch)
	if err != nil {
		return "", err
	}

	s := b.Current()
	if b.current.Graph == nil {
		return "", s.Graph.Validate()
	}

	runtime := b.current.node(node)
	if runtime == nil || runtime.Configuration().Kind == Source || !slices.Contains(b.current.Graph.Order(), runtime) {
		return "", errors.New("choose an active processing node")
	}
	if s.Busy() {
		return "", errors.New("execution already active")
	}

	if err := w.materializeSources(branch, now); err != nil {
		return "", err
	}

	s = b.Current()
	for _, dependency := range b.current.node(node).Dependencies() {
		input := dependency.Execution()
		if input.Status() != Succeeded || input.Selected == nil {
			return "", errors.New("input is stale or has not been accepted; run and accept its producer first")
		}
	}

	if state := s.Nodes[node]; state.Status() != Stale && state.Attempt != nil {
		branch, err = w.Retry(branch, node, now)
		if err != nil {
			return "", err
		}
	}

	b, _ = w.Branch(branch)
	s = b.Current()
	if err := b.appendEvents(RunNodeKind, "Run this node only", now, b.current.invalidateFrom(node, true)...); err != nil {
		return "", err
	}

	_, err = w.beginNode(branch, node, now)
	return branch, err
}

func (w *Workspace) RunAll(branch string, now time.Time) (string, error) {
	b, err := w.Branch(branch)
	if err != nil {
		return "", err
	}

	s := b.Current()
	if s.Busy() {
		return "", errors.New("cancel the running execution first")
	}
	if b.current.Graph == nil {
		return "", s.Graph.Validate()
	}

	order := b.current.Graph.Order()
	branch, err = w.Fork(branch, b.events[len(b.events)-1].ID, now)
	if err != nil {
		return "", err
	}

	b, _ = w.Branch(branch)

	dirty := map[string]bool{}
	for _, node := range order {
		dirty[node.ID()] = true
	}

	events := invalidationEvents(s.Graph, dirty)
	runAll := true
	events = append(events, Event{Type: schema.ExecutionEvent, Action: schema.RunPolicy, RunAll: &runAll})
	if err := b.appendEvents(RunAllKind, "Run all active stages from the beginning", now, events...); err != nil {
		return "", err
	}

	return branch, w.Continue(branch, now)
}

func (w *Workspace) Continue(branch string, now time.Time) error {
	b, err := w.Branch(branch)
	if err != nil {
		return err
	}
	if !b.Current().RunAll {
		return errors.New("no Run all execution to continue")
	}

	if err := w.materializeSources(branch, now); err != nil {
		return err
	}

	s := b.Current()
	id, err := b.current.Next()
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("no node is ready; accept pending output first")
	}
	if s.Nodes[id].Status() == Failed || s.Nodes[id].Status() == Interrupted {
		return errors.New("rerun the failed or interrupted node before continuing")
	}

	_, err = w.beginNode(branch, id, now)
	return err
}
