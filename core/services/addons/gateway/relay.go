// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

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

// Resolver returns a host-recorded binding, not an addon or model assertion.
// Output is an opaque host record bound to the requesting principal/operation.
type ResourceResolver interface {
	Resolve(context.Context, contract.Actor, contract.Request) (ResourceKey, error)
	Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error)
}

func (g *Gateway) read(ctx context.Context, a contract.Actor, r contract.Request) (out contract.Result, err error) {
	if g.Admission == nil || g.Resolver == nil {
		return out, ErrForbidden
	}
	key, e := g.Resolver.Resolve(ctx, a, r)
	if e != nil || key.AddonID != r.AddonID || key.ResourceID == "" {
		return out, ErrForbidden
	}
	r.ResourceID = key.ResourceID
	err = g.Admission.Admit(ctx, a, key, "read", func(ctx context.Context) error {
		if g.Revalidate(ctx, a) != nil {
			return ErrForbidden
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
	return
}
func (g *Gateway) Relay(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	if g.Revalidate == nil || g.Revalidate(ctx, a) != nil {
		return ErrForbidden
	}
	if r.Kind != "status" && r.Kind != "progress" && r.Kind != "file" && r.Kind != "asset" && r.Kind != "json" {
		return ErrForbidden
	}
	if (r.Kind == "file" || r.Kind == "json") && (g.Admission == nil || g.Resolver == nil) && g.Forward == nil {
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
	}
	return ErrForbidden
}
func (g *Gateway) file(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	limits, err := g.Limits.resolved()
	if err != nil {
		return err
	}
	key, e := g.Resolver.Resolve(ctx, a, r.Request)
	if e != nil || key.AddonID != r.Request.AddonID || key.ResourceID == "" {
		return ErrForbidden
	}
	r.Request.ResourceID = key.ResourceID
	return g.Admission.Admit(ctx, a, key, "output", func(ctx context.Context) error {
		if g.Revalidate(ctx, a) != nil {
			return ErrForbidden
		}
		output, e := g.Resolver.Output(ctx, a, r.Request, r.Handle)
		if e != nil || output.Size < 0 || output.Size > limits.Bytes {
			return ErrForbidden
		}
		instance, e := g.current(ctx, key.AddonID)
		if e != nil {
			return e
		}
		if instance.Generation != r.Request.Generation {
			return ErrForbidden
		}
		reader, e := instance.Client.OpenOutput(ctx, contract.Envelope{Request: r.Request, Authority: contract.Authority{Actor: a, Scope: "output", ExpiresAt: time.Now().Add(60 * time.Second)}}, output.Handle)
		if e != nil {
			return e
		}
		defer reader.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": output.Name}))
		return g.copy(ctx, a, w, reader, output.Size)
	})
}
func (g *Gateway) copy(ctx context.Context, a contract.Actor, w io.Writer, r io.ReadCloser, size int64) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			r.Close()
		case <-stop:
		}
	}()
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if g.Revalidate(ctx, a) != nil {
			return ErrForbidden
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
func (g *Gateway) asset(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	limits, err := g.Limits.resolved()
	if err != nil {
		return err
	}
	// Installation administration is sufficient only for immutable package code,
	// never for resource data. Status itself has no actor check.
	if g.Manager == nil {
		return ErrUnavailable
	}
	if _, e := g.Manager.OperationStatus(ctx, a, r.Request.OperationID); e != nil {
		return ErrForbidden
	}
	status, e := g.Manager.Status(ctx, r.Request.AddonID)
	if e != nil {
		return e
	}
	if !status.Installed || status.Generation != r.Request.Generation {
		return ErrForbidden
	}
	for _, generation := range status.Generations {
		if generation.Number != r.Request.Generation {
			continue
		}
		p := generation.Receipt
		f, e := openAsset(p, r.Handle)
		if e != nil {
			return e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > limits.AssetBytes {
			return ErrForbidden
		}
		media := "text/javascript"
		if filepath.Ext(r.Handle) == ".css" {
			media = "text/css"
		}
		w.Header().Set("Content-Type", media)
		return g.copy(ctx, a, w, f, info.Size())
	}
	return ErrForbidden
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
	defer root.Close()
	return root.Open(name)
}
