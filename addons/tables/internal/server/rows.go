// SPDX-License-Identifier: MIT
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// rowArgs is the argument object of the row tools. Unknown fields are
// refused: a misspelled "filter" must never turn into a broader operation.
type rowArgs struct {
	Tool       string                       `json:"tool"`
	Table      string                       `json:"table"`
	Columns    []string                     `json:"columns"`
	Filter     *store.Filter                `json:"filter"`
	Sort       []store.SortKey              `json:"sort"`
	Limit      int                          `json:"limit"`
	Cursor     string                       `json:"cursor"`
	Aggregates []store.Aggregation          `json:"aggregates"`
	GroupBy    string                       `json:"groupBy"`
	Rows       []map[string]json.RawMessage `json:"rows"`
	Set        map[string]json.RawMessage   `json:"set"`
	IDs        []int64                      `json:"ids"`
	MaxRows    int                          `json:"maxRows"`
}

func parseRows(env contract.Envelope) (rowArgs, error) {
	var a rowArgs
	d := json.NewDecoder(bytes.NewReader(env.Request.Arguments))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil || d.More() {
		return a, protocol.ErrBadRequest
	}
	return a, nil
}

func (a rowArgs) change(kind, resource string) store.RowChange {
	return store.RowChange{ResourceID: resource, Kind: kind, Table: a.Table, Rows: a.Rows, Set: a.Set, IDs: a.IDs, Filter: a.Filter}
}

// rowAuthority is the addon's own check of a row operation. The host admitted
// the actor for this action already; the addon refuses an envelope that has no
// actor, another scope or another action, so a row tool cannot run under a
// schema action and a schema tool cannot run under a row action.
func rowAuthority(env contract.Envelope, scope, action string) error {
	if env.Request.Action != action || env.Authority.Scope != scope || env.Authority.Actor.UserID == "" {
		return protocol.ErrForbidden
	}
	return nil
}

func (t *Tables) readRows(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	if err := rowAuthority(env, "read", readAction); err != nil {
		return contract.Result{}, err
	}
	a, err := parseRows(env)
	if err != nil {
		return contract.Result{}, err
	}
	id := env.Request.ResourceID
	if a.Tool != ToolRowsExport && a.MaxRows != 0 {
		return contract.Result{}, protocol.ErrBadRequest
	}
	switch a.Tool {
	case ToolRowsExport:
		return t.exportRows(ctx, env, a)
	case ToolRowsQuery:
		if a.Aggregates != nil || a.GroupBy != "" || a.Rows != nil || a.Set != nil || a.IDs != nil {
			return contract.Result{}, protocol.ErrBadRequest
		}
		p, err := t.Store.Query(ctx, id, store.Query{Table: a.Table, Columns: a.Columns, Filter: a.Filter, Sort: a.Sort, Limit: a.Limit, Cursor: a.Cursor})
		if err != nil {
			return contract.Result{}, mapErr(err)
		}
		b, err := json.Marshal(p)
		if err != nil {
			return contract.Result{}, protocol.ErrBadRequest
		}
		return contract.Result{Payload: b}, nil
	case ToolRowsAggregate:
		if a.Columns != nil || a.Sort != nil || a.Cursor != "" || a.Rows != nil || a.Set != nil || a.IDs != nil {
			return contract.Result{}, protocol.ErrBadRequest
		}
		r, err := t.Store.Aggregate(ctx, id, store.AggregateQuery{Table: a.Table, Filter: a.Filter, Aggregates: a.Aggregates, GroupBy: a.GroupBy, Limit: a.Limit})
		if err != nil {
			return contract.Result{}, mapErr(err)
		}
		b, err := json.Marshal(r)
		if err != nil {
			return contract.Result{}, protocol.ErrBadRequest
		}
		return contract.Result{Payload: b}, nil
	}
	return contract.Result{}, protocol.ErrBadRequest
}

// rowMutationShape refuses fields a mutation does not use.
func (a rowArgs) mutationShape() error {
	if a.Columns != nil || a.Sort != nil || a.Limit != 0 || a.Cursor != "" || a.Aggregates != nil || a.GroupBy != "" || a.MaxRows != 0 {
		return protocol.ErrBadRequest
	}
	return nil
}

func (t *Tables) previewRows(ctx context.Context, env contract.Envelope, out contract.Preview) (contract.Preview, error) {
	if err := rowAuthority(env, "preview", writeAction); err != nil {
		return out, err
	}
	a, err := parseRows(env)
	if err != nil || a.mutationShape() != nil {
		return out, protocol.ErrBadRequest
	}
	// A retried operation that already committed has nothing to preview; its
	// receipt is the answer and apply replays it.
	if r, e := t.Store.LookupReceipt(ctx, env.Request.OperationID); e == nil && r.State == contract.ReceiptCommitted {
		out.Revision = r.Revision
		out.Summary = payload(map[string]any{"count": 0, "sample": []string{}, "dataLoss": map[string]int{"rows": 0, "columns": 0, "tables": 0}, "replay": true})
		return out, nil
	}
	p, err := t.Store.PreviewRows(ctx, a.change(rowKinds[a.Tool], env.Request.ResourceID))
	if err != nil {
		return out, mapErr(err)
	}
	out.Revision, out.Summary = p.Revision, p.Summary
	return out, nil
}

func (t *Tables) applyRows(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	if err := rowAuthority(env, "apply", writeAction); err != nil {
		return contract.Receipt{}, err
	}
	a, err := parseRows(env)
	if err != nil || a.mutationShape() != nil {
		return contract.Receipt{}, protocol.ErrBadRequest
	}
	r, err := t.Store.ApplyRows(ctx, a.change(rowKinds[a.Tool], env.Request.ResourceID), rev, op)
	var stale *store.StaleError
	switch {
	case err == nil:
		return r, nil
	case errors.As(err, &stale):
		return reject(op, map[string]any{"error": "stale", "revision": stale.Current}), nil
	case errors.Is(err, store.ErrConflict):
		return contract.Receipt{}, protocol.ErrConflict
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalidID), errors.Is(err, store.ErrInvalidOperation), errors.Is(err, store.ErrUnsafePath):
		return contract.Receipt{}, mapErr(err)
	case errors.Is(err, store.ErrRowConstraint), errors.Is(err, store.ErrInvalidRows), errors.Is(err, store.ErrInvalidQuery),
		errors.Is(err, store.ErrTooManyRows), errors.Is(err, store.ErrNoRows), errors.Is(err, store.ErrRowNotFound),
		errors.Is(err, store.ErrTableNotFound), errors.Is(err, store.ErrColumnNotFound), errors.Is(err, store.ErrCapacity),
		errors.Is(err, store.ErrArithmetic):
		return reject(op, map[string]any{"error": rowRejection(err), "message": err.Error()}), nil
	}
	return contract.Receipt{}, err
}

func rowRejection(err error) string {
	switch {
	case errors.Is(err, store.ErrRowConstraint):
		return "constraint"
	case errors.Is(err, store.ErrTooManyRows):
		return "too-many-rows"
	case errors.Is(err, store.ErrNoRows):
		return "no-rows"
	case errors.Is(err, store.ErrRowNotFound), errors.Is(err, store.ErrTableNotFound), errors.Is(err, store.ErrColumnNotFound):
		return "not-found"
	case errors.Is(err, store.ErrCapacity):
		return "capacity"
	case errors.Is(err, store.ErrArithmetic):
		return "arithmetic"
	}
	return "invalid"
}
