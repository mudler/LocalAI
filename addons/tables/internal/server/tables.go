// SPDX-License-Identifier: MIT
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

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
)

// Tools is the declared tool set of this addon.
var Tools = []string{ToolResourceExists, ToolDatabaseCreate, ToolDatabaseRename, ToolDatabaseInspect}

// Tables implements contract.Runtime over the database store. It trusts the
// host binding of actor and resource ID: by the time a request arrives the
// owner has authenticated the caller, admitted it for this one resource, and
// bound the operation. Tables only ever uses the resource ID the envelope names.
type Tables struct {
	Store *store.Store
	Now   func() time.Time
}

var _ contract.Runtime = (*Tables)(nil)

type toolArgs struct {
	Tool string `json:"tool"`
	Name string `json:"name"`
}

func parse(env contract.Envelope) (toolArgs, error) {
	var a toolArgs
	// Unknown argument fields are tolerated; the tool name is not optional.
	if err := json.Unmarshal(env.Request.Arguments, &a); err != nil || a.Tool == "" {
		return a, ErrBadRequest
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
	case errors.Is(err, store.ErrInvalidID), errors.Is(err, store.ErrInvalidName), errors.Is(err, store.ErrInvalidOperation):
		return ErrBadRequest
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrExists), errors.Is(err, store.ErrConflict):
		return ErrConflict
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
	return contract.Result{}, ErrBadRequest
}

func (t *Tables) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	a, err := parse(env)
	if err != nil {
		return contract.Preview{}, err
	}
	id := env.Request.ResourceID
	out := contract.Preview{OperationID: env.Request.OperationID, ExpiresAt: t.now().Add(time.Minute)}
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
				return out, ErrConflict
			}
		}
		out.Revision = contract.Revision{Schema: 1, Data: 0}
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]string{"action": "create database", "name": a.Name}})
	case ToolDatabaseRename:
		s, err := t.Store.Inspect(ctx, id)
		if err != nil {
			return out, mapErr(err)
		}
		out.Revision = s.Revision
		out.Summary = payload(map[string]any{"count": 1, "sample": map[string]string{"action": "rename database", "from": s.Name, "to": a.Name}})
	default:
		return out, ErrBadRequest
	}
	if err := validateName(a.Name); err != nil {
		return out, err
	}
	return out, nil
}

func validateName(n string) error {
	// The store owns the rules; a dry call keeps one definition of "valid".
	if _, err := store.CheckName(n); err != nil {
		return ErrBadRequest
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
	switch a.Tool {
	case ToolDatabaseCreate:
		if _, err := t.Store.Create(ctx, id, a.Name, op); err != nil {
			if mapErr(err) == err {
				return contract.Receipt{}, err
			}
			return rejected()
		}
	case ToolDatabaseRename:
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
		return rejected()
	}
	return t.Store.Receipt(ctx, op)
}

func (t *Tables) Receipt(ctx context.Context, op string) (contract.Receipt, error) {
	r, err := t.Store.Receipt(ctx, op)
	return r, mapErr(err)
}

func (t *Tables) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
