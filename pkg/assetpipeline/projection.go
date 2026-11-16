// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

// State retains editable configuration even when it cannot form an executable
// graph. Graph is a derived connection of that configuration and execution state.
type State struct {
	Graph         *RuntimeGraph
	configuration Graph
	nodes         map[string]*NodeState
	attempts      map[string]*Attempt
	observations  map[string]any
	runAll        bool
	last          string
}

func StateFromSnapshot(snapshot Snapshot) State {
	snapshot = copyState(snapshot)
	s := State{}
	s.replaceConfiguration(snapshot)
	return s
}

func (s *State) replaceConfiguration(snapshot Snapshot) {
	s.configuration = snapshot.Graph
	s.nodes = executionStates(snapshot.Nodes)
	for _, n := range s.configuration.Nodes {
		if s.nodes[n.ID] == nil {
			s.nodes[n.ID] = &NodeState{}
		}
	}
	s.attempts = snapshot.Attempts
	if s.attempts == nil {
		s.attempts = map[string]*Attempt{}
	}
	s.observations = snapshot.Observations
	s.runAll, s.last = snapshot.RunAll, snapshot.Last
	s.Graph, _ = buildGraph(s.configuration, s.nodes)
}

// snapshot borrows payloads inside the owner; Snapshot isolates external callers.
func (s State) snapshot() Snapshot {
	nodes := make(map[string]NodeState, len(s.nodes))
	for id, n := range s.nodes {
		nodes[id] = *n
	}
	return Snapshot{Graph: s.configuration, Nodes: nodes, Attempts: s.attempts,
		Observations: s.observations, RunAll: s.runAll, Last: s.last}
}

func (s State) Snapshot() Snapshot {
	return copyState(s.snapshot())
}

func (s State) copy() State {
	return StateFromSnapshot(s.snapshot())
}

func (s State) node(id string) Node {
	if s.Graph != nil {
		return s.Graph.index[id]
	}

	return nil
}

func (s State) Next() (string, error) {
	if s.Graph == nil {
		return "", s.snapshot().Graph.Validate()
	}

	return nextNode(s.Graph)
}

func nextNode(graph *RuntimeGraph) (string, error) {
	for _, n := range graph.Order() {
		status := n.Execution().Status()
		if status == Running || status == Waiting {
			return "", nil
		}
		if status != Succeeded {
			return n.ID(), nil
		}
	}

	return "", nil
}

func (s State) invalidateFrom(id string, includeRoot bool) []Event {
	if s.Graph == nil {
		g := s.snapshot().Graph
		dirty := g.Descendants(id)
		if !includeRoot {
			delete(dirty, id)
		}

		return invalidationEvents(g, dirty)
	}

	root := s.node(id)
	var events []Event
	for _, n := range s.Graph.Descendants(root) {
		if n == root && !includeRoot {
			continue
		}

		events = append(events, Event{Type: schema.ExecutionEvent, Action: schema.Invalidated, Node: n.ID()})
	}

	return events
}

// The connected nodes and the owner share one execution record per node.
func executionStates(states map[string]NodeState) map[string]*NodeState {
	out := make(map[string]*NodeState, len(states))
	for id, state := range states {
		out[id] = &state
	}
	return out
}
