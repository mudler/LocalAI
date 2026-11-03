// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Direction of a transfer, from the host.
const (
	// TransferIn moves a file from an add-on to the host: the add-on puts.
	TransferIn = "in"
	// TransferOut moves a file from the host to an add-on: the add-on gets.
	TransferOut = "out"
)

// Results of a transfer, as the audit shows them.
const (
	TransferDone       = "done"
	TransferTooLarge   = "too_large"
	TransferExpired    = "expired"
	TransferRefused    = "refused"
	TransferIncomplete = "incomplete"
)

// Default times of a handle.
const (
	// DefaultTransferIdle ends a handle that has seen no call for this long.
	DefaultTransferIdle = 2 * time.Minute
	// DefaultTransferLife ends a handle this long after it was made, whatever
	// it does. A connector call is shorter than this.
	DefaultTransferLife = time.Hour
)

// ErrTransferTooLarge means a put went past the bound of its handle.
var ErrTransferTooLarge = errors.New("the file is larger than the limit of the handle")

// TransferGrant is what the host that issued a handle records about it. The
// owner of the add-ons registers it when a connector call that carries a
// ConnectorTransfer arrives.
type TransferGrant struct {
	Handle    string
	Direction string
	// AddonID is the only add-on that may use the handle.
	AddonID string
	// BindingID is for the audit.
	BindingID string
	// MaxBytes bounds an inbound file.
	MaxBytes int64
	// SHA and Size name the outbound content, a blob of the workspace store.
	SHA  string
	Size int64
}

// TransferOutcome is what a handle ended with. It never holds a name or any
// content.
type TransferOutcome struct {
	Direction string
	BindingID string
	Bytes     int64
	Result    string
	Duration  time.Duration
	// SHA is the hash of a finished inbound file.
	SHA string
}

// FileScan is the hook after the last byte of an inbound file. An error
// rejects the file. It is a no-op until an owner sets one.
type FileScan func(ctx context.Context, sha string, size int64) error

// Transfers is the table of live handles of one process, and the bytes behind
// them. Inbound bytes stream into the blob store of the workspace, which in a
// cluster is the shared object store, so no node needs a shared disk. Outbound
// bytes stream out of the same store. A file is never held in memory: a put
// or a get moves one chunk.
type Transfers struct {
	// Blobs is the blob store of the workspace. UseBlobs sets it when the
	// files start, which can be after the table is made.
	Blobs blob.Store
	Scan  FileScan
	// Audit gets the outcome of every handle when it ends. Nil: none.
	Audit func(TransferOutcome)
	// Idle and Life default to DefaultTransferIdle and DefaultTransferLife.
	Idle, Life time.Duration

	mu sync.Mutex
	m  map[string]*xfer
}

type xfer struct {
	t     *Transfers
	g     TransferGrant
	start time.Time

	mu sync.Mutex // one call at a time per handle
	// cmu guards the first write of pw and cancel, so an expiry can read them
	// without waiting for a call that holds mu.
	cmu      sync.Mutex
	received int64
	pw       *io.PipeWriter
	done     chan struct{} // closed when the blob Put of an inbound handle returned
	sha      string
	size     int64
	putErr   error
	final    bool
	rd       io.ReadSeekCloser
	cancel   context.CancelFunc
	idle     *time.Timer
	life     *time.Timer
	ended    bool
	result   string
}

// NewTransfers builds a table over the blob store of the workspace.
func NewTransfers(blobs blob.Store) *Transfers {
	return &Transfers{Blobs: blobs, m: map[string]*xfer{}}
}

// UseBlobs sets the blob store. Handles that begin before it is set are
// refused as unavailable.
func (t *Transfers) UseBlobs(b blob.Store) {
	t.mu.Lock()
	t.Blobs = b
	t.mu.Unlock()
}

func (t *Transfers) store() blob.Store {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Blobs
}

func (t *Transfers) idle() time.Duration {
	if t.Idle > 0 {
		return t.Idle
	}
	return DefaultTransferIdle
}

func (t *Transfers) life() time.Duration {
	if t.Life > 0 {
		return t.Life
	}
	return DefaultTransferLife
}

// Begin registers a handle. The returned function ends it: it drops a file
// that is not finished and reports the outcome. For a finished inbound file the
// outcome holds the hash and the size. Calling it again returns the same
// outcome. A handle that is already registered is refused, so a handle is
// never used for two calls.
func (t *Transfers) Begin(g TransferGrant) (end func() TransferOutcome, err error) {
	if t == nil || t.store() == nil {
		return nil, errors.New("file transfer is not available here")
	}
	if g.Handle == "" || len(g.Handle) > contract.MaxTransferHandle || g.AddonID == "" {
		return nil, fmt.Errorf("%w: bad handle", ErrBadRequest)
	}
	switch g.Direction {
	case TransferIn:
		if g.MaxBytes <= 0 {
			return nil, fmt.Errorf("%w: no bound", ErrBadRequest)
		}
	case TransferOut:
		if g.SHA == "" || g.Size <= 0 {
			return nil, fmt.Errorf("%w: no content", ErrBadRequest)
		}
	default:
		return nil, fmt.Errorf("%w: bad direction", ErrBadRequest)
	}
	x := &xfer{t: t, g: g, start: time.Now()}
	t.mu.Lock()
	if t.m == nil {
		t.m = map[string]*xfer{}
	}
	if _, dup := t.m[g.Handle]; dup {
		t.mu.Unlock()
		return nil, fmt.Errorf("%w: handle in use", ErrConflict)
	}
	t.m[g.Handle] = x
	t.mu.Unlock()
	x.idle = time.AfterFunc(t.idle(), func() { x.finish(TransferExpired) })
	x.life = time.AfterFunc(t.life(), func() { x.finish(TransferExpired) })
	return func() TransferOutcome { return x.finish("") }, nil
}

// Live reports how many handles exist; a test and the sweep use it.
func (t *Transfers) Live() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}

func (t *Transfers) lookup(addonID, handle string) (*xfer, error) {
	t.mu.Lock()
	x := t.m[handle]
	t.mu.Unlock()
	// The same answer for a handle that does not exist and one of another
	// add-on: a handle cannot be probed.
	if x == nil || subtle.ConstantTimeCompare([]byte(x.g.AddonID), []byte(addonID)) != 1 {
		return nil, ErrNotFound
	}
	return x, nil
}

// finish ends the handle once. why is the result when the handle did not end
// by its own end function.
func (x *xfer) finish(why string) TransferOutcome {
	x.idle.Stop()
	x.life.Stop()
	// A put or a get in progress holds mu; an expiry must not wait for a
	// stuck one, so it cancels first.
	x.cmu.Lock()
	cancel, pw := x.cancel, x.pw
	x.cmu.Unlock()
	if cancel != nil {
		cancel()
	}
	if pw != nil {
		_ = pw.CloseWithError(context.Canceled)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ended {
		return x.outcome()
	}
	x.ended = true
	x.t.mu.Lock()
	delete(x.t.m, x.g.Handle)
	x.t.mu.Unlock()
	if x.rd != nil {
		_ = x.rd.Close()
	}
	if x.done != nil {
		<-x.done // the blob Put drops its temp file when its reader fails
	}
	switch {
	case x.result != "":
	case why != "":
		x.result = why
	case x.g.Direction == TransferIn && !x.final:
		x.result = TransferIncomplete
	default:
		x.result = TransferDone
	}
	o := x.outcome()
	if x.t.Audit != nil {
		x.t.Audit(o)
	}
	return o
}

func (x *xfer) outcome() TransferOutcome {
	o := TransferOutcome{Direction: x.g.Direction, BindingID: x.g.BindingID, Bytes: x.received, Result: x.result, Duration: time.Since(x.start)}
	if x.g.Direction == TransferOut {
		o.Bytes = x.size
	}
	if x.final && x.result != TransferTooLarge && x.result != TransferRefused {
		o.SHA = x.sha
	}
	return o
}

func (x *xfer) touch() { x.idle.Reset(x.t.idle()) }

// put writes the next chunk of an inbound file.
func (x *xfer) put(ctx context.Context, p contract.ConnectorFilePut) (int64, error) {
	if x.g.Direction != TransferIn {
		return 0, ErrNotFound
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ended || x.final {
		return 0, ErrConflict
	}
	x.touch()
	if len(p.Data) > contract.TransferChunkBytes {
		return 0, fmt.Errorf("%w: chunk too large", ErrBadRequest)
	}
	if p.Offset != x.received {
		// The add-on resumes where the host is.
		return x.received, nil
	}
	if x.received+int64(len(p.Data)) > x.g.MaxBytes {
		x.result = TransferTooLarge
		x.abortLocked()
		return x.received, ErrTransferTooLarge
	}
	if x.pw == nil {
		pr, pw := io.Pipe()
		bctx, cancel := context.WithCancel(context.Background())
		x.cmu.Lock()
		x.pw, x.cancel, x.done = pw, cancel, make(chan struct{})
		x.cmu.Unlock()
		go func() {
			defer close(x.done)
			sha, size, err := x.t.store().Put(bctx, pr, x.g.MaxBytes)
			x.sha, x.size, x.putErr = sha, size, err
			_ = pr.CloseWithError(errors.New("closed"))
		}()
	}
	if len(p.Data) > 0 {
		if _, err := io.Copy(x.pw, bytes.NewReader(p.Data)); err != nil {
			return x.received, x.failedLocked(err)
		}
		x.received += int64(len(p.Data))
	}
	if !p.Final {
		return x.received, nil
	}
	if x.received == 0 {
		x.result = TransferRefused
		x.abortLocked()
		return 0, fmt.Errorf("%w: an empty file", ErrBadRequest)
	}
	_ = x.pw.Close()
	<-x.done
	switch {
	case errors.Is(x.putErr, workspace.ErrTooLarge):
		x.result = TransferTooLarge
		return x.received, ErrTransferTooLarge
	case x.putErr != nil:
		return x.received, x.putErr
	case x.size != x.received:
		return x.received, errors.New("the stored size differs from the bytes received")
	}
	if x.t.Scan != nil {
		if err := x.t.Scan(ctx, x.sha, x.size); err != nil {
			x.result = TransferRefused
			return x.received, fmt.Errorf("%w: the file was refused", ErrBadRequest)
		}
	}
	x.final = true
	return x.received, nil
}

// failedLocked turns a failed write into the error of the blob store, which
// ended the pipe, so that a bound reached inside the store is too large.
func (x *xfer) failedLocked(err error) error {
	<-x.done
	if errors.Is(x.putErr, workspace.ErrTooLarge) {
		x.result = TransferTooLarge
		return ErrTransferTooLarge
	}
	if x.putErr != nil {
		return x.putErr
	}
	return err
}

func (x *xfer) abortLocked() {
	if x.pw != nil {
		_ = x.pw.CloseWithError(context.Canceled)
		if x.cancel != nil {
			x.cancel()
		}
		<-x.done
	}
}

// get reads the next chunk of an outbound file.
func (x *xfer) get(ctx context.Context, g contract.ConnectorFileGet) (contract.ConnectorFileGetResult, error) {
	if x.g.Direction != TransferOut {
		return contract.ConnectorFileGetResult{}, ErrNotFound
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ended {
		return contract.ConnectorFileGetResult{}, ErrConflict
	}
	x.touch()
	if g.Offset < 0 || g.Offset > x.g.Size || g.Length < 1 || g.Length > contract.TransferChunkBytes {
		return contract.ConnectorFileGetResult{}, fmt.Errorf("%w: offset or length", ErrBadRequest)
	}
	if x.rd == nil {
		r, err := x.t.store().Open(ctx, x.g.SHA)
		if err != nil {
			return contract.ConnectorFileGetResult{}, err
		}
		n, err := r.Seek(0, io.SeekEnd)
		if err != nil || n != x.g.Size {
			_ = r.Close()
			return contract.ConnectorFileGetResult{}, errors.New("the file changed since the handle was made")
		}
		x.rd, x.size = r, n
	}
	if _, err := x.rd.Seek(g.Offset, io.SeekStart); err != nil {
		return contract.ConnectorFileGetResult{}, err
	}
	buf := make([]byte, min(g.Length, x.g.Size-g.Offset))
	n, err := io.ReadFull(x.rd, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return contract.ConnectorFileGetResult{}, err
	}
	buf = buf[:n]
	return contract.ConnectorFileGetResult{Data: buf, EOF: g.Offset+int64(n) >= x.g.Size}, nil
}

// RegisterConnectorTransfer registers the two operations with which a
// connector add-on moves file bytes by handle. Both are inbound class: the
// credential of the live incarnation authorizes them, and the handle binds
// them to one connector call. The arguments name no user.
func RegisterConnectorTransfer(b *Broker, t *Transfers) error {
	if t == nil {
		return errors.New("connector file transfer needs a table")
	}
	decode := func(raw json.RawMessage, v any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			return ErrBadRequest
		}
		return nil
	}
	if err := b.Register(Spec{Op: contract.ConnectorFilePutOp, Scope: ScopeInbound, Timeout: 2 * time.Minute, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		var p contract.ConnectorFilePut
		if err := decode(c.Arguments, &p); err != nil {
			return nil, err
		}
		x, err := t.lookup(c.AddonID, p.Handle)
		if err != nil {
			return nil, err
		}
		got, err := x.put(ctx, p)
		if err != nil {
			return nil, err
		}
		return json.Marshal(contract.ConnectorFilePutResult{Received: got})
	}}); err != nil {
		return err
	}
	return b.Register(Spec{Op: contract.ConnectorFileGetOp, Scope: ScopeInbound, Timeout: 2 * time.Minute, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		var g contract.ConnectorFileGet
		if err := decode(c.Arguments, &g); err != nil {
			return nil, err
		}
		x, err := t.lookup(c.AddonID, g.Handle)
		if err != nil {
			return nil, err
		}
		r, err := x.get(ctx, g)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r)
	}})
}
