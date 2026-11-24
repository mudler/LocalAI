// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// References are IDs on disk and pointers in memory. Neither the projection
// nor an ancestor's events are serialized as part of a branch.
type branchDocument struct {
	ID         string            `json:"id"`
	Parent     string            `json:"parent,omitempty"`
	Checkpoint string            `json:"checkpoint,omitempty"`
	Created    time.Time         `json:"created"`
	Events     []json.RawMessage `json:"events"`
}

type workspaceDocument struct {
	Version      int              `json:"version"`
	ResultBranch string           `json:"resultBranch,omitempty"`
	Name         string           `json:"name"`
	Revision     uint64           `json:"revision"`
	Branches     []branchDocument `json:"branches"`
}

func (w Workspace) MarshalJSON() ([]byte, error) {
	d := workspaceDocument{Version: 2, ResultBranch: w.ResultBranch, Name: w.Name, Revision: w.Revision}
	for _, b := range w.Branches {
		bd := branchDocument{ID: b.ID, Created: b.Created}
		if b.Parent != nil {
			if b.checkpoint == nil {
				return nil, errors.New("missing fork checkpoint")
			}

			bd.Parent, bd.Checkpoint = b.Parent.ID, b.checkpoint.ID
		}

		for _, e := range b.events {
			raw, err := json.Marshal(e)
			if err != nil {
				return nil, err
			}

			bd.Events = append(bd.Events, raw)
		}

		d.Branches = append(d.Branches, bd)
	}

	return json.Marshal(d)
}

func (w *Workspace) UnmarshalJSON(raw []byte) error {
	var d workspaceDocument
	if err := json.Unmarshal(raw, &d); err != nil {
		return err
	}
	out, err := restoreWorkspace(d)
	if err != nil {
		return err
	}
	*w = out
	return nil
}

func restoreWorkspace(d workspaceDocument) (Workspace, error) {
	if d.Version != 2 {
		return Workspace{}, fmt.Errorf("unsupported pipeline format %d", d.Version)
	}

	out := Workspace{Name: d.Name, Revision: d.Revision, ResultBranch: d.ResultBranch}
	docs := map[string]branchDocument{}
	branches := map[string]*Branch{}
	for _, bd := range d.Branches {
		if bd.ID == "" || branches[bd.ID] != nil {
			return Workspace{}, errors.New("missing or duplicate branch ID")
		}

		b := &Branch{ID: bd.ID, Created: bd.Created}
		branches[b.ID], docs[b.ID] = b, bd
		out.Branches = append(out.Branches, b)
	}

	visiting, done, eventIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var restore func(*Branch) error
	restore = func(b *Branch) error {
		if done[b.ID] {
			return nil
		}
		if visiting[b.ID] {
			return errors.New("cyclic branch ancestry")
		}

		visiting[b.ID] = true
		bd := docs[b.ID]
		if bd.Parent != "" {
			b.Parent = branches[bd.Parent]
			if b.Parent == nil {
				return errors.New("parent branch not found")
			}

			if err := restore(b.Parent); err != nil {
				return err
			}

			for _, e := range b.Parent.events {
				if e.ID == bd.Checkpoint {
					b.checkpoint = e
					break
				}
			}
			if b.checkpoint == nil {
				return errors.New("fork checkpoint not found")
			}

			var err error
			b.current, err = b.Parent.replayState(b.checkpoint.ID)
			if err != nil {
				return err
			}
		} else if bd.Checkpoint != "" {
			return errors.New("checkpoint without parent")
		}

		for _, raw := range bd.Events {
			var e Event
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			if e.ID == "" || eventIDs[e.ID] {
				return errors.New("missing or duplicate event ID")
			}

			eventIDs[e.ID] = true
			if err := ApplyEvent(&b.current, &e); err != nil {
				return fmt.Errorf("event %s: %w", e.ID, err)
			}

			b.events = append(b.events, &e)
		}
		if len(b.events) == 0 {
			return errors.New("branch has no events")
		}

		done[b.ID] = true
		return nil
	}

	for _, b := range out.Branches {
		if err := restore(b); err != nil {
			return Workspace{}, err
		}
	}
	return out, nil
}
