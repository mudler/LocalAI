// SPDX-License-Identifier: MIT

// Package tools serves the tools of the Contacts add-on over the contact
// store. The owner of the book is the user the host put in the authority of
// the call; no argument can name another one.
package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/contacts/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// Tool names. They are the entries of capabilities.tools in the manifest.
const (
	ToolContactsSearch = "contacts-search"
	ToolContactGet     = "contact-get"
	ToolContactCreate  = "contact-create"
	ToolContactUpdate  = "contact-update"
	ToolContactDelete  = "contact-delete"

	// The tools below are for the page of the add-on. The manifest marks them
	// uiOnly, so no model is offered them.
	ToolUIList          = "ui-contacts-list"
	ToolUIContactGet    = "ui-contact-get"
	ToolUIContactCreate = "ui-contact-create"
	ToolUIContactUpdate = "ui-contact-update"
	ToolUIContactDelete = "ui-contact-delete"
	ToolContactsExport  = "contacts-export"
)

// Runtime is the add-on runtime over a store.
type Runtime struct {
	Store *store.Store
}

// New returns the runtime of the executable.
func New(st *store.Store) *Runtime { return &Runtime{Store: st} }

var (
	_ contract.Runtime    = (*Runtime)(nil)
	_ contract.ActRuntime = (*Runtime)(nil)
)

var errNoTool = errors.New("this tool is not served by the contacts add-on")

func (*Runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (*Runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*Runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*Runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}

func isAction(tool string) bool {
	switch tool {
	case ToolContactCreate, ToolContactUpdate, ToolContactDelete:
		return true
	}
	return false
}

// Read serves the read tools and the tools of the page.
func (r *Runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if isAction(tool) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	return r.run(ctx, env, tool)
}

// Act serves the tools that change the book for a chat.
func (r *Runtime) Act(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if !isAction(tool) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	return r.run(ctx, env, tool)
}

// maxExport keeps the file inside the payload bound of the host.
const maxExport = 900 << 10

var errArgs = errors.New("the arguments do not match the tool")

func args(raw []byte, v any) error {
	if protocol.DecodeArgs(raw, v) != nil {
		return errArgs
	}
	return nil
}

func (r *Runtime) run(ctx context.Context, env contract.Envelope, tool string) (contract.Result, error) {
	owner := env.Authority.Actor.UserID
	raw := env.Request.Arguments
	var out any
	var err error
	switch tool {
	case ToolContactsSearch:
		out, err = r.search(ctx, owner, raw, 50, 20)
	case ToolUIList:
		out, err = r.search(ctx, owner, raw, store.MaxContacts, store.MaxContacts)
	case ToolContactGet, ToolUIContactGet:
		out, err = r.get(ctx, owner, raw)
	case ToolContactCreate, ToolUIContactCreate:
		out, err = r.create(ctx, owner, raw)
	case ToolContactUpdate, ToolUIContactUpdate:
		out, err = r.update(ctx, owner, raw)
	case ToolContactDelete, ToolUIContactDelete:
		out, err = r.remove(ctx, owner, raw)
	case ToolContactsExport:
		out, err = r.export(ctx, owner, raw)
	default:
		return contract.Result{}, protocol.ErrNotFound
	}
	switch {
	case errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrFull), errors.Is(err, errArgs):
		// The model can correct this: say what was wrong, change nothing.
		return contract.Result{Payload: protocol.Payload(map[string]any{"ok": false, "error": strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+": ")})}, nil
	case err != nil:
		return contract.Result{}, err
	}
	return contract.Result{Payload: protocol.Payload(out)}, nil
}

type tool struct {
	Tool string `json:"tool"`
}

func (r *Runtime) search(ctx context.Context, owner string, raw []byte, maxLimit, def int) (any, error) {
	var a struct {
		tool
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > maxLimit {
		a.Limit = def
	}
	hits, more, err := r.Store.Search(ctx, owner, a.Query, a.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "contacts": hits, "more": more}, nil
}

type idArgs struct {
	tool
	ID string `json:"id"`
}

func (r *Runtime) get(ctx context.Context, owner string, raw []byte) (any, error) {
	var a idArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	c, err := r.Store.Get(ctx, owner, a.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "contact": c}, nil
}

type contactArgs struct {
	tool
	ID       string         `json:"id"`
	Name     *string        `json:"name"`
	Org      *string        `json:"organisation"`
	Phones   *[]store.Entry `json:"phones"`
	Emails   *[]store.Entry `json:"emails"`
	Birthday *string        `json:"birthday"`
	Address  *string        `json:"address"`
	Notes    *string        `json:"notes"`
}

func (a contactArgs) input() store.Input {
	return store.Input{Name: a.Name, Org: a.Org, Phones: a.Phones, Emails: a.Emails, Birthday: a.Birthday, Address: a.Address, Notes: a.Notes}
}

func (r *Runtime) create(ctx context.Context, owner string, raw []byte) (any, error) {
	var a contactArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	c, err := r.Store.Create(ctx, owner, a.input())
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "contact": c}, nil
}

func (r *Runtime) update(ctx context.Context, owner string, raw []byte) (any, error) {
	var a contactArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	c, err := r.Store.Update(ctx, owner, a.ID, a.input())
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "contact": c}, nil
}

func (r *Runtime) remove(ctx context.Context, owner string, raw []byte) (any, error) {
	var a idArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	if err := r.Store.Delete(ctx, owner, a.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "deleted": a.ID}, nil
}

func (r *Runtime) export(ctx context.Context, owner string, raw []byte) (any, error) {
	var a tool
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	vcf, err := r.Store.VCards(ctx, owner)
	if err != nil {
		return nil, err
	}
	if len(vcf) > maxExport {
		return nil, fmt.Errorf("%w: the book is too large to export in one file", store.ErrInvalid)
	}
	return map[string]any{"ok": true, "filename": "contacts.vcf", "vcard": vcf}, nil
}
