// SPDX-License-Identifier: MIT
package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// sizedReader enforces the declared size while the bytes stream through: it
// never lets more than one byte past the declaration be read, fails at once on
// an excess and fails at the end when the stream is short. Its error is not
// io.EOF, so the staged store never publishes a short or long file.
type sizedReader struct {
	ctx  context.Context
	r    io.Reader
	want int64
	n    int64
}

func (s *sizedReader) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	// One byte beyond the declaration is enough to know the stream is long.
	if room := s.want - s.n + 1; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := s.r.Read(p)
	s.n += int64(n)
	if s.n > s.want {
		return 0, errSizeMismatch
	}
	if err == io.EOF && s.n != s.want {
		return n, errSizeMismatch
	}
	return n, err
}

// request builds the envelope the runtime sees for the output. The authority
// is the requesting principal's, with the output scope and a short expiry.
func (a *Artifacts) envelope(actor contract.Actor, request contract.Request) contract.Envelope {
	if len(request.Arguments) == 0 {
		request.Arguments = json.RawMessage(`{}`)
	}
	return contract.Envelope{Request: request, Authority: contract.Authority{Actor: actor, Scope: "output", ExpiresAt: a.now().Add(a.limits.TransferTimeout)}}
}

// Ingest copies one output of a request into a private artifact and returns
// the completed artifact. The source resource is the request's resource, as the
// owner policy recorded it. A repeated call for the same principal, operation
// and output returns the same artifact.
func (a *Artifacts) Ingest(ctx context.Context, actor contract.Actor, request contract.Request, output contract.OutputDescription) (Artifact, error) {
	key, err := a.opts.Policy.Resolve(ctx, actor, request)
	if err != nil {
		return Artifact{}, err
	}
	return a.IngestSources(ctx, actor, request, []policy.ResourceKey{key}, output)
}

// IngestSources is Ingest for a result produced from several resources. The
// principal must be able to read every one of them at completion and at every
// download.
func (a *Artifacts) IngestSources(ctx context.Context, actor contract.Actor, request contract.Request, sources []policy.ResourceKey, output contract.OutputDescription) (Artifact, error) {
	// Nothing is reserved for a principal that cannot read every source.
	if err := a.authorizeAll(ctx, actor, sources); err != nil {
		return Artifact{}, err
	}
	rec, existing, j, err := a.reserve(actor, request, sources, output)
	if err != nil {
		return Artifact{}, err
	}
	if existing {
		return rec.view(), nil
	}
	stop := context.AfterFunc(ctx, j.cancel)
	defer stop()
	if err = a.execute(j, actor, request, output.Handle, rec, keysOf(rec.Sources)); err != nil {
		return Artifact{}, err
	}
	cur, ok := a.meta.get(rec.ID)
	if !ok {
		return Artifact{}, ErrNotFound
	}
	return cur.view(), nil
}

// Start is the background form of Ingest: it reserves the artifact and its
// quota at once, returns the transferring artifact and finishes the copy on a
// bounded worker. The owner asks for the outcome with Status.
func (a *Artifacts) Start(ctx context.Context, actor contract.Actor, request contract.Request, output contract.OutputDescription) (Artifact, error) {
	key, err := a.opts.Policy.Resolve(ctx, actor, request)
	if err != nil {
		return Artifact{}, err
	}
	return a.StartSources(ctx, actor, request, []policy.ResourceKey{key}, output)
}

// StartSources is Start for a result produced from several resources.
func (a *Artifacts) StartSources(ctx context.Context, actor contract.Actor, request contract.Request, sources []policy.ResourceKey, output contract.OutputDescription) (Artifact, error) {
	if err := a.authorizeAll(ctx, actor, sources); err != nil {
		return Artifact{}, err
	}
	rec, existing, j, err := a.reserve(actor, request, sources, output)
	if err != nil {
		return Artifact{}, err
	}
	if existing {
		return rec.view(), nil
	}
	view := rec.view()
	go func() { _ = a.execute(j, actor, request, output.Handle, rec, keysOf(rec.Sources)) }()
	return view, nil
}

// reserve validates the output, enforces every quota atomically and records the
// artifact as transferring before a byte moves. When the same principal already
// asked for the same output it returns that artifact instead.
func (a *Artifacts) reserve(actor contract.Actor, request contract.Request, sources []policy.ResourceKey, output contract.OutputDescription) (record, bool, *job, error) {
	if len(sources) == 0 {
		return record{}, false, nil, gateway.ErrForbidden
	}
	for _, s := range sources {
		if s.AddonID == "" || s.AddonID != request.AddonID || s.ResourceID == "" {
			return record{}, false, nil, gateway.ErrForbidden
		}
	}
	// The envelope contract bounds identifiers, the actor and the arguments;
	// the result contract bounds the output handle, name and media type. An
	// addon handle that is a URL or a path is not a valid handle.
	env := a.envelope(actor, request)
	if err := env.Validate(a.now(), contract.DefaultLimits()); err != nil {
		return record{}, false, nil, gateway.ErrForbidden
	}
	if err := (contract.Result{Outputs: []contract.OutputDescription{output}}).Validate(contract.DefaultLimits()); err != nil {
		return record{}, false, nil, gateway.ErrForbidden
	}
	if output.Size > a.limits.MaxBytes {
		return record{}, false, nil, ErrTooLarge
	}
	id, err := newID()
	if err != nil {
		return record{}, false, nil, err
	}
	bind := binding(actor.UserID, request.OperationID, output.Handle)
	now := a.now()
	rec := record{ID: id, Principal: actor.UserID, Mode: actor.Mode, Sources: sourcesOf(sources), AddonID: request.AddonID,
		Generation: request.Generation, OperationID: request.OperationID, Binding: bind, Name: SafeName(output.Name),
		MediaType: SafeMediaType(output.MediaType), Size: output.Size, State: gateway.ArtifactTransferring,
		CreatedAt: now, ExpiresAt: now.Add(a.limits.Expiry)}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return record{}, false, nil, ErrClosed
	}
	var replace []string
	var userCount, totalCount int
	var userBytes, totalBytes int64
	for _, r := range a.meta.all() {
		if !now.Before(r.ExpiresAt) {
			// Expired and not yet swept: it is gone as far as quotas go.
			if r.Binding == bind {
				replace = append(replace, r.ID)
			}
			continue
		}
		if r.Binding == bind {
			if r.State == gateway.ArtifactTransferring || r.State == gateway.ArtifactComplete {
				return r, true, nil, nil
			}
			replace = append(replace, r.ID)
			continue
		}
		totalCount++
		if r.Principal == actor.UserID && r.Mode == actor.Mode {
			userCount++
		}
		if r.holdsBytes() {
			totalBytes += r.Size
			if r.Principal == actor.UserID && r.Mode == actor.Mode {
				userBytes += r.Size
			}
		}
	}
	if userCount >= a.limits.UserCount || totalCount >= a.limits.TotalCount ||
		userBytes+output.Size > a.limits.UserBytes || totalBytes+output.Size > a.limits.TotalBytes {
		return record{}, false, nil, ErrQuota
	}
	userJobs := 0
	for _, j := range a.jobs {
		if j.principal == actor.UserID {
			userJobs++
		}
	}
	if len(a.jobs) >= a.limits.Transfers || userJobs >= a.limits.UserTransfers {
		return record{}, false, nil, ErrBusy
	}
	if err = a.meta.mutate(func(d *document) error {
		for _, old := range replace {
			delete(d.Artifacts, old)
		}
		d.Artifacts[id] = rec.copy()
		return nil
	}); err != nil {
		return record{}, false, nil, err
	}
	for _, old := range replace {
		_ = a.objects.Delete(context.Background(), old)
	}
	ctx, cancel := context.WithTimeout(a.ctx, a.limits.TransferTimeout)
	j := &job{principal: actor.UserID, cancel: cancel, done: make(chan struct{}), ctx: ctx}
	a.jobs[id] = j
	a.wg.Add(1)
	return rec, false, j, nil
}

// execute runs one reserved transfer to its final state. It records the state
// before it returns, so a caller or Close that waits sees nothing pending.
func (a *Artifacts) execute(j *job, actor contract.Actor, request contract.Request, handle string, rec record, keys []policy.ResourceKey) (err error) {
	defer func() {
		j.cancel()
		if err != nil {
			a.mu.Lock()
			cancelled := j.cancelled
			a.mu.Unlock()
			state, reason := failure(err, cancelled, a.ctx.Err() != nil)
			// The final write must not depend on the caller's context. It is
			// made before the worker leaves the live set, so a sweep never
			// mistakes a finishing transfer for an abandoned one.
			_ = a.settle(context.Background(), rec.ID, state, reason)
		}
		a.mu.Lock()
		delete(a.jobs, rec.ID)
		a.mu.Unlock()
		close(j.done)
		a.wg.Done()
	}()
	err = a.admit(j.ctx, actor, keys, func(ctx context.Context) error {
		return a.transfer(ctx, actor, request, handle, rec, keys)
	})
	if errors.Is(err, context.Canceled) && j.ctx.Err() == nil {
		// Only the admission was cancelled: a source permission was revoked.
		err = fmt.Errorf("source access revoked: %w", gateway.ErrForbidden)
	}
	return err
}

// failure maps a transfer error to the recorded state and a fixed reason. The
// reason never carries addon text.
func failure(err error, cancelled, closing bool) (gateway.ArtifactState, string) {
	switch {
	case cancelled:
		return gateway.ArtifactCancelled, "cancelled"
	case errors.Is(err, errSizeMismatch):
		return gateway.ArtifactFailed, "output size differs from its declaration"
	case errors.Is(err, errExpired):
		return gateway.ArtifactFailed, "expired before completion"
	case errors.Is(err, context.DeadlineExceeded):
		return gateway.ArtifactFailed, "transfer timed out"
	case errors.Is(err, context.Canceled) && closing:
		return gateway.ArtifactFailed, "store closed"
	case errors.Is(err, context.Canceled):
		return gateway.ArtifactCancelled, "cancelled"
	case errors.Is(err, gateway.ErrForbidden):
		return gateway.ArtifactFailed, "access denied"
	case errors.Is(err, contract.ErrRefused):
		return gateway.ArtifactFailed, "output refused as invalid"
	}
	return gateway.ArtifactFailed, "output unavailable"
}

// transfer runs under the output admission of every source resource. A
// revocation cancels ctx, which stops the read and removes the partial bytes.
func (a *Artifacts) transfer(ctx context.Context, actor contract.Actor, request contract.Request, handle string, rec record, keys []policy.ResourceKey) error {
	if err := a.addonReady(ctx, rec.AddonID, rec.Generation); err != nil {
		return err
	}
	if !a.now().Before(rec.ExpiresAt) {
		return errExpired
	}
	instance, err := a.opts.Runtime.Runtime(ctx, rec.AddonID)
	if err != nil {
		if errors.Is(err, gateway.ErrForbidden) {
			return gateway.ErrForbidden
		}
		return fmt.Errorf("addon runtime: %w: %w", gateway.ErrUnavailable, err)
	}
	if instance.Client == nil || instance.Generation != rec.Generation {
		return gateway.ErrForbidden
	}
	// The request that produced the output must not name a tool the add-on
	// did not declare, whatever the stored record says.
	src, _ := a.opts.Runtime.(gateway.ToolSource)
	if err := gateway.AuthorizeTool(ctx, src, rec.AddonID, rec.Generation, request.Arguments); err != nil {
		return err
	}
	reader, err := instance.Client.OpenOutput(ctx, a.envelope(actor, request), handle)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("open output: %w", err)
	}
	defer func() { _ = reader.Close() }()
	// A reader blocked in a read is unblocked by closing it when the context
	// ends; the sized reader then reports the context error.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = reader.Close()
		case <-stop:
		}
	}()
	if err = a.objects.Put(ctx, rec.ID, &sizedReader{ctx: ctx, r: reader, want: rec.Size}); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	// Completion: everything that was true at admission must be true now.
	complete := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.authorizeAll(ctx, actor, keys); err != nil {
			return err
		}
		if err := a.addonReady(ctx, rec.AddonID, rec.Generation); err != nil {
			return err
		}
		if !a.now().Before(rec.ExpiresAt) {
			return errExpired
		}
		meta, err := a.objects.Head(ctx, rec.ID)
		if err != nil || meta.Size != rec.Size {
			return errSizeMismatch
		}
		return a.meta.mutate(func(d *document) error {
			r := d.Artifacts[rec.ID]
			if r == nil || r.State != gateway.ArtifactTransferring {
				return gateway.ErrForbidden
			}
			r.State, r.Reason = gateway.ArtifactComplete, ""
			return nil
		})
	}
	if err = complete(); err != nil {
		a.dropUnlessComplete(rec.ID)
		return err
	}
	return nil
}

// Download streams the artifact of the actor to dst and returns its
// description. It authorizes the actor against every source resource again
// before the first byte and keeps the stream under revocation.
func (a *Artifacts) Download(ctx context.Context, actor contract.Actor, id string, dst io.Writer) (Artifact, error) {
	return a.download(ctx, actor, id, nil, dst)
}

func (a *Artifacts) download(ctx context.Context, actor contract.Actor, id string, begin func(Artifact) error, dst io.Writer) (Artifact, error) {
	r, err := a.lookup(actor, id)
	if err != nil {
		return Artifact{}, err
	}
	if r.State != gateway.ArtifactComplete {
		return Artifact{}, ErrNotReady
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return Artifact{}, ErrClosed
	}
	if a.downloads >= a.limits.Downloads {
		a.mu.Unlock()
		return Artifact{}, ErrBusy
	}
	a.downloads++
	a.wg.Add(1)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.downloads--
		a.mu.Unlock()
		a.wg.Done()
	}()
	// The stream ends at expiry and when the store closes.
	parent := ctx
	// A timer relative to the store's clock, so the stream also ends when
	// nothing is being read. The loop checks the clock itself as well.
	ctx, cancel := context.WithTimeout(ctx, max(r.ExpiresAt.Sub(a.now()), time.Nanosecond))
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	keys := keysOf(r.Sources)
	err = a.admit(ctx, actor, keys, func(ctx context.Context) error {
		cur, err := a.lookup(actor, id)
		if err != nil {
			return err
		}
		if cur.State != gateway.ArtifactComplete {
			return ErrNotReady
		}
		if err = a.authorizeAll(ctx, actor, keys); err != nil {
			return err
		}
		if err = a.addonReady(ctx, cur.AddonID, cur.Generation); err != nil {
			return err
		}
		meta, err := a.objects.Head(ctx, id)
		if err != nil {
			return a.unreadable(ctx, actor, id, err)
		}
		if meta.Size != cur.Size {
			return a.unreadable(ctx, actor, id, nil)
		}
		if begin != nil {
			if err = begin(cur.view()); err != nil {
				return err
			}
		}
		file, err := a.objects.Get(ctx, id)
		if err != nil {
			return a.unreadable(ctx, actor, id, err)
		}
		defer func() { _ = file.Close() }()
		r = cur
		return a.stream(ctx, actor, cur, keys, file, dst)
	})
	if errors.Is(err, context.Canceled) && parent.Err() == nil && a.ctx.Err() == nil {
		// The request is alive and the store is open, so the admission was
		// cancelled by a revocation of a source permission.
		err = fmt.Errorf("source access revoked: %w", gateway.ErrForbidden)
	}
	if err != nil {
		return Artifact{}, err
	}
	return r.view(), nil
}

// stream copies exactly the recorded size with one fixed buffer. Expiry, the
// account and the source permissions are looked at again while it runs.
func (a *Artifacts) stream(ctx context.Context, actor contract.Actor, r record, keys []policy.ResourceKey, src io.Reader, dst io.Writer) error {
	buf := make([]byte, copyBufferSize)
	var total int64
	last := a.now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := a.now()
		if !now.Before(r.ExpiresAt) {
			return errExpired
		}
		if now.Sub(last) >= a.limits.RecheckInterval {
			last = now
			if err := a.authorizeAll(ctx, actor, keys); err != nil {
				return err
			}
			if err := a.addonReady(ctx, r.AddonID, r.Generation); err != nil {
				return err
			}
		}
		n, err := src.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > r.Size {
				return ErrCorrupt
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			if total != r.Size {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// unreadable decides what a failed read of the bytes means. Only bytes that are
// definitely absent, not regular or of another size are corruption. Any other
// error is a store that cannot answer now: it is unavailable and leaves the
// bytes and the record as they are. A record that is gone, expired or no
// longer complete was removed by a sweep or a cancel while the download was
// admitted, and is answered like any other expired identifier.
func (a *Artifacts) unreadable(ctx context.Context, actor contract.Actor, id string, err error) error {
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrCorrupt) {
			return fmt.Errorf("addon artifact bytes: %w", gateway.ErrUnavailable)
		}
	}
	cur, lerr := a.lookup(actor, id)
	if lerr != nil {
		return lerr
	}
	if cur.State != gateway.ArtifactComplete {
		return ErrNotReady
	}
	a.corrupt(id)
	return ErrCorrupt
}

// corrupt records an artifact whose bytes do not match the record as failed
// and removes them. It is best effort: the download is already refused.
func (a *Artifacts) corrupt(id string) {
	_ = a.meta.mutate(func(d *document) error {
		if r := d.Artifacts[id]; r != nil && r.State == gateway.ArtifactComplete {
			r.State, r.Reason = gateway.ArtifactFailed, "stored bytes did not match the record"
		}
		return nil
	})
	_ = a.objects.Delete(context.Background(), id)
}

// adopt replaces the outputs of a read result by artifact identifiers and
// starts their transfers. The addon handles stay with the owner. When one
// output cannot be admitted the ones already started are cancelled.
func (a *Artifacts) adopt(ctx context.Context, actor contract.Actor, request contract.Request, result contract.Result) (contract.Result, error) {
	out := result
	out.Outputs = nil
	var started []string
	for _, o := range result.Outputs {
		art, err := a.Start(ctx, actor, request, o)
		if err != nil {
			for _, id := range started {
				a.discard(id)
			}
			return contract.Result{}, err
		}
		started = append(started, art.ID)
		out.Outputs = append(out.Outputs, contract.OutputDescription{Handle: art.ID, Name: art.Name, MediaType: art.MediaType, Size: art.Size})
	}
	return out, nil
}

// gatewayAdapter implements gateway.ArtifactProvider.
type gatewayAdapter struct{ a *Artifacts }

// Gateway is the provider the owner's gateway serves downloads and file
// results through.
func (a *Artifacts) Gateway() gateway.ArtifactProvider { return gatewayAdapter{a} }

func (g gatewayAdapter) Serve(ctx context.Context, actor contract.Actor, id string, begin func(Artifact) error, dst io.Writer) error {
	_, err := g.a.download(ctx, actor, id, begin, dst)
	return err
}

func (g gatewayAdapter) Status(ctx context.Context, actor contract.Actor, id string) (Artifact, error) {
	return g.a.Status(ctx, actor, id)
}

func (g gatewayAdapter) Adopt(ctx context.Context, actor contract.Actor, request contract.Request, result contract.Result) (contract.Result, error) {
	return g.a.adopt(ctx, actor, request, result)
}

// discard cancels a transfer if it is running, waits for it, and removes the
// record and the bytes. It is for work that was started and then not handed to
// anyone, so it may not keep quota.
func (a *Artifacts) discard(id string) {
	a.mu.Lock()
	j := a.jobs[id]
	if j != nil {
		j.cancelled = true
		j.cancel()
	}
	a.mu.Unlock()
	if j != nil {
		<-j.done
	}
	_ = a.meta.mutate(func(d *document) error {
		delete(d.Artifacts, id)
		return nil
	})
	_ = a.objects.Delete(context.Background(), id)
}
