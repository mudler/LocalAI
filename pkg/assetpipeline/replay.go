// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"errors"
	"fmt"
)

// Replay reconstructs this branch at an event, following only the parent's
// prefix. An empty checkpoint means the current head. It performs no inference.
func (b *Branch) Replay(checkpoint string) (Snapshot, error) {
	s, err := b.replayState(checkpoint)
	if err != nil {
		return Snapshot{}, err
	}

	return s.Snapshot(), nil
}

// Internal projections share immutable event data. Only the outward-facing
// Replay/Current copies need to isolate arbitrary caller edits.
func (b *Branch) replayState(checkpoint string) (State, error) {
	var s State
	if checkpoint != "" {
		owner := b.eventOwner(checkpoint)
		if owner == nil {
			return s, errors.New("checkpoint not found")
		}
		if owner != b {
			return owner.replayState(checkpoint)
		}
	}
	if b.Parent != nil {
		if b.checkpoint == nil {
			return s, errors.New("missing fork checkpoint")
		}

		var err error
		s, err = b.Parent.replayState(b.checkpoint.ID)
		if err != nil {
			return s, err
		}
	}

	for _, e := range b.events {
		if err := ApplyEvent(&s, e); err != nil {
			return State{}, fmt.Errorf("event %s: %w", e.ID, err)
		}
		if e.ID == checkpoint {
			return s, nil
		}
	}
	if checkpoint != "" {
		return State{}, errors.New("checkpoint not found")
	}

	return s, nil
}

// History follows ancestry only to the fork boundary. Events remain owned by
// their original branch; the returned slice contains references, not copies.
func (b *Branch) history() []*Event {
	var events []*Event
	if b.Parent != nil && b.checkpoint != nil {
		for _, e := range b.Parent.history() {
			events = append(events, e)
			if e == b.checkpoint {
				break
			}
		}
	}

	return append(events, b.events...)
}

func (b *Branch) eventOwner(id string) *Branch {
	for _, e := range b.events {
		if e.ID == id {
			return b
		}
	}
	if b.Parent != nil {
		for _, e := range b.history() {
			if e.ID == id {
				return b.Parent.eventOwner(id)
			}
		}
	}

	return nil
}

func copyState(s Snapshot) Snapshot {
	out := Snapshot{
		Graph:    copyGraph(s.Graph),
		RunAll:   s.RunAll,
		Last:     s.Last,
		Nodes:    map[string]NodeState{},
		Attempts: map[string]*Attempt{},
	}
	out.Observations = copyObservations(s.Observations)

	for id, a := range s.Attempts {
		out.Attempts[id] = copyAttempt(a)
	}

	for id, n := range s.Nodes {
		n.Selected = copyPointer(n.Selected)
		if n.Attempt != nil {
			if out.Attempts[n.Attempt.ID] == nil {
				out.Attempts[n.Attempt.ID] = copyAttempt(n.Attempt)
			}

			n.Attempt = out.Attempts[n.Attempt.ID]
		}

		out.Nodes[id] = n
	}

	return out
}

// Events returns detached records; callers cannot rewrite a branch's history.
func (b *Branch) Events() []*Event  { return copyEvents(b.events) }
func (b *Branch) History() []*Event { return copyEvents(b.history()) }
func (b *Branch) Checkpoint() *Event {
	if b.checkpoint == nil {
		return nil
	}
	e := copyEvent(*b.checkpoint)
	return &e
}
func copyEvents(events []*Event) []*Event {
	out := make([]*Event, len(events))
	for i, e := range events {
		copied := copyEvent(*e)
		out[i] = &copied
	}
	return out
}
