// SPDX-License-Identifier: MIT
package assetpipeline

import "time"

type BranchSummary struct {
	ID      string    `json:"id"`
	Parent  string    `json:"parent,omitempty"`
	Created time.Time `json:"created"`
	State   string    `json:"state"`
}

type View struct {
	Name       string          `json:"name"`
	Revision   uint64          `json:"revision"`
	Branches   []BranchSummary `json:"branches"`
	Branch     string          `json:"branch"`
	Current    Snapshot        `json:"current"`
	Events     []EventView     `json:"events"`
	Before     int             `json:"before"`
	Next       string          `json:"next,omitempty"`
	Validation string          `json:"validation,omitempty"`
}

// Historical states are a presentation projection, never persisted events.
type EventView struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Node     string    `json:"node,omitempty"`
	Message  string    `json:"message"`
	Snapshot Snapshot  `json:"snapshot"`
}

func MakeView(w Workspace, branch string, before int) View {
	v := View{Name: w.Name, Revision: w.Revision}
	if branch == "" && len(w.Branches) > 0 {
		branch = w.Branches[len(w.Branches)-1].ID
	}

	for _, b := range w.Branches {
		s := b.Current()
		next, err := s.Next()
		state := "completed"
		if next != "" {
			state = "ready"
		}
		if err != nil {
			state = "invalid"
		}

		active, _ := s.Graph.Order()
		for _, id := range active {
			n := s.Nodes[id]
			if n.Status() == Waiting {
				state = "waiting"
			}
			if n.Status() == Failed || n.Status() == Interrupted {
				state = string(n.Status())
			}
		}
		if s.Busy() {
			state = "running"
		}

		parent := ""
		if b.Parent != nil {
			parent = b.Parent.ID
		}

		v.Branches = append(v.Branches, BranchSummary{ID: b.ID, Parent: parent, Created: b.Created, State: state})
		if b.ID != branch {
			continue
		}

		v.Branch, v.Current, v.Next = b.ID, s, next
		if err != nil {
			v.Validation = err.Error()
		}

		var history []*Event
		timelineNodes := map[string]string{}
		firstNode := ""
		for _, event := range b.History() {
			if firstNode == "" {
				firstNode = event.Node
			}
			if event.Message != "" {
				history = append(history, event)
				node := event.Node
				switch event.Kind {
				case CreatedKind, ConfigurationKind, ForkedKind, RunAllKind, RunAllCompletedKind, WarningKind, CancellingKind, InterruptedKind:
					node = ""
				case RetryKind, RunNodeKind:
					node = firstNode
				}

				timelineNodes[event.ID] = node
				firstNode = ""
			}
		}

		end := len(history)
		if before > 0 && before < end {
			end = before
		}

		start := max(0, end-20)
		for _, e := range history[start:end] {
			historical, replayErr := b.Replay(e.ID)
			if replayErr != nil {
				v.Validation = replayErr.Error()
				break
			}

			v.Events = append(v.Events, EventView{e.ID, e.At, string(e.Kind), timelineNodes[e.ID], e.Message, historical})
		}

		v.Before = start
	}

	return v
}
