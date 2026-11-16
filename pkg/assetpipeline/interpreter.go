// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

// ApplyEvent is the only event dispatcher. Replay performs no I/O, inference,
// clock reads or ID allocation.
func ApplyEvent(state *State, e *Event) error {
	if e.Version != 2 {
		return fmt.Errorf("unsupported pipeline event version %d", e.Version)
	}

	switch e.Type {
	case schema.ConfigurationEvent:
		if e.Action != "" || e.hasExecutionPayload() {
			return errors.New("execution fields in configuration event")
		}

		projection := state.snapshot()
		if err := applyConfigurationEvent(&projection, e); err != nil {
			return err
		}
		state.replaceConfiguration(projection)
		return nil
	case schema.ExecutionEvent:
		if e.Path != "" || e.ValueType != "" || e.Value != nil {
			return errors.New("configuration fields in execution event")
		}

		return applyExecutionEvent(state, e)
	case schema.SystemEvent:
		if e.Action != schema.Observed || e.Path == "" {
			return errors.New("invalid system observation")
		}
		if e.Node != "" || e.hasExecutionPayload() {
			return errors.New("execution fields in system observation")
		}

		if !validObservation(e.ValueType, e.Value) {
			return errors.New("invalid observation value")
		}
		if state.observations == nil {
			state.observations = map[string]any{}
		}
		state.observations[string(e.Path)] = copyObservation(e.Value)

		return nil
	default:
		return fmt.Errorf("unknown event type %q", e.Type)
	}
}

func applyExecutionEvent(s *State, e *Event) error {
	if err := validateExecutionEvent(e); err != nil {
		return err
	}
	if e.Action == schema.Notice {
		return nil
	}
	if e.Action == schema.RunPolicy {
		if e.RunAll == nil {
			return errors.New("missing run policy")
		}

		s.runAll = *e.RunAll

		return nil
	}
	if e.Action == schema.Removed {
		if _, exists := s.configuration.Node(e.Node); exists {
			return errors.New("cannot remove state of configured node")
		}
		delete(s.nodes, e.Node)
		return nil
	}
	if _, exists := s.configuration.Node(e.Node); !exists {
		return errors.New("execution node not found")
	}
	if e.Action == schema.Invalidated {
		n := s.nodes[e.Node]
		n.Stale, n.Selected = true, nil
		return nil
	}
	if s.Graph == nil {
		return errors.New("cannot execute an invalid configuration")
	}

	node := s.node(e.Node)
	if node == nil {
		return errors.New("execution node not found")
	}

	n := *node.Execution()
	switch e.Action {
	case schema.Started:
		if e.AttemptID == "" || e.Arguments == nil || e.Arguments.Node.ID != e.Node || s.attempts[e.AttemptID] != nil {
			return errors.New("invalid or duplicate attempt")
		}

		input, inputs, err := node.ResolveInputs()
		if err != nil {
			return err
		}
		expected := canonicalArguments(Arguments{Node: node.Configuration(), Input: input, Inputs: inputs, Target: s.Graph.Target})
		if !reflect.DeepEqual(canonicalArguments(*e.Arguments), expected) {
			return errors.New("attempt arguments do not match configured node and inputs")
		}
		if s.snapshot().Busy() {
			return errors.New("execution already active")
		}

		a := &Attempt{ID: e.AttemptID, Arguments: copyArguments(*e.Arguments), Status: Running, Started: e.At}
		if s.attempts == nil {
			s.attempts = map[string]*Attempt{}
		}

		s.attempts[a.ID] = a
		n = NodeState{Attempt: a}
		s.last = e.Node
	case schema.Progress, schema.Finished:
		if n.Stale || n.Attempt == nil || n.Attempt.ID != e.AttemptID || n.Attempt.Status != Running {
			return errors.New("attempt is no longer running")
		}

		a := *n.Attempt
		if e.Action == schema.Progress {
			a.Progress, a.JobID = e.Progress, e.JobID
		} else {
			if !terminalOutcome(e.Status) {
				return errors.New("invalid terminal outcome")
			}
			if e.Status == Succeeded && e.Failure != nil {
				return errors.New("successful attempt cannot have a failure")
			}
			if e.Status == Succeeded {
				if len(e.Outputs) == 0 {
					return errors.New("successful attempt needs output")
				}

				for _, output := range e.Outputs {
					if output.Type != outputType(a.Arguments.Node) {
						return errors.New("output type does not match node")
					}

					if _, err := checkedValue(output); err != nil {
						return err
					}
				}
			}

			a.Status = e.Status
			a.Finished = copyPointer(&e.At)
			a.Outputs = slices.Clone(e.Outputs)
			a.Explanation = e.Explanation
			a.Failure = copyFailure(e.Failure)
			a.Diagnostics = slices.Clone(e.Diagnostics)
		}

		n.Attempt = &a
		s.attempts[a.ID] = &a
	case schema.Selected:
		if n.Stale || n.Attempt == nil || n.Attempt.ID != e.AttemptID || n.Attempt.Status != Succeeded || e.Selection == nil {
			return errors.New("output is not available for selection")
		}

		found := false
		for _, output := range n.Attempt.Outputs {
			editedText := output.Type == schema.TextValue && e.Selection.Type == schema.TextValue &&
				e.Selection.Text != "" && e.Selection.Artifact == ""
			if output == *e.Selection || editedText {
				found = true
			}
		}
		if !found {
			return errors.New("selection is not an output of this attempt")
		}

		n.Selected = copyPointer(e.Selection)
	}
	*node.Execution() = n
	return nil
}

// appendEvents validates the entire command on an isolated projection before
// publishing any events. Store.Update commits the stream atomically to SQLite.
func (b *Branch) appendEvents(kind EventKind, message string, now time.Time, events ...Event) error {
	if len(events) == 0 {
		events = []Event{{Type: schema.ExecutionEvent, Action: schema.Notice}}
	}

	events = slices.Clone(events)
	for i := range events {
		events[i] = copyEvent(events[i])
	}
	s := b.current.copy()
	for i := range events {
		e := &events[i]
		e.ID, e.At, e.Version, e.Kind = uuid.NewString(), now.UTC().Round(0), 2, kind
		if i == len(events)-1 {
			e.Message = message
		}

		if err := ApplyEvent(&s, e); err != nil {
			return err
		}
	}

	for i := range events {
		b.events = append(b.events, &events[i])
	}

	b.current = s
	return nil
}

func invalidationEvents(g Graph, dirty map[string]bool) []Event {
	var events []Event
	for _, n := range g.Nodes {
		if dirty[n.ID] {
			events = append(events, Event{Type: schema.ExecutionEvent, Action: schema.Invalidated, Node: n.ID})
		}
	}

	return events
}

// ConfigurationAt ignores all execution and observed-system events.
func (b *Branch) ConfigurationAt(checkpoint string) (Graph, error) {
	var s Snapshot
	found := checkpoint == ""
	for _, e := range b.History() {
		if e.Type == schema.ConfigurationEvent {
			if err := applyConfigurationEvent(&s, e); err != nil {
				return Graph{}, err
			}
		}
		if e.ID == checkpoint {
			found = true
			break
		}
	}
	if !found {
		return Graph{}, errors.New("checkpoint not found")
	}

	return copyGraph(s.Graph), nil
}

func failureFromError(err error) *Failure {
	if err == nil {
		return nil
	}

	if failure, ok := err.(*Failure); ok {
		return copyFailure(failure)
	}

	code := schema.UnknownFailure
	if errors.Is(err, context.Canceled) {
		code = schema.CancelledFailure
	}

	return &Failure{
		Code:    code,
		Message: strings.ToValidUTF8(err.Error(), "�"),
		Cause:   failureFromError(errors.Unwrap(err)),
	}
}

func terminalOutcome(s Status) bool {
	return s == Succeeded || s == Failed || s == Interrupted || s == Cancelled
}

func validObservation(typ schema.DataType, value any) bool {
	switch typ {
	case schema.String:
		_, ok := value.(string)
		return ok
	case schema.Boolean:
		_, ok := value.(bool)
		return ok
	case schema.Integer:
		_, ok := value.(int64)
		return ok
	case schema.Object:
		_, ok := value.(map[string]any)
		return ok
	case schema.Array:
		_, ok := value.([]any)
		return ok
	case schema.Null:
		return value == nil
	default:
		return false
	}
}

func validateExecutionEvent(e *Event) error {
	// Match allowed fields for each action, rejecting surprising hidden writes.
	allowed := *e
	allowed.ID = ""
	allowed.At = time.Time{}
	allowed.Version = 0
	allowed.Type = ""
	allowed.Kind = ""
	allowed.Node = ""
	allowed.Message = ""
	allowed.Action = ""
	switch e.Action {
	case schema.Started:
		allowed.AttemptID, allowed.Arguments = "", nil
	case schema.Progress:
		allowed.AttemptID, allowed.Progress, allowed.JobID = "", "", ""
	case schema.Finished:
		allowed.AttemptID, allowed.Status, allowed.Outputs, allowed.Explanation, allowed.Failure, allowed.Diagnostics = "", "", nil, "", nil, nil
	case schema.Selected:
		allowed.AttemptID, allowed.Selection = "", nil
	case schema.Invalidated, schema.Removed, schema.Notice:
	case schema.RunPolicy:
		allowed.RunAll = nil
	default:
		return fmt.Errorf("unknown execution action %q", e.Action)
	}
	if !reflect.DeepEqual(allowed, Event{}) {
		return errors.New("fields not allowed for execution action")
	}
	return nil
}
