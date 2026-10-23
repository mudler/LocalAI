// SPDX-License-Identifier: MIT
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Limits of staged exports. A CSV lives in the add-on's private directory
// from the moment the read returns until the owner has streamed it (or this
// time has passed), so the staging is bounded in count, per user and in time.
const (
	// ExportTTL is how long a staged file waits for the owner to open it.
	ExportTTL = 10 * time.Minute
	// MaxStagedExports bounds staged files in all, MaxStagedPerUser per person.
	MaxStagedExports = 16
	MaxStagedPerUser = 4
)

// staged is one export waiting to be streamed. It is bound to the operation,
// the person and the database that produced it: the handle alone opens
// nothing.
type staged struct {
	path      string
	operation string
	user      string
	resource  string
	size      int64
	expires   time.Time
}

// exports is the registry of staged files. The handle is a random opaque
// name; it never names a path and survives only in memory, so a restart
// leaves nothing to open (the directory is cleared at startup).
type exports struct {
	mu      sync.Mutex
	byID    map[string]*staged
	tickets int
}

// ClearExports removes everything a previous process staged.
func ClearExports(dir string) error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}

func (t *Tables) exportTTL() time.Duration { return ExportTTL }

// sweepLocked drops expired handles and their files.
func (x *exports) sweepLocked(now time.Time) {
	for id, s := range x.byID {
		if !now.Before(s.expires) {
			_ = os.Remove(s.path)
			delete(x.byID, id)
		}
	}
}

// exportName is the file name of a table's export: ASCII letters, digits and
// a few separators, so every host rule on names holds. The host sanitizes the
// name again before it reaches a header.
func exportName(table string) string {
	var b strings.Builder
	letters := 0
	for _, r := range table {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			letters++
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			if b.Len() > 0 {
				s := b.String()
				if s[len(s)-1] != '_' {
					b.WriteByte('_')
				}
			}
		}
		if b.Len() >= 64 {
			break
		}
	}
	name := strings.Trim(b.String(), "_-")
	if letters == 0 || name == "" {
		name = "table"
	}
	return name + ".csv"
}

func (t *Tables) exportRows(ctx context.Context, env contract.Envelope, a rowArgs) (contract.Result, error) {
	if a.Aggregates != nil || a.GroupBy != "" || a.Rows != nil || a.Set != nil || a.IDs != nil || a.Cursor != "" || a.Limit != 0 || a.MaxRows < 0 {
		return contract.Result{}, protocol.ErrBadRequest
	}
	if t.ExportDir == "" {
		return contract.Result{}, errors.New("exports are not available")
	}
	user, op, id := env.Authority.Actor.UserID, env.Request.OperationID, env.Request.ResourceID
	now := t.now()
	x := &t.exp
	x.mu.Lock()
	if x.byID == nil {
		x.byID = map[string]*staged{}
	}
	x.sweepLocked(now)
	perUser := 0
	for _, s := range x.byID {
		if s.user == user {
			perUser++
		}
	}
	// A reservation counts while the file is written, so concurrent exports
	// cannot pass the bound together.
	if len(x.byID)+x.tickets >= MaxStagedExports || perUser >= MaxStagedPerUser {
		x.mu.Unlock()
		return contract.Result{Payload: payload(map[string]any{"exported": false, "error": "busy",
			"message": "Too many exports are waiting; try again in a moment."})}, nil
	}
	x.tickets++
	x.mu.Unlock()
	release := func() { x.mu.Lock(); x.tickets--; x.mu.Unlock() }
	defer release()

	if err := os.MkdirAll(t.ExportDir, 0o700); err != nil { // #nosec G703 -- the export directory is host configuration and file names are generated, never taken from a request
		return contract.Result{}, err
	}
	f, err := os.CreateTemp(t.ExportDir, "export-*.csv")
	if err != nil {
		return contract.Result{}, err
	}
	path := f.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path) // #nosec G703 -- the export directory is host configuration and file names are generated, never taken from a request
		}
	}()
	stats, err := t.Store.ExportCSV(ctx, id, store.Query{Table: a.Table, Columns: a.Columns, Filter: a.Filter, Sort: a.Sort, Limit: a.MaxRows}, f)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		if errors.Is(err, store.ErrExportTooLarge) {
			return contract.Result{Payload: payload(map[string]any{"exported": false, "error": "too-large", "message": err.Error()})}, nil
		}
		return contract.Result{}, mapErr(err)
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return contract.Result{}, err
	}
	handle := "x" + hex.EncodeToString(raw[:])
	fi, err := os.Stat(path) // #nosec G703 -- the export directory is host configuration and file names are generated, never taken from a request
	if err != nil || fi.Size() != stats.Bytes {
		return contract.Result{}, errors.New("staged export is incomplete")
	}
	x.mu.Lock()
	x.byID[handle] = &staged{path: path, operation: op, user: user, resource: id, size: stats.Bytes, expires: now.Add(t.exportTTL())}
	x.mu.Unlock()
	keep = true
	return contract.Result{
		Payload: payload(map[string]any{"exported": true, "table": a.Table, "rows": stats.Rows, "bytes": stats.Bytes, "columns": stats.Columns, "revision": stats.Revision}),
		Outputs: []contract.OutputDescription{{Handle: handle, Name: exportName(a.Table), MediaType: "text/csv", Size: stats.Bytes}},
	}, nil
}

// openExport opens a staged file for the envelope that is bound to it. Every
// mismatch is the same not-found as an unknown handle.
func (t *Tables) openExport(env contract.Envelope, handle string) (io.ReadCloser, error) {
	x := &t.exp
	x.mu.Lock()
	x.sweepLocked(t.now())
	s := x.byID[handle]
	x.mu.Unlock()
	if s == nil || s.user != env.Authority.Actor.UserID || s.operation != env.Request.OperationID || s.resource != env.Request.ResourceID || env.Authority.Scope != "output" {
		return nil, protocol.ErrNotFound
	}
	f, err := os.Open(s.path) // #nosec G703 -- the export directory is host configuration and file names are generated, never taken from a request
	if err != nil {
		return nil, protocol.ErrNotFound
	}
	return &stagedReader{f: f, t: t, handle: handle, size: s.size}, nil
}

// stagedReader releases the file once every byte was read: the export is
// then in the owner's hands and nothing stays on this side.
type stagedReader struct {
	f      *os.File
	t      *Tables
	handle string
	size   int64
	read   int64
	once   sync.Once
}

func (r *stagedReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	r.read += int64(n)
	return n, err
}

func (r *stagedReader) Close() error {
	err := r.f.Close()
	if r.read >= r.size {
		r.once.Do(func() {
			x := &r.t.exp
			x.mu.Lock()
			if s := x.byID[r.handle]; s != nil {
				_ = os.Remove(s.path)
				delete(x.byID, r.handle)
			}
			x.mu.Unlock()
		})
	}
	return err
}
