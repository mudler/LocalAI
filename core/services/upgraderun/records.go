package upgraderun

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/distributed"
)

// toRecords turns a run into its rows. Progress and the cancellable flag are
// not stored: they are read from the status of the operation.
func toRecords(r Run, runner string) (*distributed.UpgradeRunRecord, []distributed.UpgradeRunItemRecord, error) {
	rec := &distributed.UpgradeRunRecord{
		ID: r.ID, Source: string(r.Source), Actor: r.Actor, State: string(r.State),
		CancelRequested: r.CancelRequested, Dismissed: r.Dismissed, Runner: runner,
		CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
	}
	items := make([]distributed.UpgradeRunItemRecord, len(r.Items))
	for i, it := range r.Items {
		nodes, err := marshalOrEmpty(it.Nodes)
		if err != nil {
			return nil, nil, fmt.Errorf("upgrade run %s, item %s: encoding the nodes: %w", r.ID, it.Key, err)
		}
		extra, err := marshalOrEmpty(it.Extra)
		if err != nil {
			return nil, nil, fmt.Errorf("upgrade run %s, item %s: encoding the extra data: %w", r.ID, it.Key, err)
		}
		items[i] = distributed.UpgradeRunItemRecord{
			RunID: r.ID, Key: it.Key, Position: i, Kind: string(it.Kind), Name: it.Name, Title: it.Title,
			From: it.From, To: it.To, ExpectedTo: it.ExpectedTo, Phase: string(it.Phase), Message: it.Message,
			ErrorKey: it.ErrorKey, Error: it.Error, Retryable: it.Retryable, JobID: it.JobID,
			QueuedReason: it.QueuedReason, Verifying: it.Verifying, CancelWanted: it.CancelWanted, CancelSent: it.CancelSent,
			Nodes: nodes, Extra: extra,
		}
	}
	return rec, items, nil
}

// marshalOrEmpty encodes v as JSON, or returns "" for a value with nothing in
// it (null, an empty object or an empty list).
func marshalOrEmpty[T any](v T) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if string(b) == "null" || string(b) == "{}" || string(b) == "[]" {
		return "", nil
	}
	return string(b), nil
}

// corruptColumnError is a JSON column of an item row that does not decode.
// Its text names the row and the column, never the content of the row: the
// nodes and the extra data can hold node names, digests and error text.
type corruptColumnError struct {
	run, item, column string
	err               error
}

func (e *corruptColumnError) Error() string {
	return fmt.Sprintf("upgrade run %s, item %s: the %s column is not valid JSON", e.run, e.item, e.column)
}

func (e *corruptColumnError) Unwrap() error { return e.err }

// fromRecords turns rows into a run. A settled updated item reads 100%; the
// progress of a running one is filled in from its operation by the caller.
//
// A JSON column that does not decode leaves its field empty and the rest of
// the run as stored: one damaged row must not make the run, or the list of
// runs, unreadable. The error says which rows and columns were damaged, so
// the caller can tell someone.
func fromRecords(rec *distributed.UpgradeRunRecord, items []distributed.UpgradeRunItemRecord) (Run, error) {
	r := Run{
		ID: rec.ID, Source: Source(rec.Source), Actor: rec.Actor, State: RunState(rec.State),
		CancelRequested: rec.CancelRequested, Dismissed: rec.Dismissed, CreatedAt: rec.CreatedAt, FinishedAt: rec.FinishedAt,
		Items: make([]Item, 0, len(items)),
	}
	var errs []error
	for _, ir := range items {
		it := Item{
			Key: ir.Key, Kind: Kind(ir.Kind), Name: ir.Name, Title: ir.Title, From: ir.From, To: ir.To, ExpectedTo: ir.ExpectedTo,
			// A row written by an older version may hold raw text.
			Phase: Phase(ir.Phase), Message: safeText(ir.Message), ErrorKey: ir.ErrorKey, Error: safeText(ir.Error), Retryable: ir.Retryable,
			JobID: ir.JobID, QueuedReason: ir.QueuedReason, Verifying: ir.Verifying, CancelWanted: ir.CancelWanted, CancelSent: ir.CancelSent,
		}
		if ir.Nodes != "" {
			if err := json.Unmarshal([]byte(ir.Nodes), &it.Nodes); err != nil {
				it.Nodes = nil
				errs = append(errs, &corruptColumnError{run: rec.ID, item: ir.Key, column: "nodes", err: err})
			}
			it.Nodes = safeNodes(it.Nodes)
		}
		if ir.Extra != "" {
			if err := json.Unmarshal([]byte(ir.Extra), &it.Extra); err != nil {
				it.Extra = nil
				errs = append(errs, &corruptColumnError{run: rec.ID, item: ir.Key, column: "extra", err: err})
			}
		}
		if it.Phase == PhaseUpdated || it.Verifying {
			it.Progress = 100
		}
		r.Items = append(r.Items, it)
	}
	return r, errors.Join(errs...)
}

// cancelFlags returns the cancel flags other frontends set on the rows.
func cancelFlags(rec *distributed.UpgradeRunRecord, items []distributed.UpgradeRunItemRecord) (bool, []string) {
	var keys []string
	for _, it := range items {
		if it.CancelRequested {
			keys = append(keys, it.Key)
		}
	}
	return rec.CancelRequested, keys
}
