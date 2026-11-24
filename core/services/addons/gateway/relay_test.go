// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type admitted struct{ active atomic.Bool }

func (a *admitted) Admit(ctx context.Context, _ contract.Actor, k ResourceKey, action string, run func(context.Context) error) error {
	if k.ResourceID != "trusted" {
		return ErrForbidden
	}
	a.active.Store(true)
	defer a.active.Store(false)
	return run(ctx)
}

type resolved struct{}

func (resolved) Resolve(context.Context, contract.Actor, contract.Request) (ResourceKey, error) {
	return ResourceKey{AddonID: "sample", ResourceID: "trusted"}, nil
}
func (resolved) Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error) {
	return contract.OutputDescription{Handle: "private", Name: "report.csv", MediaType: "text/csv", Size: 4}, nil
}

type currentFixture struct{ client contract.Runtime }

func (c currentFixture) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: c.client}, nil
}

type runtimeFixture struct {
	contract.Runtime
	admission *admitted
	open      func(context.Context) io.ReadCloser
}

func (f runtimeFixture) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	if !f.admission.active.Load() || e.Request.ResourceID != "trusted" {
		return contract.Result{}, ErrForbidden
	}
	return contract.Result{Payload: []byte(`{"ok":true}`)}, nil
}
func (f runtimeFixture) OpenOutput(ctx context.Context, e contract.Envelope, h string) (io.ReadCloser, error) {
	if !f.admission.active.Load() || e.Request.ResourceID != "trusted" || h != "private" {
		return nil, ErrForbidden
	}
	return f.open(ctx), nil
}

// fakeArtifacts is a stand-in owner artifact store: one artifact whose bytes
// come from open, or an error returned before the first byte.
type fakeArtifacts struct {
	art    Artifact
	open   func(context.Context) io.ReadCloser
	err    error
	active atomic.Bool
	serves atomic.Int32
	// adopted records the results handed to Adopt.
	adopted atomic.Int32
}

func (f *fakeArtifacts) Serve(ctx context.Context, _ contract.Actor, id string, begin func(Artifact) error, dst io.Writer) error {
	f.serves.Add(1)
	if f.err != nil {
		return f.err
	}
	if id != f.art.ID {
		return ErrForbidden
	}
	if e := begin(f.art); e != nil {
		return e
	}
	f.active.Store(true)
	defer f.active.Store(false)
	r := f.open(ctx)
	defer func() { _ = r.Close() }()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-stop:
		}
	}()
	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, e := r.Read(buf)
		if n > 0 {
			if _, we := dst.Write(buf[:n]); we != nil {
				return we
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}

func (f *fakeArtifacts) Status(_ context.Context, _ contract.Actor, id string) (Artifact, error) {
	if f.err != nil {
		return Artifact{}, f.err
	}
	if id != f.art.ID {
		return Artifact{}, ErrForbidden
	}
	return f.art, nil
}

func (f *fakeArtifacts) Adopt(_ context.Context, _ contract.Actor, _ contract.Request, r contract.Result) (contract.Result, error) {
	f.adopted.Add(1)
	out := r
	out.Outputs = nil
	for _, o := range r.Outputs {
		out.Outputs = append(out.Outputs, contract.OutputDescription{Handle: f.art.ID, Name: o.Name, MediaType: o.MediaType, Size: o.Size})
	}
	return out, nil
}

const fakeArtifactID = "a0123456789abcdef0123456789abcdef"

func dataArtifacts(body string) *fakeArtifacts {
	return &fakeArtifacts{
		art:  Artifact{ID: fakeArtifactID, Name: "report.csv", MediaType: "text/csv", Size: int64(len(body)), State: ArtifactComplete},
		open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader(body)) },
	}
}

var _ = Describe("bounded private relays", func() {
	It("keeps resolved resources under admission for JSON and attachment bytes", func() {
		admission := &admitted{}
		client := runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader("data")) }}
		files := dataArtifacts("data")
		g := &Gateway{Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}, Artifacts: files, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		r := contract.Request{AddonID: "sample", ResourceID: "forged", Generation: 1, OperationID: "op", Action: "read", Arguments: []byte(`{}`)}
		result, e := g.Dispatch(context.Background(), contract.Actor{}, r)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(result.Payload)).To(ContainSubstring("ok"))
		Expect(admission.active.Load()).To(BeFalse())
		w := httptest.NewRecorder()
		Expect(g.Relay(context.Background(), contract.Actor{}, RelayRequest{Request: r, Kind: "file", Handle: fakeArtifactID}, w)).To(Succeed())
		Expect(w.Body.String()).To(Equal("data"))
		Expect(w.Header().Get("Content-Disposition")).To(HavePrefix("attachment;"))
		Expect(w.Header().Get("Content-Type")).To(Equal("text/csv"))
		Expect(w.Header().Get("Content-Length")).To(Equal("4"))
		Expect(w.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(w.Header().Get("Cache-Control")).To(Equal("private, no-store"))
		// A handle that is not an artifact of the principal is a denial and
		// leaves no file headers behind.
		w = httptest.NewRecorder()
		Expect(g.Relay(context.Background(), contract.Actor{}, RelayRequest{Request: r, Kind: "file", Handle: "private"}, w)).To(MatchError(ErrForbidden))
		Expect(w.Body.Len()).To(BeZero())
		Expect(w.Header().Get("Content-Disposition")).To(BeEmpty())
		Expect(w.Header().Get("Content-Length")).To(BeEmpty())
	})
	It("closes interrupted output and releases admission on disconnect", func() {
		reader, writer := io.Pipe()
		defer func() { _ = writer.Close() }()
		started := make(chan struct{})
		files := &fakeArtifacts{art: Artifact{ID: fakeArtifactID, Name: "r.csv", MediaType: "text/csv", Size: 4}, open: func(context.Context) io.ReadCloser { close(started); return reader }}
		g := &Gateway{Artifacts: files, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- g.Relay(ctx, contract.Actor{}, RelayRequest{Request: contract.Request{AddonID: "sample", Generation: 1}, Kind: "file", Handle: fakeArtifactID}, httptest.NewRecorder())
		}()
		Eventually(started, time.Second).Should(BeClosed())
		Expect(files.active.Load()).To(BeTrue())
		cancel()
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
		Expect(files.active.Load()).To(BeFalse())
	})
	It("rechecks revoked credentials during attachment copying", func() {
		// The account check runs before the artifact store is asked, so a
		// revoked credential never reaches the bytes. The store itself looks
		// at the account again while it streams.
		var checks atomic.Int32
		files := dataArtifacts("data")
		g := &Gateway{Artifacts: files, Revalidate: func(context.Context, contract.Actor) error {
			if checks.Add(1) >= 1 {
				return ErrForbidden
			}
			return nil
		}}
		w := httptest.NewRecorder()
		e := g.Relay(context.Background(), contract.Actor{}, RelayRequest{Request: contract.Request{AddonID: "sample", Generation: 1}, Kind: "file", Handle: fakeArtifactID}, w)
		Expect(e).To(Equal(ErrForbidden))
		Expect(w.Body.Len()).To(BeZero())
		Expect(files.serves.Load()).To(BeZero())
	})
})

var _ = Describe("relay lifecycle cancellation", func() {
	It("propagates interrupted progress cancellation through the receiving frontend", func() {
		canceled := make(chan struct{})
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: lifecycle\ndata: {}\n\n"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(canceled)
			})
		})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		e := cs["front-a"].Relay(ctx, contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}, RelayRequest{Request: contract.Request{OperationID: "op", Action: "operation-status"}, Kind: "progress"}, httptest.NewRecorder())
		Expect(e).To(HaveOccurred())
		Eventually(canceled, time.Second).Should(BeClosed())
	})
})

var _ = Describe("immutable asset containment", func() {
	It("serves only declared installed generation assets and rejects traversal", func() {
		root := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(root, "ui.js"), []byte("export default {}"), 0400)).To(Succeed())
		p := packages.PackageReceipt{Directory: root, Manifest: contract.Manifest{Capabilities: contract.Capabilities{UI: &contract.UI{Entry: "ui.js"}}}}
		f, e := openAsset(p, "ui.js")
		Expect(e).NotTo(HaveOccurred())
		_ = f.Close()
		_, e = openAsset(p, "../secret")
		Expect(e).To(HaveOccurred())
		Expect(os.Symlink(filepath.Join(GinkgoT().TempDir(), "secret"), filepath.Join(root, "outside.js"))).To(Succeed())
		p.Manifest.Capabilities.UI.Entry = "outside.js"
		_, e = openAsset(p, "outside.js")
		Expect(e).To(HaveOccurred())
	})
})
