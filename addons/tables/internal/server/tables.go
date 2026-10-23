// SPDX-License-Identifier: MIT
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tool names. They are the entries of capabilities.tools in the manifest and
// the exact values of arguments.tool. Names follow the manifest identifier
// rule, so they use hyphens.
const (
	ToolResourceExists  = "resource-exists"
	ToolDatabaseCreate  = "databases-create"
	ToolDatabaseRename  = "databases-rename"
	ToolDatabaseInspect = "databases-inspect"
	// ToolDatabaseDelete is an admin tool. It is not in the manifest's tools:
	// the host lists it in capabilities.adminTools and calls it only from its
	// UI-only delete endpoint, under the "delete" action.
	ToolDatabaseDelete = "databases-delete"
	ToolTableCreate    = "tables-create"
	ToolTableRename    = "tables-rename"
	ToolTableDrop      = "tables-drop"
	ToolColumnAdd      = "columns-add"
	ToolColumnRename   = "columns-rename"
	ToolColumnDrop     = "columns-drop"
	ToolRowsQuery      = "rows-query"
	ToolRowsAggregate  = "rows-aggregate"
	ToolRowsExport     = "rows-export"
	ToolRowsInsert     = "rows-insert"
	ToolRowsUpdate     = "rows-update"
	ToolRowsDelete     = "rows-delete"
)

// rowKinds maps each row mutation tool to its store operation. These tools
// travel under the host's write action: editors and owners may use them, and
// the addon refuses any envelope that carries another action.
var rowKinds = map[string]string{
	ToolRowsInsert: store.KindRowInsert,
	ToolRowsUpdate: store.KindRowUpdate,
	ToolRowsDelete: store.KindRowDelete,
}

const (
	writeAction = "write"
	readAction  = "read"
)

// schemaKinds maps each schema tool to its store operation. These tools are
// schema changes: LocalAI admits them for owners only, and the addon refuses
// any envelope that does not carry the schema action.
var schemaKinds = map[string]string{
	ToolTableCreate:  store.KindTableCreate,
	ToolTableRename:  store.KindTableRename,
	ToolTableDrop:    store.KindTableDrop,
	ToolColumnAdd:    store.KindColumnAdd,
	ToolColumnRename: store.KindColumnRename,
	ToolColumnDrop:   store.KindColumnDrop,
}

// schemaAction is the host action every schema change travels under.
const schemaAction = "schema"

// deleteAction is the host action of the UI-only deletion of a database. No
// model tool, proposal or typed request travels under it.
const deleteAction = "delete"

// Tools is the declared tool set of this addon.
var Tools = []string{ToolResourceExists, ToolDatabaseCreate, ToolDatabaseRename, ToolDatabaseInspect,
	ToolTableCreate, ToolTableRename, ToolTableDrop, ToolColumnAdd, ToolColumnRename, ToolColumnDrop,
	ToolRowsQuery, ToolRowsAggregate, ToolRowsExport, ToolRowsInsert, ToolRowsUpdate, ToolRowsDelete}

// Tables implements contract.Runtime over the database store. It trusts the
// host binding of actor and resource ID: by the time a request arrives the
// owner has authenticated the caller, admitted it for this one resource, and
// bound the operation. Tables only ever uses the resource ID the envelope names.
type Tables struct {
	Store *store.Store
	Now   func() time.Time
	// ExportDir is the private directory CSV exports are staged in until the
	// owner streams them. Empty turns exports off.
	ExportDir string
	exp       exports
}

var _ contract.Runtime = (*Tables)(nil)

type toolArgs struct {
	Tool     string          `json:"tool"`
	Name     string          `json:"name"`
	Table    string          `json:"table"`
	Column   string          `json:"column"`
	NewName  string          `json:"newName"`
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	Unique   bool            `json:"unique"`
	Default  json.RawMessage `json:"default"`
}

func parse(env contract.Envelope) (toolArgs, error) {
	var a toolArgs
	// Unknown argument fields are tolerated; the tool name is not optional.
	if err := json.Unmarshal(env.Request.Arguments, &a); err != nil || a.Tool == "" {
		return a, protocol.ErrBadRequest
	}
	return a, nil
}

func (t *Tables) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrInvalidQuery), errors.Is(err, store.ErrInvalidRows), errors.Is(err, store.ErrRowConstraint),
		errors.Is(err, store.ErrTooManyRows), errors.Is(err, store.ErrNoRows), errors.Is(err, store.ErrRowNotFound),
		errors.Is(err, store.ErrStaleCursor), errors.Is(err, store.ErrArithmetic):
		return protocol.ErrBadRequest
	case errors.Is(err, store.ErrInvalidID), errors.Is(err, store.ErrInvalidName), errors.Is(err, store.ErrInvalidOperation),
		errors.Is(err, store.ErrInvalidSchema), errors.Is(err, store.ErrSchemaLimit), errors.Is(err, store.ErrTableNotFound), errors.Is(err, store.ErrColumnNotFound):
		return protocol.ErrBadRequest
	case errors.Is(err, store.ErrSchemaExists):
		return protocol.ErrConflict
	case errors.Is(err, store.ErrNotFound):
		return protocol.ErrNotFound
	case errors.Is(err, store.ErrExists), errors.Is(err, store.ErrConflict):
		return protocol.ErrConflict
	}
	return err
}

func payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (t *Tables) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	a, err := parse(env)
	if err != nil {
		return contract.Result{}, err
	}
	id := env.Request.ResourceID
	if a.Tool == ToolRowsQuery || a.Tool == ToolRowsAggregate || a.Tool == ToolRowsExport {
		return t.readRows(ctx, env)
	}
	switch a.Tool {
	case ToolResourceExists:
		ok, err := t.Store.Exists(ctx, id)
		if err != nil {
			return contract.Result{}, mapErr(err)
		}
		return contract.Result{Payload: payload(map[string]bool{"exists": ok})}, nil
	case ToolDatabaseInspect:
		s, err := t.Store.Inspect(ctx, id)
		if err != nil {
			return contract.Result{}, mapErr(err)
		}
		return contract.Result{Payload: payload(s)}, nil
	}
	return contract.Result{}, protocol.ErrBadRequest
}

func (t *Tables) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	a, err := parse(env)
	if err != nil {
		return contract.Preview{}, err
	}
	id := env.Request.ResourceID
	out := contract.Preview{OperationID: env.Request.OperationID, ExpiresAt: t.now().Add(time.Minute)}
	if a.Tool == ToolDatabaseDelete {
		return t.previewDelete(ctx, env, out)
	}
	if _, ok := rowKinds[a.Tool]; ok {
		return t.previewRows(ctx, env, out)
	}
	switch a.Tool {
	case ToolDatabaseCreate:
		ok, err := t.Store.Exists(ctx, id)
		if err != nil {
			return out, mapErr(err)
		}
		if ok {
			// A retried creation finds its own database; anything else is a clash.
			r, err := t.Store.Receipt(ctx, env.Request.OperationID)
			if err != nil || r.State != contract.ReceiptCommitted {
				return out, protocol.ErrConflict
			}
		}
		out.Revision = contract.Revision{Schema: 1, Data: 0}
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]string{"action": "create database", "name": a.Name}})
	case ToolDatabaseRename:
		if err := schemaAuthority(env, "preview"); err != nil {
			return out, err
		}
		s, err := t.Store.Inspect(ctx, id)
		if err != nil {
			return out, mapErr(err)
		}
		out.Revision = s.Revision
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]string{"action": "rename database", "from": s.Name, "to": a.Name}})
	default:
		if kind, ok := schemaKinds[a.Tool]; ok {
			if err := schemaAuthority(env, "preview"); err != nil {
				return out, err
			}
			// A retried operation that already committed has nothing to
			// preview; its receipt is the answer and apply replays it.
			if r, e := t.Store.LookupReceipt(ctx, env.Request.OperationID); e == nil && r.State == contract.ReceiptCommitted {
				out.Revision = r.Revision
				out.Summary = payload(map[string]any{"count": 0, "sample": []string{}, "dataLoss": map[string]int{"rows": 0, "columns": 0, "tables": 0}, "replay": true})
				return out, nil
			}
			p, err := t.Store.PreviewSchema(ctx, a.change(kind, id))
			if err != nil {
				return out, mapErr(err)
			}
			out.Revision, out.Summary = p.Revision, p.Summary
			return out, nil
		}
		return out, protocol.ErrBadRequest
	}
	if err := validateName(a.Name); err != nil {
		return out, err
	}
	return out, nil
}

// deleteAuthority refuses an envelope that is not the host's delete action
// with the matching scope, so a caller that reaches the runtime transport
// with another action cannot remove a database.
func deleteAuthority(env contract.Envelope, scope string) error {
	if env.Request.Action != deleteAction || env.Authority.Scope != scope || env.Authority.Actor.UserID == "" {
		return protocol.ErrForbidden
	}
	return nil
}

func (t *Tables) previewDelete(ctx context.Context, env contract.Envelope, out contract.Preview) (contract.Preview, error) {
	if err := deleteAuthority(env, "preview"); err != nil {
		return out, err
	}
	s, err := t.Store.Inspect(ctx, env.Request.ResourceID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Already gone: apply replays the receipt or records the outcome.
		out.Revision = contract.Revision{Schema: 1}
		out.Summary = payload(map[string]any{"count": 0, "sample": map[string]string{"action": "delete database", "state": "already deleted"}})
		return out, nil
	case err != nil:
		return out, mapErr(err)
	}
	out.Revision = s.Revision
	out.Summary = payload(map[string]any{"count": 1, "sample": map[string]any{"action": "delete database", "name": s.Name, "tables": len(s.Tables)}})
	return out, nil
}

func (t *Tables) applyDelete(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	if err := deleteAuthority(env, "apply"); err != nil {
		return contract.Receipt{}, err
	}
	id, op := env.Request.ResourceID, env.Request.OperationID
	s, err := t.Store.Inspect(ctx, id)
	switch {
	case err == nil:
		// A database that changed since the preview is not the one the owner
		// confirmed: nothing is removed.
		if r, e := t.Store.Receipt(ctx, op); e != nil || r.State != contract.ReceiptCommitted {
			if s.Revision != rev {
				return contract.Receipt{OperationID: op, State: contract.ReceiptRejected}, nil
			}
		}
	case errors.Is(err, store.ErrNotFound):
	default:
		return contract.Receipt{}, mapErr(err)
	}
	r, err := t.Store.Delete(ctx, id, op)
	if err != nil {
		if mapErr(err) == err {
			return contract.Receipt{}, err
		}
		return contract.Receipt{OperationID: op, State: contract.ReceiptRejected}, nil
	}
	return r, nil
}

func (a toolArgs) change(kind, resource string) store.SchemaChange {
	return store.SchemaChange{ResourceID: resource, Kind: kind, Table: a.Table, Column: a.Column, NewName: a.NewName,
		Type: a.Type, Required: a.Required, Unique: a.Unique, Default: a.Default}
}

// schemaAuthority is the addon's own check of a schema change. The host has
// already admitted the actor as owner; the addon still refuses an envelope
// that is not a schema action, has no actor, or carries another scope, so a
// caller that reaches the runtime transport without the host's mapping
// cannot change a schema through a row action.
func schemaAuthority(env contract.Envelope, scope string) error {
	if env.Request.Action != schemaAction || env.Authority.Scope != scope || env.Authority.Actor.UserID == "" {
		return protocol.ErrForbidden
	}
	return nil
}

func validateName(n string) error {
	// The store owns the rules; a dry call keeps one definition of "valid".
	if _, err := store.CheckName(n); err != nil {
		return protocol.ErrBadRequest
	}
	return nil
}

func (t *Tables) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	a, err := parse(env)
	if err != nil {
		return contract.Receipt{}, err
	}
	id, op := env.Request.ResourceID, env.Request.OperationID
	rejected := func() (contract.Receipt, error) {
		return contract.Receipt{OperationID: op, State: contract.ReceiptRejected}, nil
	}
	if a.Tool == ToolDatabaseDelete {
		return t.applyDelete(ctx, env, rev)
	}
	if _, ok := rowKinds[a.Tool]; ok {
		return t.applyRows(ctx, env, rev)
	}
	switch a.Tool {
	case ToolDatabaseCreate:
		if _, err := t.Store.Create(ctx, id, a.Name, op); err != nil {
			if mapErr(err) == err {
				return contract.Receipt{}, err
			}
			return rejected()
		}
	case ToolDatabaseRename:
		if err := schemaAuthority(env, "apply"); err != nil {
			return contract.Receipt{}, err
		}
		s, err := t.Store.Inspect(ctx, id)
		if err != nil {
			if mapErr(err) == err {
				return contract.Receipt{}, err
			}
			return rejected()
		}
		// A schema revision other than the previewed one means the database
		// changed shape since the preview; the host must preview again.
		if r, e := t.Store.Receipt(ctx, op); e != nil || r.State != contract.ReceiptCommitted {
			if s.Revision.Schema != rev.Schema {
				return rejected()
			}
		}
		if _, err := t.Store.Rename(ctx, id, a.Name, op); err != nil {
			if mapErr(err) == err {
				return contract.Receipt{}, err
			}
			return rejected()
		}
	default:
		kind, ok := schemaKinds[a.Tool]
		if !ok {
			return rejected()
		}
		if err := schemaAuthority(env, "apply"); err != nil {
			return contract.Receipt{}, err
		}
		r, err := t.Store.ApplySchema(ctx, a.change(kind, id), rev, op)
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
		case errors.Is(err, store.ErrInvalidSchema), errors.Is(err, store.ErrSchemaLimit), errors.Is(err, store.ErrSchemaExists),
			errors.Is(err, store.ErrTableNotFound), errors.Is(err, store.ErrColumnNotFound), errors.Is(err, store.ErrCapacity):
			return reject(op, map[string]any{"error": rejectionCode(err), "message": err.Error()}), nil
		}
		return contract.Receipt{}, err
	}
	return t.Store.Receipt(ctx, op)
}

func reject(op string, body map[string]any) contract.Receipt {
	return contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Result: contract.Result{Payload: payload(body)}}
}

func rejectionCode(err error) string {
	switch {
	case errors.Is(err, store.ErrSchemaLimit):
		return "limit"
	case errors.Is(err, store.ErrSchemaExists):
		return "exists"
	case errors.Is(err, store.ErrTableNotFound), errors.Is(err, store.ErrColumnNotFound):
		return "not-found"
	case errors.Is(err, store.ErrCapacity):
		return "capacity"
	}
	return "invalid"
}

func (t *Tables) Receipt(ctx context.Context, op string) (contract.Receipt, error) {
	r, err := t.Store.LookupReceipt(ctx, op)
	return r, mapErr(err)
}

// OpenOutput streams a staged export to the owner. The handle opens only for
// the operation, person and database that produced it.
func (t *Tables) OpenOutput(_ context.Context, env contract.Envelope, handle string) (io.ReadCloser, error) {
	return t.openExport(env, handle)
}
