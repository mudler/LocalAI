// SPDX-License-Identifier: MIT

// Package tools serves the tools of the Calendar add-on over the calendar
// store. The owner of the calendar is the user the host put in the authority
// of the call; no argument can name another one.
package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tool names. They are the entries of capabilities.tools in the manifest.
const (
	ToolEventsList   = "events-list"
	ToolEventsSearch = "events-search"
	ToolEventGet     = "event-get"
	ToolEventCreate  = "event-create"
	ToolEventUpdate  = "event-update"
	ToolEventDelete  = "event-delete"

	// The tools below are for the page of the add-on. The manifest marks them
	// uiOnly, so no model is offered them.
	ToolSettingsGet   = "settings-get"
	ToolSettingsSet   = "settings-set"
	ToolUIEventCreate = "ui-event-create"
	ToolUIEventUpdate = "ui-event-update"
	ToolUIEventDelete = "ui-event-delete"
	ToolEventsExport  = "events-export"
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

var errNoTool = errors.New("this tool is not served by the calendar add-on")

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
	case ToolEventCreate, ToolEventUpdate, ToolEventDelete:
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

// Act serves the tools that change the calendar for a chat.
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

func (r *Runtime) run(ctx context.Context, env contract.Envelope, tool string) (contract.Result, error) {
	owner := env.Authority.Actor.UserID
	raw := env.Request.Arguments
	var out any
	var err error
	switch tool {
	case ToolEventsList:
		out, err = r.list(ctx, owner, raw)
	case ToolEventsSearch:
		out, err = r.search(ctx, owner, raw)
	case ToolEventGet:
		out, err = r.get(ctx, owner, raw)
	case ToolEventCreate, ToolUIEventCreate:
		out, err = r.create(ctx, owner, raw)
	case ToolEventUpdate, ToolUIEventUpdate:
		out, err = r.update(ctx, owner, raw)
	case ToolEventDelete, ToolUIEventDelete:
		out, err = r.remove(ctx, owner, raw)
	case ToolSettingsGet:
		out, err = r.settingsGet(ctx, owner, raw)
	case ToolSettingsSet:
		out, err = r.settingsSet(ctx, owner, raw)
	case ToolEventsExport:
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

var errArgs = errors.New("the arguments do not match the tool")

func args(raw []byte, v any) error {
	// The host adds the tool name to the arguments; the structs name it.
	if protocol.DecodeArgs(raw, v) != nil {
		return errArgs
	}
	return nil
}

type tool struct {
	Tool string `json:"tool"`
}

func today(now time.Time, zone string) (string, error) {
	loc, err := store.LoadZone(zone)
	if err != nil {
		return "", err
	}
	return now.In(loc).Format("2006-01-02"), nil
}

func (r *Runtime) list(ctx context.Context, owner string, raw []byte) (any, error) {
	var a struct {
		tool
		From string `json:"from"`
		To   string `json:"to"`
		Zone string `json:"zone"`
	}
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	zone := a.Zone
	if zone == "" {
		z, err := r.Store.Zone(ctx, owner)
		if err != nil {
			return nil, err
		}
		zone = z
	}
	td, err := today(r.Store.Now(), zone)
	if err != nil {
		return nil, err
	}
	if a.From == "" {
		a.From = td
	}
	if a.To == "" {
		if a.From == td {
			a.To = addDays(td, 6)
		} else {
			a.To = a.From
		}
	}
	occ, more, err := r.Store.List(ctx, owner, a.From, a.To, zone)
	if err != nil {
		return nil, err
	}
	if occ == nil {
		occ = []store.Occurrence{}
	}
	return map[string]any{"ok": true, "today": td, "zone": zone, "from": a.From, "to": a.To, "events": occ, "more": more}, nil
}

func addDays(date string, n int) string {
	d, err := store.ParseDate(date)
	if err != nil {
		return date
	}
	return d.AddDate(0, 0, n).Format("2006-01-02")
}

func (r *Runtime) search(ctx context.Context, owner string, raw []byte) (any, error) {
	var a struct {
		tool
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > 50 {
		a.Limit = 20
	}
	hits, err := r.Store.Search(ctx, owner, a.Query, a.Limit)
	if err != nil {
		return nil, err
	}
	if hits == nil {
		hits = []store.SearchHit{}
	}
	return map[string]any{"ok": true, "matches": hits}, nil
}

type idArgs struct {
	tool
	ID         string `json:"id"`
	Occurrence string `json:"occurrence"`
}

func (r *Runtime) get(ctx context.Context, owner string, raw []byte) (any, error) {
	var a idArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	e, err := r.Store.Get(ctx, owner, a.ID)
	if err != nil {
		return nil, err
	}
	sk, err := r.Store.Skipped(ctx, owner, a.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "event": e, "skipped": sk}, nil
}

type eventArgs struct {
	tool
	ID        string        `json:"id"`
	Title     *string       `json:"title"`
	Start     *string       `json:"start"`
	End       *string       `json:"end"`
	AllDay    *bool         `json:"allDay"`
	Zone      *string       `json:"zone"`
	Notes     *string       `json:"notes"`
	Place     *string       `json:"place"`
	Attendees *string       `json:"attendees"`
	Repeat    *store.Repeat `json:"repeat"`
}

func (a eventArgs) input() store.Input {
	return store.Input{Title: a.Title, Start: a.Start, End: a.End, AllDay: a.AllDay, Zone: a.Zone, Notes: a.Notes, Place: a.Place, Attendees: a.Attendees, Repeat: a.Repeat}
}

func (r *Runtime) create(ctx context.Context, owner string, raw []byte) (any, error) {
	var a eventArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	e, err := r.Store.Create(ctx, owner, a.input())
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "event": e}, nil
}

func (r *Runtime) update(ctx context.Context, owner string, raw []byte) (any, error) {
	var a eventArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	e, err := r.Store.Update(ctx, owner, a.ID, a.input())
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "event": e}, nil
}

// remove deletes an event, or with an occurrence date takes that one
// occurrence out of a repeating event.
func (r *Runtime) remove(ctx context.Context, owner string, raw []byte) (any, error) {
	var a idArgs
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	if a.Occurrence != "" {
		if err := r.Store.SkipOccurrence(ctx, owner, a.ID, a.Occurrence); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "skipped": a.Occurrence}, nil
	}
	if err := r.Store.Delete(ctx, owner, a.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "deleted": a.ID}, nil
}

func (r *Runtime) settingsGet(ctx context.Context, owner string, raw []byte) (any, error) {
	var a tool
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	z, err := r.Store.Zone(ctx, owner)
	if err != nil {
		return nil, err
	}
	td, _ := today(r.Store.Now(), z)
	return map[string]any{"ok": true, "zone": z, "today": td}, nil
}

func (r *Runtime) settingsSet(ctx context.Context, owner string, raw []byte) (any, error) {
	var a struct {
		tool
		Zone string `json:"zone"`
	}
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	if err := r.Store.SetZone(ctx, owner, a.Zone); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "zone": a.Zone}, nil
}

func (r *Runtime) export(ctx context.Context, owner string, raw []byte) (any, error) {
	var a tool
	if err := args(raw, &a); err != nil {
		return nil, err
	}
	ics, err := r.Store.ICS(ctx, owner)
	if err != nil {
		return nil, err
	}
	if len(ics) > maxExport {
		return nil, fmt.Errorf("%w: the calendar is too large to export in one file", store.ErrInvalid)
	}
	return map[string]any{"ok": true, "filename": "calendar.ics", "ics": ics}, nil
}
