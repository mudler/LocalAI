package upgraderun

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/distributed"
)

// toRecords turns a run into its rows. Progress and the cancellable flag are
// not stored: they are read from the status of the operation.
func toRecords(r Run, runner string) (*distributed.UpgradeRunRecord, []distributed.UpgradeRunItemRecord) {
	rec := &distributed.UpgradeRunRecord{
		ID: r.ID, Source: string(r.Source), Actor: r.Actor, State: string(r.State),
		CancelRequested: r.CancelRequested, Dismissed: r.Dismissed, Runner: runner,
		CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
	}
	items := make([]distributed.UpgradeRunItemRecord, len(r.Items))
	for i, it := range r.Items {
		items[i] = distributed.UpgradeRunItemRecord{
			RunID: r.ID, Key: it.Key, Position: i, Kind: string(it.Kind), Name: it.Name, Title: it.Title,
			From: it.From, To: it.To, ExpectedTo: it.ExpectedTo, Phase: string(it.Phase), Message: it.Message,
			ErrorKey: it.ErrorKey, Error: it.Error, Retryable: it.Retryable, JobID: it.JobID,
			QueuedReason: it.QueuedReason, Verifying: it.Verifying, CancelWanted: it.CancelWanted, CancelSent: it.CancelSent,
			Nodes: marshalOrEmpty(it.Nodes), Extra: marshalOrEmpty(it.Extra),
		}
	}
	return rec, items
}

func marshalOrEmpty[T any](v T) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" || string(b) == "{}" || string(b) == "[]" {
		return ""
	}
	return string(b)
}

// fromRecords turns rows into a run. A settled updated item reads 100%; the
// progress of a running one is filled in from its operation by the caller.
func fromRecords(rec *distributed.UpgradeRunRecord, items []distributed.UpgradeRunItemRecord) Run {
	r := Run{
		ID: rec.ID, Source: Source(rec.Source), Actor: rec.Actor, State: RunState(rec.State),
		CancelRequested: rec.CancelRequested, Dismissed: rec.Dismissed, CreatedAt: rec.CreatedAt, FinishedAt: rec.FinishedAt,
		Items: make([]Item, 0, len(items)),
	}
	for _, ir := range items {
		it := Item{
			Key: ir.Key, Kind: Kind(ir.Kind), Name: ir.Name, Title: ir.Title, From: ir.From, To: ir.To, ExpectedTo: ir.ExpectedTo,
			// A row written by an older version may hold raw text.
			Phase: Phase(ir.Phase), Message: safeText(ir.Message), ErrorKey: ir.ErrorKey, Error: safeText(ir.Error), Retryable: ir.Retryable,
			JobID: ir.JobID, QueuedReason: ir.QueuedReason, Verifying: ir.Verifying, CancelWanted: ir.CancelWanted, CancelSent: ir.CancelSent,
		}
		if ir.Nodes != "" {
			_ = json.Unmarshal([]byte(ir.Nodes), &it.Nodes)
			it.Nodes = safeNodes(it.Nodes)
		}
		if ir.Extra != "" {
			_ = json.Unmarshal([]byte(ir.Extra), &it.Extra)
		}
		if it.Phase == PhaseUpdated || it.Verifying {
			it.Progress = 100
		}
		r.Items = append(r.Items, it)
	}
	return r
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
