// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Receipts and the pipeline head contain only manifests. Event counts pin each
// branch to an exact prefix, so replaying an old receipt cannot see later events.
type journalBranch struct {
	branchDocument
	Count int `json:"count"`
}

type journalManifest struct {
	Version      int             `json:"version"`
	ResultBranch string          `json:"resultBranch,omitempty"`
	Name         string          `json:"name"`
	Revision     uint64          `json:"revision"`
	Branches     []journalBranch `json:"branches"`
}

type journalReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func branchLineage(w Workspace) map[string]string {
	out := map[string]string{}
	for _, b := range w.Branches {
		parent, checkpoint := "", ""
		if b.Parent != nil {
			parent = b.Parent.ID
		}
		if b.checkpoint != nil {
			checkpoint = b.checkpoint.ID
		}

		out[b.ID] = parent + "/" + checkpoint + "/" + b.Created.String()
	}

	return out
}

func readJournal(ctx context.Context, db journalReader, resource string, raw []byte) (Workspace, error) {
	var m journalManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Workspace{}, err
	}
	if m.Version != 3 {
		return Workspace{}, fmt.Errorf("unsupported pipeline journal format %d", m.Version)
	}

	d := workspaceDocument{Version: 2, Name: m.Name, Revision: m.Revision, ResultBranch: m.ResultBranch}
	for _, b := range m.Branches {
		if b.Count < 1 || len(b.Events) != 0 {
			return Workspace{}, errors.New("invalid journal branch manifest")
		}

		bd := b.branchDocument
		rows, err := db.QueryContext(ctx, "SELECT position, body FROM pipeline_events WHERE resource=? AND branch=? AND position<? ORDER BY position", resource, b.ID, b.Count)
		if err != nil {
			return Workspace{}, err
		}

		for rows.Next() {
			var position int
			var body []byte
			if err = rows.Scan(&position, &body); err != nil {
				break
			}
			if position != len(bd.Events) {
				err = errors.New("gap in pipeline event log")
				break
			}

			bd.Events = append(bd.Events, json.RawMessage(body))
		}
		if err == nil {
			err = rows.Err()
		}

		_ = rows.Close()
		if err != nil {
			return Workspace{}, err
		}
		if len(bd.Events) != b.Count {
			return Workspace{}, errors.New("incomplete pipeline event log")
		}

		d.Branches = append(d.Branches, bd)
	}

	return restoreWorkspace(d)
}

func appendJournal(ctx context.Context, tx *sql.Tx, resource string, w Workspace) ([]byte, error) {
	// Validate references/replay before publishing a manifest. This also rejects
	// callers bypassing the command layer with malformed event operations.
	raw, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	if len(raw) > 64<<20 {
		return nil, errors.New("pipeline history exceeds 64 MiB; create a new pipeline")
	}

	var checked Workspace
	if err := json.Unmarshal(raw, &checked); err != nil {
		return nil, err
	}
	type storedEvent struct {
		branch   string
		position int
		body     []byte
	}

	existing := map[string]storedEvent{}
	rows, err := tx.QueryContext(ctx, "SELECT id, branch, position, body FROM pipeline_events WHERE resource=?", resource)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var id string
		var e storedEvent
		if err = rows.Scan(&id, &e.branch, &e.position, &e.body); err != nil {
			break
		}

		existing[id] = e
	}
	if err == nil {
		err = rows.Err()
	}

	_ = rows.Close()
	if err != nil {
		return nil, err
	}

	m := journalManifest{Version: 3, Name: w.Name, Revision: w.Revision, ResultBranch: w.ResultBranch}
	for _, b := range w.Branches {
		bd := branchDocument{ID: b.ID, Created: b.Created}
		if b.Parent != nil {
			bd.Parent, bd.Checkpoint = b.Parent.ID, b.checkpoint.ID
		}

		m.Branches = append(m.Branches, journalBranch{branchDocument: bd, Count: len(b.events)})
		for position, e := range b.events {
			body, err := json.Marshal(e)
			if err != nil {
				return nil, err
			}

			if prior, found := existing[e.ID]; found {
				if prior.branch != b.ID || prior.position != position || !bytes.Equal(prior.body, body) {
					return nil, fmt.Errorf("cannot rewrite recorded event %s", e.ID)
				}
				delete(existing, e.ID)
				continue
			}

			if _, err := tx.ExecContext(ctx, "INSERT INTO pipeline_events(resource,branch,position,id,body) VALUES(?,?,?,?,?)", resource, b.ID, position, e.ID, body); err != nil {
				return nil, err
			}
		}
	}
	if len(existing) != 0 {
		return nil, errors.New("cannot remove recorded events")
	}

	return json.Marshal(m)
}
