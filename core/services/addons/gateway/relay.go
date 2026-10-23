// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type CurrentRuntime interface {
	Runtime(context.Context, string) (addonruntime.Instance, error)
}

func (g *Gateway) current(ctx context.Context, id string) (addonruntime.Instance, error) {
	if g.Runtime != nil {
		return g.Runtime.Runtime(ctx, id)
	}
	if g.Manager != nil {
		return g.Manager.Runtime(ctx, id)
	}
	return addonruntime.Instance{}, ErrUnavailable
}

type ResourceKey struct{ AddonID, ResourceID string }
type Admission interface {
	Admit(context.Context, contract.Actor, ResourceKey, string, func(context.Context) error) error
}

// AddonAdmission is the admission of a read that names no resource. It checks
// the account and that the add-on is not held, and nothing else: there is no
// resource whose role could apply. An Admission that does not implement it
// has no such reads.
type AddonAdmission interface {
	AdmitAddon(ctx context.Context, actor contract.Actor, addonID string, run func(context.Context) error) error
}

// Resolver returns a host-recorded binding, not an addon or model assertion.
// Output is an opaque host record bound to the requesting principal/operation.
type ResourceResolver interface {
	Resolve(context.Context, contract.Actor, contract.Request) (ResourceKey, error)
	Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error)
}

// toolSource is the source of declared tools: the configured one, else the
// manager. A nil manager must stay a nil interface.
func (g *Gateway) toolSource() ToolSource {
	if g.Tools != nil {
		return g.Tools
	}
	if g.Manager != nil {
		return g.Manager
	}
	return nil
}

func (g *Gateway) read(ctx context.Context, a contract.Actor, r contract.Request) (out contract.Result, err error) {
	if g.Admission == nil || g.Resolver == nil {
		return out, ErrForbidden
	}
	var admit func(run func(context.Context) error) error
	aa, hasAddonAdmission := g.Admission.(AddonAdmission)
	if r.ResourceID == "" && hasAddonAdmission && g.resourceless(ctx, r) {
		// A read tool that declares no resource runs without one. The add-on
		// answers for the actor of the envelope, which the host binds here, so
		// the result is scoped to the calling user.
		if e := AuthorizeTool(ctx, g.toolSource(), r.AddonID, r.Generation, r.Arguments); e != nil {
			return out, e
		}
		admit = func(run func(context.Context) error) error { return aa.AdmitAddon(ctx, a, r.AddonID, run) }
	} else {
		key, e := g.Resolver.Resolve(ctx, a, r)
		if e != nil || key.AddonID != r.AddonID || key.ResourceID == "" {
			return out, ErrForbidden
		}
		r.ResourceID = key.ResourceID
		// A tool the add-on did not declare never reaches it.
		if e := AuthorizeTool(ctx, g.toolSource(), r.AddonID, r.Generation, r.Arguments); e != nil {
			return out, e
		}
		admit = func(run func(context.Context) error) error { return g.Admission.Admit(ctx, a, key, "read", run) }
	}
	err = admit(func(ctx context.Context) error {
		if e := g.revalidate(ctx, a); e != nil {
			return e
		}
		instance, e := g.current(ctx, r.AddonID)
		if e != nil {
			return e
		}
		if instance.Generation != r.Generation {
			return ErrForbidden
		}
		out, e = instance.Client.Read(ctx, contract.Envelope{Request: r, Authority: contract.Authority{Actor: a, Scope: "read", ExpiresAt: time.Now().Add(30 * time.Second)}})
		return e
	})
	if err != nil || len(out.Outputs) == 0 {
		return
	}
	// A file result never reaches a client with the addon's own handle: the
	// owner turns each output into a private artifact and returns the host
	// identifiers. Without an artifact store the result is denied whole.
	if g.Artifacts == nil {
		return contract.Result{}, ErrForbidden
	}
	// Nothing is staged that the relay could never serve: an output larger
	// than the file bound is refused whole, before any byte moves.
	limits, e := g.Limits.resolved()
	if e != nil {
		return contract.Result{}, e
	}
	for _, o := range out.Outputs {
		if o.Size < 0 || o.Size > limits.Bytes {
			return contract.Result{}, ErrForbidden
		}
	}
	return g.Artifacts.Adopt(ctx, a, r, out)
}

// resourceless reports a request that names a read tool whose definition in
// the verified manifest declares no resource. Anything else, an unreadable
// manifest included, is false and takes the resource path, which denies a
// request without a resource.
func (g *Gateway) resourceless(ctx context.Context, r contract.Request) bool {
	tool, named := NamedTool(r.Arguments)
	if !named {
		return false
	}
	src, ok := g.toolSource().(CapabilitySource)
	if !ok {
		return false
	}
	caps, err := src.DeclaredCapabilities(ctx, r.AddonID, r.Generation)
	if err != nil {
		return false
	}
	for _, d := range caps.ToolDefinitions {
		if d.Name == tool {
			return contract.IsResourceless(d)
		}
	}
	return false
}

func (g *Gateway) Relay(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	if e := g.revalidate(ctx, a); e != nil {
		return e
	}
	if r.Kind != "status" && r.Kind != "progress" && r.Kind != "file" && r.Kind != "asset" && r.Kind != "icon" && r.Kind != "json" {
		return ErrForbidden
	}
	if r.Kind == "json" && (g.Admission == nil || g.Resolver == nil) && g.Forward == nil {
		return ErrForbidden
	}
	if r.Kind == "file" && g.Artifacts == nil && g.Forward == nil {
		return ErrForbidden
	}
	limits, err := g.Limits.resolved()
	if err != nil {
		return err
	}
	g.streamsMu.Lock()
	if g.streams >= limits.Concurrency {
		g.streamsMu.Unlock()
		return ErrUnavailable
	}
	g.streams++
	g.streamsMu.Unlock()
	defer func() { g.streamsMu.Lock(); g.streams--; g.streamsMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, limits.Deadline)
	defer cancel()
	if w == nil {
		return ErrForbidden
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(limits.Deadline))
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// done closes before the deferred cancel runs. When both are ready
			// the relay already returned, and expiring the deadline now would
			// break the caller's final write, such as a typed denial status.
			select {
			case <-done:
			default:
				_ = rc.SetWriteDeadline(time.Now())
			}
		case <-done:
		}
	}()
	if g.Forward != nil {
		return g.Forward.Relay(ctx, a, r, w)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch r.Kind {
	case "status", "progress":
		if r.Request.Action != "operation-status" {
			return ErrForbidden
		}
		previous := ""
		total := 0
		heartbeat := time.Now()
		for {
			result, e := g.Dispatch(ctx, a, r.Request)
			if e != nil {
				return e
			}
			if r.Kind == "status" {
				w.Header().Set("Content-Type", "application/json")
				_, e = w.Write(result.Payload)
				return e
			}
			w.Header().Set("Content-Type", "text/event-stream")
			text := string(result.Payload)
			if text != previous || time.Since(heartbeat) >= limits.Heartbeat {
				event := []byte("event: lifecycle\ndata: " + text + "\n\n")
				total += len(event)
				if int64(total) > limits.ProgressBytes {
					return ErrUnavailable
				}
				if _, e = w.Write(event); e != nil {
					return e
				}
				if e = rc.Flush(); e != nil {
					return e
				}
				previous = text
				heartbeat = time.Now()
			}
			var state struct{ Phase string }
			if json.Unmarshal(result.Payload, &state) != nil {
				return ErrUnavailable
			}
			if state.Phase == "completed" || state.Phase == "failed" {
				return nil
			}
			timer := time.NewTimer(limits.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	case "json":
		if r.Request.Action != "read" {
			return ErrForbidden
		}
		result, e := g.Dispatch(ctx, a, r.Request)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(result)
	case "file":
		return g.file(ctx, a, r, w)
	case "asset":
		return g.asset(ctx, a, r, w)
	case "icon":
		return g.icon(ctx, a, r, w)
	}
	return ErrForbidden
}

// file serves a private artifact by its host identifier. The bytes come from
// the owner's staged copy, never from the addon, and the artifact store decides
// who may read them; this layer only bounds the stream and sets the headers.
func (g *Gateway) file(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	limits, err := g.Limits.resolved()
	if err != nil {
		return err
	}
	if g.Artifacts == nil {
		return ErrForbidden
	}
	tw := &trackedWriter{ResponseWriter: w}
	err = g.Artifacts.Serve(ctx, a, r.Handle, func(art Artifact) error {
		if art.Size < 0 || art.Size > limits.Bytes {
			return ErrForbidden
		}
		h := w.Header()
		h.Set("Content-Type", art.MediaType)
		h.Set("Content-Disposition", ContentDisposition(art.Name))
		h.Set("Content-Length", strconv.FormatInt(art.Size, 10))
		h.Set("Cache-Control", "private, no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		return nil
	}, tw)
	if err != nil && !tw.committed {
		// A refusal after the headers were prepared must not read as a file.
		for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Length", "Content-Security-Policy"} {
			w.Header().Del(name)
		}
	}
	return err
}

func (g *Gateway) copy(ctx context.Context, a contract.Actor, w io.Writer, r io.ReadCloser, size int64) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-stop:
		}
	}()
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := g.revalidate(ctx, a); e != nil {
			return e
		}
		n, e := r.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > size {
				return ErrUnavailable
			}
			if _, err := w.Write(buffer[:n]); err != nil {
				return err
			}
		}
		if e == io.EOF {
			if total != size {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		if e != nil {
			return e
		}
	}
}

// asset serves one declared file of the installed package of an enabled addon,
// named by the package digest. The owner's journal decides: a disabled,
// uninstalled or replaced package, a digest that is not the current one and a
// file the manifest does not declare are all the same denial, so an old page
// that is still open cannot fetch withdrawn code. The bytes never come from an
// addon process and only a response for the current digest is cacheable as
// immutable.
func (g *Gateway) asset(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	limits, err := g.Limits.resolved()
	if err != nil {
		return err
	}
	if g.Manager == nil {
		return ErrUnavailable
	}
	p, _, e := g.Manager.AppAsset(ctx, a, r.Request.AddonID, r.Digest)
	if errors.Is(e, addons.ErrForbidden) {
		return ErrForbidden
	}
	if e != nil {
		return e
	}
	f, e := openAsset(p, r.Handle)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > limits.AssetBytes {
		return ErrForbidden
	}
	media := "text/javascript"
	if filepath.Ext(r.Handle) == ".css" {
		media = "text/css"
	}
	h := w.Header()
	h.Set("Content-Type", media)
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	return g.copy(ctx, a, w, f, info.Size())
}

func openAsset(p packages.PackageReceipt, name string) (*os.File, error) {
	ui := p.Manifest.Capabilities.UI
	if ui == nil {
		return nil, ErrForbidden
	}
	allowed := name == ui.Entry
	for _, css := range ui.CSS {
		allowed = allowed || name == css
	}
	if !allowed || !filepath.IsLocal(name) {
		return nil, ErrForbidden
	}
	root, e := os.OpenRoot(p.Directory)
	if e != nil {
		return nil, e
	}
	defer func() { _ = root.Close() }()
	return root.Open(name)
}
