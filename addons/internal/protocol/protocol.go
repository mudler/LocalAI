// SPDX-License-Identifier: MIT

// Package protocol speaks the LocalAI addon runtime protocol: the
// startup handshake, authenticated loopback requests, previews, applies,
// receipts and outputs. It mirrors the host's wire types instead of importing
// the owner's packages, so an add-on executable links none of the host.
package protocol

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Registration, Startup, ApplyRequest, ReceiptRequest and OutputRequest are the
// wire shapes the owner sends. Field names are the protocol and must not drift;
// the integration suite starts the real executable under the real supervisor.
type Registration struct {
	AddonID      string                `json:"addonId"`
	Generation   uint64                `json:"generation"`
	Incarnation  string                `json:"incarnation"`
	Protocol     int                   `json:"protocol"`
	Endpoint     string                `json:"endpoint"`
	Capabilities contract.Capabilities `json:"capabilities"`
}
type Startup struct {
	Registration Registration    `json:"registration"`
	Credential   string          `json:"credential"`
	Limits       contract.Limits `json:"limits"`
	DataDir      string          `json:"dataDir,omitempty"`
	// HostEndpoint and HostCredential are set only for an add-on that the host
	// setting names. They reach the host operation channel.
	HostEndpoint   string `json:"hostEndpoint,omitempty"`
	HostCredential string `json:"hostCredential,omitempty"`
}
type ApplyRequest struct {
	Envelope contract.Envelope `json:"envelope"`
	Revision contract.Revision `json:"revision"`
}
type ReceiptRequest struct {
	OperationID string `json:"operationId"`
}
type OutputRequest struct {
	Envelope contract.Envelope `json:"envelope"`
	Handle   string            `json:"handle"`
}

// Errors a Runtime returns map to protocol status codes. Any other error is a
// 503: the owner treats it as uncertain and never as a rejection.
var (
	ErrBadRequest = errors.New("invalid request")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrForbidden  = errors.New("forbidden")
)

// ReadStartup decodes the startup object from the inherited descriptor and
// returns whatever the decoder buffered beyond it, for the parent-death watch.
func ReadStartup(parent io.Reader) (Startup, io.Reader, error) {
	d := json.NewDecoder(parent)
	var s Startup
	if err := d.Decode(&s); err != nil {
		return s, nil, err
	}
	return s, io.MultiReader(d.Buffered(), parent), nil
}

// ExitWithParent ends the process when the owner closes the startup channel.
func ExitWithParent(rest io.Reader) {
	go func() { _, _ = io.Copy(io.Discard, rest); os.Exit(0) }()
}

// Handler authenticates every request with the incarnation credential and
// header, then validates the envelope's binding to this addon and generation.
func Handler(startup Startup, rt contract.Runtime) http.Handler {
	reg := startup.Registration
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+startup.Credential))
		inc := subtle.ConstantTimeCompare([]byte(r.Header.Get("X-LocalAI-Incarnation")), []byte(reg.Incarnation))
		if auth&inc != 1 || startup.Credential == "" {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, startup.Limits.EnvelopeBytes)
		defer func() { _ = r.Body.Close() }()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		read := func(v any) bool {
			if decoder.Decode(v) != nil || decoder.Decode(new(any)) != io.EOF {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return false
			}
			return true
		}
		valid := func(v contract.Envelope, scope string) bool {
			if v.Validate(time.Now(), startup.Limits) != nil || v.Authority.Scope != scope || v.Request.AddonID != reg.AddonID || v.Request.Generation != reg.Generation {
				http.Error(w, "invalid authority", http.StatusForbidden)
				return false
			}
			return true
		}
		reply := func(v any, err error) {
			switch {
			case err == nil:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(v)
			case errors.Is(err, ErrBadRequest):
				http.Error(w, "invalid request", http.StatusBadRequest)
			case errors.Is(err, ErrNotFound):
				http.Error(w, "not found", http.StatusNotFound)
			case errors.Is(err, ErrForbidden):
				http.Error(w, "forbidden", http.StatusForbidden)
			case errors.Is(err, ErrConflict):
				http.Error(w, "conflict", http.StatusConflict)
			default:
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}
		}
		ctx := r.Context()
		switch r.URL.Path {
		case "/health":
			reply(reg, nil)
		case "/read":
			var v contract.Envelope
			if read(&v) && valid(v, "read") {
				out, err := rt.Read(ctx, v)
				reply(out, err)
			}
		case "/preview":
			var v contract.Envelope
			if read(&v) && valid(v, "preview") {
				out, err := rt.Preview(ctx, v)
				reply(out, err)
			}
		case "/apply":
			var v ApplyRequest
			if read(&v) && valid(v.Envelope, "apply") {
				out, err := rt.Apply(ctx, v.Envelope, v.Revision)
				reply(out, err)
			}
		case "/receipt":
			var v ReceiptRequest
			if read(&v) {
				out, err := rt.Receipt(ctx, v.OperationID)
				reply(out, err)
			}
		case "/output":
			var v OutputRequest
			if !read(&v) || !valid(v.Envelope, "output") {
				return
			}
			body, err := rt.OpenOutput(ctx, v.Envelope, v.Handle)
			if err != nil {
				reply(nil, err)
				return
			}
			defer func() { _ = body.Close() }()
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.Copy(w, io.LimitReader(body, startup.Limits.ArtifactBytes+1))
		default:
			http.NotFound(w, r)
		}
	})
}

// Serve runs the protocol on an already bound listener until ctx ends.
func Serve(ctx context.Context, l net.Listener, startup Startup, rt contract.Runtime) error {
	srv := &http.Server{Handler: Handler(startup, rt), ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 16 << 10}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Run is the whole behaviour of an add-on executable: the owner starts it with
// "--localai-addon-v1", descriptor 3 is a bound loopback listener and
// descriptor 4 carries the startup object. The factory builds the runtime
// from that object, so a runtime can use the host fields of it. It returns the
// process exit code.
func Run(args []string, factory func(Startup) contract.Runtime) int {
	if len(args) != 2 || args[1] != "--localai-addon-v1" {
		return 2
	}
	startup, rest, err := ReadStartup(os.NewFile(4, "startup"))
	if err != nil || !filepath.IsAbs(startup.DataDir) {
		return 3
	}
	ExitWithParent(rest)
	listener, err := net.FileListener(os.NewFile(3, "listener"))
	if err != nil {
		return 6
	}
	if err := Serve(context.Background(), listener, startup, factory(startup)); err != nil {
		return 8
	}
	return 0
}
