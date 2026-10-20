// SPDX-License-Identifier: MIT
package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestArtifacts(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon artifacts") }

// fakeClock is the store's clock. Tests move it; nothing sleeps.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// accounts is the host's account check. A hook runs inside every check, which
// lets a test change the world at the exact moment admission is happening.
type accounts struct {
	mu   sync.Mutex
	bad  map[string]error
	hook func()
}

func (a *accounts) check(_ context.Context, actor contract.Actor) error {
	a.mu.Lock()
	err, hook := a.bad[actor.UserID], a.hook
	a.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (a *accounts) set(user string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bad == nil {
		a.bad = map[string]error{}
	}
	a.bad[user] = err
}

func (a *accounts) setHook(h func()) { a.mu.Lock(); a.hook = h; a.mu.Unlock() }

// reader is a controllable addon output.
type reader struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (r *reader) Close() error { r.once.Do(func() { close(r.closed) }); return nil }

func newReader(src io.Reader) *reader { return &reader{Reader: src, closed: make(chan struct{})} }

// blocking returns the first chunk, then waits for release or for the reader
// to be closed, which is what cancelling a real HTTP body does.
type blocking struct {
	first   []byte
	rest    io.Reader
	release chan struct{}
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
	sent    bool
}

func newBlocking(first, rest string) *blocking {
	return &blocking{first: []byte(first), rest: strings.NewReader(rest), release: make(chan struct{}), started: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blocking) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		n := copy(p, b.first)
		close(b.started)
		return n, nil
	}
	select {
	case <-b.release:
		return b.rest.Read(p)
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
}

func (b *blocking) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

// outputRuntime is a stand-in addon runtime with outputs by handle.
type outputRuntime struct {
	mu      sync.Mutex
	outputs map[string]func(context.Context) (io.ReadCloser, error)
	opens   map[string]int
	seen    []contract.Envelope
}

func newOutputRuntime() *outputRuntime {
	return &outputRuntime{outputs: map[string]func(context.Context) (io.ReadCloser, error){}, opens: map[string]int{}}
}

func (r *outputRuntime) set(handle string, open func(context.Context) (io.ReadCloser, error)) {
	r.mu.Lock()
	r.outputs[handle] = open
	r.mu.Unlock()
}

func (r *outputRuntime) text(handle, body string) {
	r.set(handle, func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil })
}

func (r *outputRuntime) opened(handle string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opens[handle]
}

func (r *outputRuntime) totalOpens() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.opens {
		n += c
	}
	return n
}

func (r *outputRuntime) envelopes() []contract.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contract.Envelope(nil), r.seen...)
}

func (r *outputRuntime) OpenOutput(ctx context.Context, e contract.Envelope, handle string) (io.ReadCloser, error) {
	r.mu.Lock()
	open, ok := r.outputs[handle]
	r.opens[handle]++
	r.seen = append(r.seen, e)
	r.mu.Unlock()
	if !ok {
		return nil, errors.New("no such output")
	}
	return open(ctx)
}

func (r *outputRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, errors.New("unused")
}
func (r *outputRuntime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errors.New("unused")
}
func (r *outputRuntime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errors.New("unused")
}
func (r *outputRuntime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errors.New("unused")
}

type instances struct {
	mu  sync.Mutex
	gen uint64
	rt  contract.Runtime
	err error
	// tools and toolErr answer DeclaredTools; nothing is declared by default.
	tools   []string
	toolErr error
}

func (i *instances) DeclaredTools(context.Context, string, uint64) ([]string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.toolErr != nil {
		return nil, i.toolErr
	}
	if i.tools == nil {
		return nil, gateway.ErrForbidden
	}
	return append([]string(nil), i.tools...), nil
}

func (i *instances) Runtime(context.Context, string) (addonruntime.Instance, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.err != nil {
		return addonruntime.Instance{}, i.err
	}
	return addonruntime.Instance{Generation: i.gen, Incarnation: "current", Client: i.rt}, nil
}

// addonState is the lifecycle manager's answer.
type addonState struct {
	mu sync.Mutex
	st AddonState
	// err is returned instead of a state.
	err error
}

func (a *addonState) get(context.Context, string) (AddonState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st, a.err
}

func (a *addonState) set(f func(*AddonState)) { a.mu.Lock(); f(&a.st); a.mu.Unlock() }

type fixture struct {
	dir      string
	store    *policy.Store
	policy   *policy.Policy
	arts     *Artifacts
	rt       *outputRuntime
	inst     *instances
	clock    *fakeClock
	addon    *addonState
	accounts *accounts
	alice    contract.Actor
	bob      contract.Actor
	carol    contract.Actor
	key      policy.ResourceKey
	limits   Limits
	spy      *spyAuthority
}

// spyAuthority records the context of the latest admitted run of a principal
// on a resource, so a test can wait until a revocation really cancelled it.
type spyAuthority struct {
	Authority
	mu   sync.Mutex
	runs map[string]context.Context
}

func (s *spyAuthority) Admit(ctx context.Context, a contract.Actor, k policy.ResourceKey, action policy.Action, run func(context.Context) error) error {
	return s.Authority.Admit(ctx, a, k, action, func(c context.Context) error {
		s.mu.Lock()
		s.runs[a.UserID+"/"+k.ResourceID] = c
		s.mu.Unlock()
		return run(c)
	})
}

func (s *spyAuthority) run(user, resource string) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[user+"/"+resource]
}

func actor(id string) contract.Actor {
	return contract.Actor{UserID: id, CredentialID: "key:" + id, Mode: "named-user"}
}

func testLimits() Limits {
	l := DefaultLimits()
	l.MaxBytes = 1 << 20
	l.UserBytes = 8 << 20
	l.TotalBytes = 16 << 20
	l.UserCount = 8
	l.TotalCount = 32
	l.Transfers = 8
	l.UserTransfers = 4
	l.Downloads = 4
	l.Expiry = time.Hour
	l.TransferTimeout = 10 * time.Minute
	l.RecheckInterval = time.Second
	return l
}

// open builds the real owner policy over a real store and an artifact store in
// dir, on a fresh scratch directory unless dir is given.
func newFixture(tune ...func(*Limits)) *fixture {
	return openFixture(GinkgoT().TempDir(), nil, tune...)
}

func openFixture(dir string, previous *fixture, tune ...func(*Limits)) *fixture {
	f := &fixture{dir: dir, rt: newOutputRuntime(), inst: &instances{gen: 1}, accounts: &accounts{}, alice: actor("alice"), bob: actor("bob"), carol: actor("carol"), limits: testLimits()}
	f.inst.rt = f.rt
	f.clock = &fakeClock{t: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
	f.addon = &addonState{st: AddonState{Installed: true, Enabled: true, Generation: 1}}
	if previous != nil {
		f.clock, f.addon, f.rt, f.inst = previous.clock, previous.addon, previous.rt, previous.inst
	}
	for _, t := range tune {
		t(&f.limits)
	}
	var err error
	f.store, err = policy.OpenStore(filepath.Join(dir, "policy"), policy.ModeNamedUser)
	Expect(err).NotTo(HaveOccurred())
	f.policy = policy.New(f.store, policy.Options{
		Revalidate:      f.accounts.check,
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
		DrainTimeout:    5 * time.Second,
	})
	if previous == nil {
		f.key, err = f.policy.Register(context.Background(), f.alice, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.Commit(context.Background(), f.alice, f.key)).To(Succeed())
		Expect(f.policy.SetGrant(context.Background(), f.alice, f.key, "bob", policy.RoleViewer)).To(Succeed())
	} else {
		f.key = previous.key
	}
	f.spy = &spyAuthority{Authority: f.policy, runs: map[string]context.Context{}}
	f.arts, err = Open(Options{Dir: filepath.Join(dir, "artifacts"), Policy: f.spy, Runtime: f.inst, Addon: f.addon.get, Limits: f.limits, Now: f.clock.now})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = f.arts.Close(); _ = f.store.Close() })
	return f
}

// request is the request that produced an output of the fixture resource.
func (f *fixture) request(op string) contract.Request {
	return contract.Request{AddonID: "sample", ResourceID: f.key.ResourceID, OperationID: op, Generation: 1, Action: "read", Arguments: json.RawMessage(`{}`)}
}

func out(handle, name, media string, size int) contract.OutputDescription {
	return contract.OutputDescription{Handle: handle, Name: name, MediaType: media, Size: int64(size)}
}

// ingest runs one text output to completion.
func (f *fixture) ingest(actor contract.Actor, op, handle, body string) Artifact {
	f.rt.text(handle, body)
	art, err := f.arts.Ingest(context.Background(), actor, f.request(op), out(handle, "report.csv", "text/csv", len(body)))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, art.State).To(Equal(gateway.ArtifactComplete))
	return art
}

func (f *fixture) download(actor contract.Actor, id string) (string, error) {
	var b bytes.Buffer
	_, err := f.arts.Download(context.Background(), actor, id, &b)
	return b.String(), err
}

func files(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func (f *fixture) stagedFiles() []string {
	return append(files(filepath.Join(f.dir, "artifacts", blobDir)), files(filepath.Join(f.dir, "artifacts", stagingDir))...)
}

var idShape = regexp.MustCompile(`^a[0-9a-f]{32}$`)

// settled waits for a background transfer to leave the transferring state.
// The transfer reports through the store only, so the test asks the store.
func (f *fixture) settled(actor contract.Actor, id string) Artifact {
	var art Artifact
	EventuallyWithOffset(1, func() gateway.ArtifactState {
		var err error
		art, err = f.arts.Status(context.Background(), actor, id)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return art.State
	}, 5*time.Second, 5*time.Millisecond).ShouldNot(Equal(gateway.ArtifactTransferring))
	return art
}

var _ = Describe("private artifact ingestion", func() {
	It("ingests a file result into a private staged artifact with a host-generated identifier", func() {
		f := newFixture()
		art := f.ingest(f.alice, "export-1", "output-1", "a,b\n1,2\n")
		Expect(art.ID).To(MatchRegexp(idShape.String()))
		Expect(art.ID).NotTo(ContainSubstring("output"))
		Expect(art.Principal).To(Equal("alice"))
		Expect(art.Sources).To(Equal([]gateway.ArtifactResource{{AddonID: "sample", ResourceID: f.key.ResourceID}}))
		Expect(art.AddonID).To(Equal("sample"))
		Expect(art.Generation).To(Equal(uint64(1)))
		Expect(art.OperationID).To(Equal("export-1"))
		Expect(art.Name).To(Equal("report.csv"))
		Expect(art.MediaType).To(Equal("text/csv"))
		Expect(art.Size).To(Equal(int64(8)))
		Expect(art.CreatedAt).To(Equal(f.clock.now()))
		Expect(art.ExpiresAt).To(Equal(f.clock.now().Add(f.limits.Expiry)))
		body, err := f.download(f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("a,b\n1,2\n"))
		// The runtime saw the output scope of the requesting principal and the
		// operation that produced the output.
		env := f.rt.envelopes()
		Expect(env).To(HaveLen(1))
		Expect(env[0].Authority.Scope).To(Equal("output"))
		Expect(env[0].Authority.Actor).To(Equal(f.alice))
		Expect(env[0].Request.OperationID).To(Equal("export-1"))
		// Everything is owner-only and under the private root.
		root := filepath.Join(f.dir, "artifacts")
		for _, d := range []string{root, filepath.Join(root, blobDir), filepath.Join(root, stagingDir), filepath.Join(root, metaDir)} {
			info, err := os.Stat(d)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)), d)
		}
		for _, p := range files(root) {
			info, err := os.Stat(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()&0o077).To(BeZero(), p)
		}
		staged := f.stagedFiles()
		Expect(staged).To(HaveLen(1))
		Expect(filepath.Base(staged[0])).To(Equal(art.ID))
	})

	It("never opens an output for a request that names a tool no source declares", Label("tools"), func() {
		f := newFixture()
		f.rt.text("output-t", "a,b\n")
		r := f.request("tool-op")
		r.Arguments = json.RawMessage(`{"tool":"wipe"}`)
		art, err := f.arts.Ingest(context.Background(), f.alice, r, out("output-t", "x.csv", "text/csv", 4))
		Expect(err == nil && art.State == gateway.ArtifactComplete).To(BeFalse())
		Expect(f.rt.opened("output-t")).To(Equal(0))
	})

	It("denies an output request whose arguments repeat a key and never opens the output", Label("tools"), func() {
		f := newFixture()
		f.inst.tools = []string{"export"}
		f.rt.text("output-d", "a,b\n")
		for i, args := range []string{`{"tool":"export","tool":"wipe"}`, `{"tool":"wipe","tool":"export"}`, `{"tool":"export","\u0074ool":"wipe"}`, `{"tool":"export","rows":[{"a":1,"a":2}]}`} {
			r := f.request("dup-op-" + string(rune('a'+i)))
			r.Arguments = json.RawMessage(args)
			_, err := f.arts.Ingest(context.Background(), f.alice, r, out("output-d", "x.csv", "text/csv", 4))
			Expect(err).To(MatchError(gateway.ErrForbidden), args)
		}
		Expect(f.rt.opened("output-d")).To(Equal(0))
		r := f.request("ok-op")
		r.Arguments = json.RawMessage(`{"tool":"export"}`)
		art, err := f.arts.Ingest(context.Background(), f.alice, r, out("output-d", "x.csv", "text/csv", 4))
		Expect(err).NotTo(HaveOccurred())
		Expect(art.State).To(Equal(gateway.ArtifactComplete))
	})

	It("reports an unavailable declaration source as unavailable and never opens the output", Label("tools"), func() {
		f := newFixture()
		f.rt.text("output-u", "a,b\n")
		for i, cause := range []error{addons.ErrNotOwner, addons.ErrRecoveryRequired, context.DeadlineExceeded, errors.New("manifest unavailable")} {
			f.inst.toolErr = cause
			r := f.request("un-op-" + string(rune('a'+i)))
			r.Arguments = json.RawMessage(`{"tool":"export"}`)
			_, err := f.arts.Ingest(context.Background(), f.alice, r, out("output-u", "x.csv", "text/csv", 4))
			Expect(err).To(HaveOccurred(), cause.Error())
			Expect(errors.Is(err, gateway.ErrForbidden)).To(BeFalse(), cause.Error())
			Expect(gateway.StatusFor(err)).To(Equal(http.StatusServiceUnavailable), cause.Error())
		}
		Expect(f.rt.opened("output-u")).To(Equal(0))
	})

	It("never uses an addon handle, URL or path as an identifier or a file path", func() {
		f := newFixture()
		for _, handle := range []string{"https://example.test/export.csv", "../../etc/passwd", "a/b", "/abs/path", "UPPER", "file:///etc/passwd", "", strings.Repeat("a", 65), "with space"} {
			f.rt.text(handle, "secret")
			_, err := f.arts.Ingest(context.Background(), f.alice, f.request("op-"+fmt.Sprint(len(handle))), out(handle, "x.csv", "text/csv", 6))
			Expect(err).To(HaveOccurred(), "handle %q", handle)
			Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue(), "handle %q", handle)
		}
		Expect(f.rt.totalOpens()).To(BeZero())
		Expect(f.stagedFiles()).To(BeEmpty())
		// The name is a label: the file lands under the host identifier.
		f.rt.text("output", "x")
		for _, name := range []string{"../../../etc/cron.d/evil", "a/b.csv", `a\b.csv`, "/etc/passwd"} {
			_, err := f.arts.Ingest(context.Background(), f.alice, f.request("op-name"), out("output", name, "text/csv", 1))
			Expect(err).To(HaveOccurred(), name)
		}
		Expect(f.rt.totalOpens()).To(BeZero())
		art, err := f.arts.Ingest(context.Background(), f.alice, f.request("op-name"), out("output", ".hidden.csv", "text/csv", 1))
		Expect(err).NotTo(HaveOccurred())
		Expect(art.Name).To(Equal("hidden.csv"))
		Expect(f.stagedFiles()).To(HaveLen(1))
		Expect(filepath.Base(f.stagedFiles()[0])).To(Equal(art.ID))
		// An identifier from a client is looked up as an identifier, never as a path.
		for _, id := range []string{"../meta/artifacts.json", art.ID + "/../" + art.ID, "output", "./" + art.ID, strings.ToUpper(art.ID), art.ID + "\x00"} {
			_, err := f.download(f.alice, id)
			Expect(err).To(MatchError(ErrNotFound), id)
			_, err = f.arts.Status(context.Background(), f.alice, id)
			Expect(err).To(MatchError(ErrNotFound), id)
		}
		// A resource that is not the request's, or another addon's, is refused.
		bad := f.request("op-forged")
		bad.AddonID = "other"
		_, err = f.arts.Ingest(context.Background(), f.alice, bad, out("output", "x.csv", "text/csv", 1))
		Expect(err).To(HaveOccurred())
		_, err = f.arts.IngestSources(context.Background(), f.alice, f.request("op-src"), []policy.ResourceKey{{AddonID: "other", ResourceID: f.key.ResourceID}}, out("output", "x.csv", "text/csv", 1))
		Expect(err).To(MatchError(gateway.ErrForbidden))
	})

	It("answers one denial for an unknown identifier and for another principal's artifact", func() {
		f := newFixture()
		art := f.ingest(f.alice, "export-1", "output-1", "private")
		// Bob can read the source resource, which is not enough: the artifact
		// belongs to the principal that asked.
		_, errOther := f.download(f.bob, art.ID)
		_, errUnknown := f.download(f.bob, "a00000000000000000000000000000000")
		Expect(errOther).To(MatchError(ErrNotFound))
		Expect(errUnknown).To(MatchError(ErrNotFound))
		Expect(errOther.Error()).To(Equal(errUnknown.Error()))
		_, errStatus := f.arts.Status(context.Background(), f.bob, art.ID)
		Expect(errStatus).To(MatchError(ErrNotFound))
		Expect(gateway.StatusFor(errOther)).To(Equal(403))
		// The same user under another ownership mode is not the principal.
		wrongMode := f.alice
		wrongMode.Mode = "local-owner"
		_, err := f.download(wrongMode, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
		// An empty actor and a foreign credential of the same user are handled
		// by the account check of the policy; the empty one never matches.
		_, err = f.download(contract.Actor{}, art.ID)
		Expect(err).To(HaveOccurred())
		other := f.alice
		other.CredentialID = "session:alice"
		body, err := f.download(other, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("private"))
	})

	It("never serves incomplete, short, long or failed transfers and removes their partial bytes", func() {
		f := newFixture()
		cases := map[string]struct {
			open     func(context.Context) (io.ReadCloser, error)
			declared int
		}{
			"short": {declared: 8, open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("abc")), nil
			}},
			"long": {declared: 4, open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("abcdefgh")), nil
			}},
			"one byte long": {declared: 4, open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("abcde")), nil
			}},
			"read error": {declared: 8, open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(io.MultiReader(strings.NewReader("abcd"), &errReader{errors.New("connection reset")})), nil
			}},
			"open error": {declared: 4, open: func(context.Context) (io.ReadCloser, error) { return nil, errors.New("endpoint gone") }},
		}
		i := 0
		for name, c := range cases {
			i++
			handle := fmt.Sprintf("output-%d", i)
			f.rt.set(handle, c.open)
			_, err := f.arts.Ingest(context.Background(), f.alice, f.request(fmt.Sprintf("op-%d", i)), out(handle, "r.csv", "text/csv", c.declared))
			Expect(err).To(HaveOccurred(), name)
			Expect(f.stagedFiles()).To(BeEmpty(), name)
		}
		// Every failed attempt is visible to its owner as failed, with a fixed
		// reason, and none of them can be downloaded.
		for _, r := range f.arts.meta.all() {
			Expect(r.State).To(Equal(gateway.ArtifactFailed))
			Expect(r.Reason).NotTo(BeEmpty())
			Expect(r.Reason).NotTo(ContainSubstring("connection reset"))
			Expect(r.Reason).NotTo(ContainSubstring("endpoint gone"))
			_, err := f.download(f.alice, r.ID)
			Expect(err).To(MatchError(ErrNotReady))
		}
		Expect(f.arts.meta.all()).To(HaveLen(len(cases)))
		// A transfer that is still running is not downloadable either.
		b := newBlocking("ab", "cd")
		f.rt.set("slow", func(context.Context) (io.ReadCloser, error) { return b, nil })
		started, err := f.arts.Start(context.Background(), f.alice, f.request("op-slow"), out("slow", "r.csv", "text/csv", 4))
		Expect(err).NotTo(HaveOccurred())
		Expect(started.State).To(Equal(gateway.ArtifactTransferring))
		Eventually(b.started).Should(BeClosed())
		_, err = f.download(f.alice, started.ID)
		Expect(err).To(MatchError(ErrNotReady))
		close(b.release)
		done := f.settled(f.alice, started.ID)
		Expect(done.State).To(Equal(gateway.ArtifactComplete))
		body, err := f.download(f.alice, started.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("abcd"))
	})

	It("streams ingestion and download without buffering the whole file", func() {
		const size = 48 << 20
		f := newFixture(func(l *Limits) { l.MaxBytes = 64 << 20; l.UserBytes = 64 << 20; l.TotalBytes = 64 << 20 })
		staging := filepath.Join(f.dir, "artifacts", stagingDir)
		var produced int64
		var lag int64
		var peak uint64
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		base := ms.HeapAlloc
		f.rt.set("big", func(context.Context) (io.ReadCloser, error) {
			return newReader(readerFunc(func(p []byte) (int, error) {
				if produced >= size {
					return 0, io.EOF
				}
				// Before more bytes are handed over, the bytes already handed
				// over must be on disk: the staged part is no further behind
				// than one copy buffer.
				for _, p := range files(staging) {
					if info, err := os.Stat(p); err == nil && produced-info.Size() > lag {
						lag = produced - info.Size()
					}
				}
				n := min(int64(len(p)), size-produced)
				for i := int64(0); i < n; i++ {
					p[i] = byte((produced + i) % 251)
				}
				produced += n
				if produced%(4<<20) < n {
					runtime.ReadMemStats(&ms)
					if ms.HeapAlloc > peak {
						peak = ms.HeapAlloc
					}
				}
				return int(n), nil
			})), nil
		})
		art, err := f.arts.Ingest(context.Background(), f.alice, f.request("big-op"), out("big", "big.bin", "application/octet-stream", size))
		Expect(err).NotTo(HaveOccurred())
		Expect(art.Size).To(Equal(int64(size)))
		Expect(lag).To(BeNumerically("<=", copyBufferSize))
		Expect(int64(peak)-int64(base)).To(BeNumerically("<", 16<<20), "ingestion held much of the file in memory")
		var total, biggest int64
		var sum int
		peak = 0
		dst := writerFunc(func(p []byte) (int, error) {
			total += int64(len(p))
			biggest = max(biggest, int64(len(p)))
			for _, c := range p[:min(len(p), 64)] {
				sum += int(c)
			}
			if total%(4<<20) < int64(len(p)) {
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > peak {
					peak = ms.HeapAlloc
				}
			}
			return len(p), nil
		})
		_, err = f.arts.Download(context.Background(), f.alice, art.ID, dst)
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(Equal(int64(size)))
		Expect(biggest).To(BeNumerically("<=", copyBufferSize))
		Expect(int64(peak)-int64(base)).To(BeNumerically("<", 16<<20), "download held much of the file in memory")
		Expect(sum).NotTo(BeZero())
	})

	It("rejects an output declared larger than one artifact may be before reading it", func() {
		f := newFixture()
		f.rt.text("huge", "x")
		_, err := f.arts.Ingest(context.Background(), f.alice, f.request("op"), out("huge", "h.bin", "application/octet-stream", int(f.limits.MaxBytes)+1))
		Expect(err).To(MatchError(ErrTooLarge))
		Expect(f.rt.totalOpens()).To(BeZero())
		// A negative size is not a size.
		_, err = f.arts.Ingest(context.Background(), f.alice, f.request("op2"), contract.OutputDescription{Handle: "huge", Name: "h", MediaType: "text/csv", Size: -1})
		Expect(err).To(HaveOccurred())
		Expect(f.arts.meta.all()).To(BeEmpty())
	})
})

type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

type readerFunc func([]byte) (int, error)

func (r readerFunc) Read(p []byte) (int, error) { return r(p) }

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// resource registers a second resource owned by owner.
func (f *fixture) resource(owner contract.Actor, grant ...string) policy.ResourceKey {
	key, err := f.policy.Register(context.Background(), owner, "sample")
	Expect(err).NotTo(HaveOccurred())
	Expect(f.policy.Commit(context.Background(), owner, key)).To(Succeed())
	for _, u := range grant {
		Expect(f.policy.SetGrant(context.Background(), owner, key, u, policy.RoleViewer)).To(Succeed())
	}
	return key
}

// blockedStart starts a transfer whose output blocks after its first chunk.
func (f *fixture) blockedStart(who contract.Actor, op, handle string, size int) (Artifact, *blocking, error) {
	b := newBlocking(strings.Repeat("x", size/2), strings.Repeat("y", size-size/2))
	f.rt.set(handle, func(context.Context) (io.ReadCloser, error) { return b, nil })
	art, err := f.arts.Start(context.Background(), who, f.request(op), out(handle, "r.csv", "text/csv", size))
	return art, b, err
}

var _ = Describe("artifact quotas and bounds", func() {
	It("enforces per-user, total and size limits atomically under concurrent ingestion", func() {
		accepted := func(f *fixture, who func(int) contract.Actor, n int) (ok int, errs []error, blockers []*blocking) {
			var mu sync.Mutex
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, b, err := f.blockedStart(who(i), fmt.Sprintf("op-%d", i), fmt.Sprintf("h%d", i), 100)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						errs = append(errs, err)
						return
					}
					ok++
					blockers = append(blockers, b)
				}(i)
			}
			wg.Wait()
			return
		}
		release := func(bs []*blocking) {
			for _, b := range bs {
				close(b.release)
			}
		}
		// One principal: 100 byte artifacts against a 300 byte user quota.
		f := newFixture(func(l *Limits) {
			l.MaxBytes = 100
			l.UserBytes = 300
			l.TotalBytes = 100000
			l.Transfers = 32
			l.UserTransfers = 32
		})
		ok, errs, bs := accepted(f, func(int) contract.Actor { return f.alice }, 16)
		Expect(ok).To(Equal(3))
		Expect(errs).To(HaveLen(13))
		for _, err := range errs {
			Expect(err).To(MatchError(ErrQuota))
		}
		release(bs)
		var reserved int64
		for _, r := range f.arts.meta.all() {
			reserved += r.Size
		}
		Expect(reserved).To(BeNumerically("<=", 300))
		// Two principals: the total bound holds across them.
		g := newFixture(func(l *Limits) {
			l.MaxBytes = 100
			l.UserBytes = 200
			l.TotalBytes = 250
			l.Transfers = 32
			l.UserTransfers = 32
		})
		ok, errs, bs = accepted(g, func(i int) contract.Actor {
			if i%2 == 0 {
				return g.alice
			}
			return g.bob
		}, 12)
		Expect(ok).To(Equal(2))
		for _, err := range errs {
			Expect(err).To(MatchError(ErrQuota))
		}
		release(bs)
		// Record count per user.
		h := newFixture(func(l *Limits) { l.UserCount = 2; l.Transfers = 32; l.UserTransfers = 32 })
		ok, errs, bs = accepted(h, func(int) contract.Actor { return h.alice }, 9)
		Expect(ok).To(Equal(2))
		Expect(errs).To(HaveLen(7))
		release(bs)
	})

	It("bounds concurrent transfers and concurrent downloads", func() {
		f := newFixture(func(l *Limits) { l.Transfers = 3; l.UserTransfers = 2; l.Downloads = 1 })
		_, b1, err := f.blockedStart(f.alice, "op-1", "h1", 10)
		Expect(err).NotTo(HaveOccurred())
		_, b2, err := f.blockedStart(f.alice, "op-2", "h2", 10)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = f.blockedStart(f.alice, "op-3", "h3", 10)
		Expect(err).To(MatchError(ErrBusy))
		Expect(gateway.StatusFor(err)).To(Equal(503))
		// Another principal still has a slot, and the global bound then holds.
		_, b4, err := f.blockedStart(f.bob, "op-4", "h4", 10)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = f.blockedStart(f.bob, "op-5", "h5", 10)
		Expect(err).To(MatchError(ErrBusy))
		for _, b := range []*blocking{b1, b2, b4} {
			close(b.release)
		}
		for _, r := range f.arts.meta.all() {
			f.settled(actor(r.Principal), r.ID)
		}
		// A refused start leaves no record and no file.
		Expect(f.arts.meta.all()).To(HaveLen(3))
		// Downloads: one stream at a time here.
		art := f.ingest(f.alice, "op-6", "h6", strings.Repeat("z", 100<<10))
		entered, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			var once sync.Once
			_, err := f.arts.Download(context.Background(), f.alice, art.ID, writerFunc(func(p []byte) (int, error) {
				once.Do(func() { close(entered); <-release })
				return len(p), nil
			}))
			done <- err
		}()
		<-entered
		_, err = f.download(f.alice, art.ID)
		Expect(err).To(MatchError(ErrBusy))
		close(release)
		Expect(<-done).NotTo(HaveOccurred())
		body, err := f.download(f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(HaveLen(100 << 10))
	})

	It("cancels a transfer by its context or by its owner and removes the partial bytes", func() {
		f := newFixture()
		// The caller's context.
		b := newBlocking("ab", "cdef")
		f.rt.set("slow", func(context.Context) (io.ReadCloser, error) { return b, nil })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := f.arts.Ingest(ctx, f.alice, f.request("op-ctx"), out("slow", "r.csv", "text/csv", 6))
			done <- err
		}()
		<-b.started
		Expect(f.stagedFiles()).NotTo(BeEmpty())
		cancel()
		Expect(<-done).To(HaveOccurred())
		<-b.closed
		Expect(f.stagedFiles()).To(BeEmpty())
		recs := f.arts.meta.all()
		Expect(recs).To(HaveLen(1))
		Expect(recs[0].State).To(Equal(gateway.ArtifactCancelled))
		// The owner of the artifact.
		art, b2, err := f.blockedStart(f.alice, "op-own", "h2", 10)
		Expect(err).NotTo(HaveOccurred())
		<-b2.started
		_, err = f.arts.Cancel(context.Background(), f.bob, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
		cur, err := f.arts.Status(context.Background(), f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(cur.State).To(Equal(gateway.ArtifactTransferring))
		got, err := f.arts.Cancel(context.Background(), f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ArtifactCancelled))
		<-b2.closed
		Expect(f.stagedFiles()).To(BeEmpty())
		_, err = f.download(f.alice, art.ID)
		Expect(err).To(MatchError(ErrNotReady))
		// A complete artifact is not touched by a cancel.
		done2 := f.ingest(f.alice, "op-done", "h3", "kept")
		again, err := f.arts.Cancel(context.Background(), f.alice, done2.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(gateway.ArtifactComplete))
		body, err := f.download(f.alice, done2.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("kept"))
	})
})

var _ = Describe("artifact expiry", func() {
	It("never serves an artifact at or after expiry, including during admission and mid-stream", func() {
		f := newFixture()
		art := f.ingest(f.alice, "op-1", "h1", "short lived")
		// One tick before expiry it is served; at expiry it is not.
		f.clock.advance(f.limits.Expiry - time.Second)
		body, err := f.download(f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("short lived"))
		f.clock.advance(time.Second)
		_, err = f.download(f.alice, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
		_, err = f.arts.Status(context.Background(), f.alice, art.ID)
		Expect(err).To(MatchError(ErrNotFound))

		// Expiry that happens while the download is being admitted.
		g := f
		fresh := g.ingest(g.alice, "op-2", "h2", "second")
		var armed atomic.Bool
		armed.Store(true)
		g.accounts.setHook(func() {
			if armed.CompareAndSwap(true, false) {
				g.clock.advance(g.limits.Expiry)
			}
		})
		var b bytes.Buffer
		_, err = g.arts.Download(context.Background(), g.alice, fresh.ID, &b)
		Expect(err).To(HaveOccurred())
		Expect(b.Len()).To(BeZero())
		g.accounts.setHook(nil)

		// Expiry in the middle of a stream.
		big := g.ingest(g.alice, "op-3", "h3", strings.Repeat("q", 5*copyBufferSize))
		var written int
		_, err = g.arts.Download(context.Background(), g.alice, big.ID, writerFunc(func(p []byte) (int, error) {
			written += len(p)
			g.clock.advance(g.limits.Expiry)
			return len(p), nil
		}))
		Expect(err).To(HaveOccurred())
		Expect(written).To(Equal(copyBufferSize))
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
	})

	It("fails a transfer that outlives its artifact", func() {
		f := newFixture()
		f.rt.set("late", func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(readerFunc(func(p []byte) (int, error) {
				f.clock.advance(f.limits.Expiry)
				n := copy(p, "late")
				return n, io.EOF
			})), nil
		})
		_, err := f.arts.Ingest(context.Background(), f.alice, f.request("op-late"), out("late", "r.csv", "text/csv", 4))
		Expect(err).To(HaveOccurred())
		Expect(f.stagedFiles()).To(BeEmpty())
		recs := f.arts.meta.all()
		Expect(recs).To(HaveLen(1))
		Expect(recs[0].State).To(Equal(gateway.ArtifactFailed))
	})
})

var _ = Describe("artifact access control", func() {
	It("fails a transfer whose source is revoked before completion and denies a revoked download", func() {
		f := newFixture()
		art, b, err := f.blockedStart(f.bob, "op-bob", "h1", 10)
		Expect(err).NotTo(HaveOccurred())
		<-b.started
		ctx := f.spy.run("bob", f.key.ResourceID)
		Expect(ctx).NotTo(BeNil())
		done := make(chan error, 1)
		go func() { done <- f.policy.Revoke(context.Background(), f.alice, f.key, "bob") }()
		// The revocation returns only after the admitted transfer has ended.
		Expect(<-done).NotTo(HaveOccurred())
		Expect(ctx.Err()).To(HaveOccurred())
		failed := f.settled(f.bob, art.ID)
		Expect(failed.State).To(Equal(gateway.ArtifactFailed))
		Expect(failed.Reason).To(Equal("access denied"))
		Expect(f.stagedFiles()).To(BeEmpty())
		_, err = f.download(f.bob, art.ID)
		Expect(err).To(MatchError(ErrNotReady))

		// A complete artifact: revoked access denies the download, and a new
		// grant brings it back, because the bytes live until expiry.
		Expect(f.policy.SetGrant(context.Background(), f.alice, f.key, "bob", policy.RoleViewer)).To(Succeed())
		complete := f.ingest(f.bob, "op-bob-2", "h2", "bob's export")
		Expect(f.policy.Revoke(context.Background(), f.alice, f.key, "bob")).To(Succeed())
		var got bytes.Buffer
		_, err = f.arts.Download(context.Background(), f.bob, complete.ID, &got)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(got.Len()).To(BeZero())
		Expect(f.stagedFiles()).To(HaveLen(1))
		Expect(f.policy.SetGrant(context.Background(), f.alice, f.key, "bob", policy.RoleViewer)).To(Succeed())
		body, err := f.download(f.bob, complete.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("bob's export"))
		// A completion that races a loss of access never publishes: the last
		// byte arrives, then the account is disabled, then completion checks.
		f.rt.set("h3", func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(readerFunc(func(p []byte) (int, error) {
				f.accounts.set("bob", gateway.ErrForbidden)
				return copy(p, "late"), io.EOF
			})), nil
		})
		_, err = f.arts.Ingest(context.Background(), f.bob, f.request("op-bob-3"), out("h3", "r.csv", "text/csv", 4))
		Expect(err).To(MatchError(gateway.ErrForbidden))
		f.accounts.set("bob", nil)
		Expect(f.stagedFiles()).To(HaveLen(1)) // only the earlier complete artifact
		for _, r := range f.arts.meta.all() {
			if r.OperationID == "op-bob-3" {
				Expect(r.State).To(Equal(gateway.ArtifactFailed))
				_, err = f.download(f.bob, r.ID)
				Expect(err).To(MatchError(ErrNotReady))
			}
		}
	})

	It("stops a download stream when the source is revoked mid-stream", func() {
		f := newFixture()
		art := f.ingest(f.bob, "op-1", "h1", strings.Repeat("r", 16*copyBufferSize))
		entered, proceed := make(chan struct{}), make(chan struct{})
		var written int
		done := make(chan error, 1)
		go func() {
			var once sync.Once
			_, err := f.arts.Download(context.Background(), f.bob, art.ID, writerFunc(func(p []byte) (int, error) {
				written += len(p)
				once.Do(func() { close(entered); <-proceed })
				return len(p), nil
			}))
			done <- err
		}()
		<-entered
		revoked := make(chan error, 1)
		go func() { revoked <- f.policy.Revoke(context.Background(), f.alice, f.key, "bob") }()
		run := f.spy.run("bob", f.key.ResourceID)
		Expect(run).NotTo(BeNil())
		// Wait for the revocation to cancel the admitted stream, then let the
		// writer go: no further byte is written.
		<-run.Done()
		close(proceed)
		err := <-done
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(written).To(Equal(copyBufferSize))
		Expect(<-revoked).NotTo(HaveOccurred())
	})

	It("requires read access to every source resource at completion and download", func() {
		f := newFixture()
		second := f.resource(f.bob, "alice")
		sources := []policy.ResourceKey{f.key, second}
		f.rt.text("h1", "joined export")
		art, err := f.arts.IngestSources(context.Background(), f.alice, f.request("op-join"), sources, out("h1", "r.csv", "text/csv", 13))
		Expect(err).NotTo(HaveOccurred())
		Expect(art.Sources).To(HaveLen(2))
		body, err := f.download(f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("joined export"))
		// The principal still reads the first resource, but loses the second.
		Expect(f.policy.Revoke(context.Background(), f.bob, second, "alice")).To(Succeed())
		Expect(f.policy.Authorize(context.Background(), f.alice, f.key, policy.ActionRead)).To(Succeed())
		var got bytes.Buffer
		_, err = f.arts.Download(context.Background(), f.alice, art.ID, &got)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(got.Len()).To(BeZero())
		// A principal that cannot read one of the sources gets nothing
		// reserved, whichever source it is.
		before := len(f.arts.meta.all())
		_, err = f.arts.IngestSources(context.Background(), f.alice, f.request("op-join-2"), sources, out("h1", "r.csv", "text/csv", 13))
		Expect(err).To(MatchError(gateway.ErrForbidden))
		_, err = f.arts.StartSources(context.Background(), f.carol, f.request("op-join-3"), []policy.ResourceKey{f.key}, out("h1", "r.csv", "text/csv", 13))
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(f.arts.meta.all()).To(HaveLen(before))
	})

	It("denies a disabled or unavailable account at ingestion and download", func() {
		f := newFixture()
		art := f.ingest(f.alice, "op-1", "h1", "mine")
		f.accounts.set("alice", gateway.ErrForbidden)
		_, err := f.download(f.alice, art.ID)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		f.rt.text("h2", "x")
		_, err = f.arts.Ingest(context.Background(), f.alice, f.request("op-2"), out("h2", "r.csv", "text/csv", 1))
		Expect(err).To(MatchError(gateway.ErrForbidden))
		// An account store that cannot answer is an outage, not a denial.
		f.accounts.set("alice", errors.New("database is locked"))
		_, err = f.download(f.alice, art.ID)
		Expect(err).To(HaveOccurred())
		Expect(gateway.StatusFor(err)).To(Equal(503))
		f.accounts.set("alice", nil)
		body, err := f.download(f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("mine"))
	})
})

var _ = Describe("addon lifecycle and artifacts", func() {
	It("denies downloads of a disabled addon, keeps the bytes and serves again only while unexpired with current permissions", func() {
		f := newFixture()
		art := f.ingest(f.bob, "op-1", "h1", "kept while disabled")
		f.addon.set(func(s *AddonState) { s.Enabled = false })
		var got bytes.Buffer
		_, err := f.arts.Download(context.Background(), f.bob, art.ID, &got)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(got.Len()).To(BeZero())
		// The bytes and the record stay until normal expiry, even through a sweep.
		swept, err := f.arts.Sweep(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(swept).To(Equal(Swept{}))
		Expect(f.stagedFiles()).To(HaveLen(1))
		// A disabled addon accepts no new file result either.
		f.rt.text("h2", "x")
		_, err = f.arts.Ingest(context.Background(), f.bob, f.request("op-2"), out("h2", "r.csv", "text/csv", 1))
		Expect(err).To(MatchError(gateway.ErrForbidden))
		// Enabling serves it again.
		f.addon.set(func(s *AddonState) { s.Enabled = true })
		body, err := f.download(f.bob, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("kept while disabled"))
		// Enabling does not restore a permission that was lost meanwhile.
		f.addon.set(func(s *AddonState) { s.Enabled = false })
		Expect(f.policy.Revoke(context.Background(), f.alice, f.key, "bob")).To(Succeed())
		f.addon.set(func(s *AddonState) { s.Enabled = true })
		_, err = f.download(f.bob, art.ID)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		// Nor an artifact that expired while the addon was off.
		Expect(f.policy.SetGrant(context.Background(), f.alice, f.key, "bob", policy.RoleViewer)).To(Succeed())
		f.addon.set(func(s *AddonState) { s.Enabled = false })
		f.clock.advance(f.limits.Expiry)
		f.addon.set(func(s *AddonState) { s.Enabled = true })
		_, err = f.download(f.bob, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
		// A new generation of the addon does not serve an older build's file.
		f2 := newFixture()
		old := f2.ingest(f2.alice, "op-g", "h1", "old build")
		f2.addon.set(func(s *AddonState) { s.Generation = 2 })
		_, err = f2.download(f2.alice, old.ID)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		// A manager that cannot answer is unavailable, never an enabled addon.
		f2.addon.set(func(s *AddonState) { s.Generation = 1 })
		f2.addon.mu.Lock()
		f2.addon.err = errors.New("manager busy")
		f2.addon.mu.Unlock()
		_, err = f2.download(f2.alice, old.ID)
		Expect(gateway.StatusFor(err)).To(Equal(503))
	})

	It("denies downloads after uninstall and removes the bytes at the next sweep", func() {
		f := newFixture()
		art := f.ingest(f.alice, "op-1", "h1", "uninstalled soon")
		f.addon.set(func(s *AddonState) { s.Installed, s.Enabled = false, false })
		_, err := f.download(f.alice, art.ID)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(f.stagedFiles()).To(HaveLen(1))
		swept, err := f.arts.Sweep(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(swept.Uninstalled).To(Equal(1))
		Expect(f.stagedFiles()).To(BeEmpty())
		Expect(f.arts.meta.all()).To(BeEmpty())
		// A reinstalled addon does not bring the file back.
		f.addon.set(func(s *AddonState) { s.Installed, s.Enabled = true, true })
		_, err = f.download(f.alice, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
	})

	It("sweeps expired artifacts, abandoned partials and orphan files without touching live transfers", func() {
		f := newFixture()
		old := f.ingest(f.alice, "op-old", "h-old", "old")
		failed := newBlocking("a", "b")
		f.rt.set("h-fail", func(context.Context) (io.ReadCloser, error) { return failed, nil })
		failedArt, err := f.arts.Start(context.Background(), f.alice, f.request("op-fail"), out("h-fail", "r.csv", "text/csv", 5))
		Expect(err).NotTo(HaveOccurred())
		<-failed.started
		Expect(f.arts.Cancel(context.Background(), f.alice, failedArt.ID)).NotTo(BeNil())
		f.clock.advance(30 * time.Minute)
		young := f.ingest(f.alice, "op-young", "h-young", "young")
		f.clock.advance(40 * time.Minute) // old is 70 minutes, young 40
		// A transfer that is running now.
		live, lb, err := f.blockedStart(f.alice, "op-live", "h-live", 10)
		Expect(err).NotTo(HaveOccurred())
		<-lb.started
		// A transfer no worker owns, as a crash would leave it, and files no
		// record names.
		ghost := "a" + strings.Repeat("1", 32)
		Expect(f.arts.meta.mutate(func(d *document) error {
			d.Artifacts[ghost] = &record{ID: ghost, Principal: "alice", Mode: "named-user", Sources: []gateway.ArtifactResource{{AddonID: "sample", ResourceID: f.key.ResourceID}},
				AddonID: "sample", Generation: 1, OperationID: "op-ghost", Binding: strings.Repeat("0", 64), Name: "g.csv", MediaType: "text/csv", Size: 3,
				State: gateway.ArtifactTransferring, CreatedAt: f.clock.now(), ExpiresAt: f.clock.now().Add(time.Hour)}
			return nil
		})).To(Succeed())
		root := filepath.Join(f.dir, "artifacts")
		orphanBlob := filepath.Join(root, blobDir, "a"+strings.Repeat("2", 32))
		orphanPart := filepath.Join(root, stagingDir, "a"+strings.Repeat("3", 32)+".part")
		ghostPart := filepath.Join(root, stagingDir, ghost+".part")
		junk := filepath.Join(root, blobDir, "notes.txt")
		junkPart := filepath.Join(root, stagingDir, "readme.part")
		for _, p := range []string{orphanBlob, orphanPart, ghostPart, junk, junkPart} {
			Expect(os.WriteFile(p, []byte("xx"), 0o600)).To(Succeed())
		}
		swept, err := f.arts.Sweep(context.Background())
		Expect(err).NotTo(HaveOccurred())
		// The finished artifact and the cancelled record are both past expiry.
		Expect(swept.Expired).To(Equal(2))
		Expect(swept.Abandoned).To(Equal(1))
		// The ghost's partial is removed with its record; the rest are orphans.
		Expect(swept.Orphans).To(BeNumerically(">=", 4))
		for _, p := range []string{orphanBlob, orphanPart, ghostPart, junk, junkPart} {
			Expect(p).NotTo(BeAnExistingFile())
		}
		_, err = f.download(f.alice, old.ID)
		Expect(err).To(MatchError(ErrNotFound))
		body, err := f.download(f.alice, young.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("young"))
		gone, err := f.arts.Status(context.Background(), f.alice, ghost)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone.State).To(Equal(gateway.ArtifactFailed))
		// The live transfer was not touched and still completes.
		cur, err := f.arts.Status(context.Background(), f.alice, live.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(cur.State).To(Equal(gateway.ArtifactTransferring))
		close(lb.release)
		Expect(f.settled(f.alice, live.ID).State).To(Equal(gateway.ArtifactComplete))
		// The loop sweeps on its tick and stops with its context.
		tick := make(chan time.Time)
		g := newFixtureTick(tick)
		g.ingest(g.alice, "op-1", "h1", "x")
		g.clock.advance(g.limits.Expiry)
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() { g.arts.Run(ctx); close(stopped) }()
		tick <- g.clock.now()
		tick <- g.clock.now() // a second tick is only accepted after the first sweep ended
		cancel()
		<-stopped
		Expect(g.arts.meta.all()).To(BeEmpty())
		Expect(g.stagedFiles()).To(BeEmpty())
	})
})

func newFixtureTick(tick <-chan time.Time) *fixture {
	f := newFixture()
	// Reopen the artifact store of the fixture with the injected tick.
	Expect(f.arts.Close()).To(Succeed())
	var err error
	f.arts, err = Open(Options{Dir: filepath.Join(f.dir, "artifacts"), Policy: f.spy, Runtime: f.inst, Addon: f.addon.get, Limits: f.limits, Now: f.clock.now, Tick: tick})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = f.arts.Close() })
	return f
}

var _ = Describe("artifact identity and persistence", func() {
	It("returns the same artifact for a repeated request without a second transfer", func() {
		f := newFixture()
		first := f.ingest(f.alice, "op-1", "h1", "once")
		second, err := f.arts.Ingest(context.Background(), f.alice, f.request("op-1"), out("h1", "r.csv", "text/csv", 4))
		Expect(err).NotTo(HaveOccurred())
		Expect(second.ID).To(Equal(first.ID))
		Expect(f.rt.opened("h1")).To(Equal(1))
		Expect(f.arts.meta.all()).To(HaveLen(1))
		// A repeated start of a running transfer finds the running artifact.
		art, b, err := f.blockedStart(f.alice, "op-2", "h2", 10)
		Expect(err).NotTo(HaveOccurred())
		again, err := f.arts.Start(context.Background(), f.alice, f.request("op-2"), out("h2", "r.csv", "text/csv", 10))
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ID).To(Equal(art.ID))
		close(b.release)
		f.settled(f.alice, art.ID)
		Expect(f.rt.opened("h2")).To(Equal(1))
		// Another principal's identical operation and handle is its own artifact.
		f.rt.text("h1", "once")
		other, err := f.arts.Ingest(context.Background(), f.bob, f.request("op-1"), out("h1", "r.csv", "text/csv", 4))
		Expect(err).NotTo(HaveOccurred())
		Expect(other.ID).NotTo(Equal(first.ID))
		Expect(other.Principal).To(Equal("bob"))
		// A failed attempt is replaced by the retry, not stacked beside it.
		attempts := 0
		f.rt.set("h3", func(context.Context) (io.ReadCloser, error) {
			attempts++
			if attempts == 1 {
				return io.NopCloser(strings.NewReader("ab")), nil
			}
			return io.NopCloser(strings.NewReader("abcd")), nil
		})
		_, err = f.arts.Ingest(context.Background(), f.alice, f.request("op-3"), out("h3", "r.csv", "text/csv", 4))
		Expect(err).To(HaveOccurred())
		retry, err := f.arts.Ingest(context.Background(), f.alice, f.request("op-3"), out("h3", "r.csv", "text/csv", 4))
		Expect(err).NotTo(HaveOccurred())
		Expect(retry.State).To(Equal(gateway.ArtifactComplete))
		count := 0
		for _, r := range f.arts.meta.all() {
			if r.OperationID == "op-3" {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})

	It("keeps addon handles operation-bound and out of metadata and views", func() {
		f := newFixture()
		handle := "private-output-handle"
		art := f.ingest(f.alice, "op-1", handle, "secret rows")
		view, err := json.Marshal(art)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(view)).NotTo(ContainSubstring(handle))
		Expect(string(view)).NotTo(ContainSubstring(f.dir))
		for _, p := range files(filepath.Join(f.dir, "artifacts")) {
			b, err := os.ReadFile(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring(handle), p)
			Expect(filepath.Base(p)).NotTo(ContainSubstring(handle))
		}
		// The host record answers for the right principal, operation and
		// handle only, and returns the artifact identifier as the handle.
		got, err := f.arts.Output(context.Background(), f.alice, f.request("op-1"), handle)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Handle).To(Equal(art.ID))
		Expect(got.Size).To(Equal(int64(11)))
		_, err = f.arts.Output(context.Background(), f.bob, f.request("op-1"), handle)
		Expect(err).To(MatchError(ErrNotFound))
		_, err = f.arts.Output(context.Background(), f.alice, f.request("op-2"), handle)
		Expect(err).To(MatchError(ErrNotFound))
		_, err = f.arts.Output(context.Background(), f.alice, f.request("op-1"), "other-handle")
		Expect(err).To(MatchError(ErrNotFound))
		// The identifier works as a download handle for the principal only.
		_, err = f.download(f.bob, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
	})

	It("keeps complete artifacts across a restart and settles interrupted transfers", func() {
		f := newFixture()
		done := f.ingest(f.alice, "op-1", "h1", "survives")
		_, b, err := f.blockedStart(f.alice, "op-2", "h2", 10)
		Expect(err).NotTo(HaveOccurred())
		<-b.started
		// A record that a crash left transferring, with its partial file.
		ghost := "a" + strings.Repeat("4", 32)
		Expect(f.arts.meta.mutate(func(d *document) error {
			d.Artifacts[ghost] = &record{ID: ghost, Principal: "alice", Mode: "named-user", Sources: []gateway.ArtifactResource{{AddonID: "sample", ResourceID: f.key.ResourceID}},
				AddonID: "sample", Generation: 1, OperationID: "op-ghost", Binding: strings.Repeat("0", 64), Name: "g.csv", MediaType: "text/csv", Size: 3,
				State: gateway.ArtifactTransferring, CreatedAt: f.clock.now(), ExpiresAt: f.clock.now().Add(time.Hour)}
			return nil
		})).To(Succeed())
		ghostPart := filepath.Join(f.dir, "artifacts", stagingDir, ghost+".part")
		Expect(os.WriteFile(ghostPart, []byte("ab"), 0o600)).To(Succeed())
		Expect(f.arts.Close()).To(Succeed())
		Expect(f.store.Close()).To(Succeed())
		// Closing the store stopped the running transfer and removed its bytes.
		Expect(f.stagedFiles()).To(HaveLen(2)) // the complete one and the ghost part
		g := openFixture(f.dir, f)
		Expect(ghostPart).NotTo(BeAnExistingFile())
		Expect(g.stagedFiles()).To(HaveLen(1))
		body, err := g.download(g.alice, done.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("survives"))
		states := map[string]gateway.ArtifactState{}
		reasons := map[string]string{}
		for _, r := range g.arts.meta.all() {
			states[r.OperationID], reasons[r.OperationID] = r.State, r.Reason
		}
		Expect(states).To(Equal(map[string]gateway.ArtifactState{"op-1": gateway.ArtifactComplete, "op-2": gateway.ArtifactFailed, "op-ghost": gateway.ArtifactFailed}))
		Expect(reasons["op-2"]).To(Equal("store closed"))
		Expect(reasons["op-ghost"]).To(Equal("interrupted by a restart"))
	})

	It("refuses a corrupt metadata store and stops writing after a failed sync", func() {
		f := newFixture()
		f.ingest(f.alice, "op-1", "h1", "x")
		Expect(f.arts.Close()).To(Succeed())
		path := filepath.Join(f.dir, "artifacts", metaDir, metaFileName)
		good, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var doc map[string]any
		Expect(json.Unmarshal(good, &doc)).To(Succeed())
		arts := doc["artifacts"].(map[string]any)
		var id string
		for k := range arts {
			id = k
		}
		mutated := func(fn func(map[string]any)) []byte {
			var c map[string]any
			Expect(json.Unmarshal(good, &c)).To(Succeed())
			fn(c["artifacts"].(map[string]any)[id].(map[string]any))
			b, err := json.Marshal(c)
			Expect(err).NotTo(HaveOccurred())
			return b
		}
		for name, bad := range map[string][]byte{
			"not json":        []byte("{"),
			"trailing":        append(append([]byte{}, good...), []byte(`{"version":1}`)...),
			"unknown field":   []byte(`{"version":1,"artifacts":{},"extra":1}`),
			"wrong version":   []byte(`{"version":2,"artifacts":{}}`),
			"unknown state":   mutated(func(r map[string]any) { r["state"] = "ready" }),
			"id mismatch":     mutated(func(r map[string]any) { r["id"] = "a" + strings.Repeat("9", 32) }),
			"negative size":   mutated(func(r map[string]any) { r["size"] = -1 }),
			"no principal":    mutated(func(r map[string]any) { r["principal"] = "" }),
			"no sources":      mutated(func(r map[string]any) { r["sources"] = []any{} }),
			"expiry too soon": mutated(func(r map[string]any) { r["expiresAt"] = "2000-01-01T00:00:00Z" }),
		} {
			Expect(os.WriteFile(path, bad, 0o600)).To(Succeed(), name)
			_, err := Open(Options{Dir: filepath.Join(f.dir, "artifacts"), Policy: f.spy, Runtime: f.inst, Addon: f.addon.get, Limits: f.limits, Now: f.clock.now})
			Expect(err).To(MatchError(ErrCorrupt), name)
			after, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(bad), name+": a corrupt store is never rewritten")
		}
		Expect(os.WriteFile(path, good, 0o600)).To(Succeed())
		// A second opener is refused while the first holds the store.
		first, err := Open(Options{Dir: filepath.Join(f.dir, "artifacts"), Policy: f.spy, Runtime: f.inst, Addon: f.addon.get, Limits: f.limits, Now: f.clock.now})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = first.Close() })
		_, err = Open(Options{Dir: filepath.Join(f.dir, "artifacts"), Policy: f.spy, Runtime: f.inst, Addon: f.addon.get, Limits: f.limits, Now: f.clock.now})
		Expect(err).To(MatchError(ErrStoreBusy))
		// A temporary file left by a crash is removed when the store opens.
		// A failed directory sync keeps the change in memory and then stops
		// every write until the store is reopened.
		realSync := syncDirectory
		DeferCleanup(func() { syncDirectory = realSync })
		syncDirectory = func(*os.File) error { return errors.New("disk detached") }
		first.limits = f.limits
		f.rt.text("h2", "y")
		g := &fixture{rt: f.rt, key: f.key, alice: f.alice, clock: f.clock}
		_, err = first.Start(context.Background(), g.alice, f.request("op-2"), out("h2", "r.csv", "text/csv", 1))
		Expect(err).To(HaveOccurred())
		syncDirectory = realSync
		_, err = first.Start(context.Background(), g.alice, f.request("op-3"), out("h2", "r.csv", "text/csv", 1))
		Expect(err).To(MatchError(gateway.ErrUnavailable))
	})

	It("waits for background transfers before Close returns and writes nothing after", func() {
		f := newFixture()
		var blockers []*blocking
		var ids []string
		for i := 0; i < 3; i++ {
			art, b, err := f.blockedStart(f.alice, fmt.Sprintf("op-%d", i), fmt.Sprintf("h%d", i), 10)
			Expect(err).NotTo(HaveOccurred())
			<-b.started
			blockers = append(blockers, b)
			ids = append(ids, art.ID)
		}
		Expect(f.arts.Close()).To(Succeed())
		// Close returned only after every worker had finished and recorded.
		for _, b := range blockers {
			Eventually(b.closed).Should(BeClosed())
		}
		meta := filepath.Join(f.dir, "artifacts", metaDir, metaFileName)
		before, err := os.ReadFile(meta)
		Expect(err).NotTo(HaveOccurred())
		for _, r := range f.arts.meta.all() {
			Expect(r.State).To(Equal(gateway.ArtifactFailed))
			Expect(r.Reason).To(Equal("store closed"))
		}
		Expect(f.stagedFiles()).To(BeEmpty())
		// The store accepts nothing and writes nothing once it is closed.
		f.rt.text("h9", "x")
		_, err = f.arts.Start(context.Background(), f.alice, f.request("op-9"), out("h9", "r.csv", "text/csv", 1))
		Expect(err).To(MatchError(ErrClosed))
		_, err = f.arts.Ingest(context.Background(), f.alice, f.request("op-9"), out("h9", "r.csv", "text/csv", 1))
		Expect(err).To(MatchError(ErrClosed))
		_, err = f.arts.Sweep(context.Background())
		Expect(err).To(MatchError(ErrClosed))
		_, err = f.download(f.alice, ids[0])
		Expect(err).To(HaveOccurred())
		Expect(f.arts.Close()).To(Succeed())
		after, err := os.ReadFile(meta)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		// Run returns at once on a closed store.
		f.arts.Run(context.Background())
	})
})

var _ = Describe("artifact safety helpers", func() {
	It("sanitizes attachment names and maps media types to a fixed allowlist", func() {
		names := map[string]string{
			"report.csv":                      "report.csv",
			"a/b/c.csv":                       "c.csv",
			`..\..\evil.exe`:                  "evil.exe",
			".bashrc":                         "bashrc",
			"...":                             "download",
			"   ":                             "download",
			"":                                "download",
			"con.txt":                         "_con.txt",
			"NUL":                             "_NUL",
			"lpt1.csv":                        "_lpt1.csv",
			"a\u202eb\u2066c.csv":             "abc.csv",
			"zero\u200bwidth\ufeff.csv":       "zerowidth.csv",
			"tab\there\r\n.csv":               "tabhere.csv",
			"q\"uote;.csv":                    "q_uote_.csv",
			"a:b<c>.csv":                      "a_b_c_.csv",
			"résumé.csv":                      "résumé.csv",
			"\x00\x01":                        "download",
			"trailing. ":                      "trailing",
			"100%.csv":                        "100_.csv",
			"line\u2028sep.csv":               "linesep.csv",
			"$(rm -rf).csv":                   "_(rm -rf).csv",
			strings.Repeat("x", 300) + ".csv": strings.Repeat("x", 96) + ".csv",
		}
		for in, want := range names {
			Expect(SafeName(in)).To(Equal(want), "input %q", in)
		}
		Expect([]rune(SafeName(strings.Repeat("é", 500)))).To(HaveLen(maxNameRunes))
		media := map[string]string{
			"text/csv":                  "text/csv",
			"text/csv; charset=utf-16":  "text/csv",
			"TEXT/CSV":                  "text/csv",
			"application/json":          "application/json",
			"text/html":                 "application/octet-stream",
			"text/html; charset=utf-8":  "application/octet-stream",
			"image/svg+xml":             "application/octet-stream",
			"application/xhtml+xml":     "application/octet-stream",
			"text/javascript":           "application/octet-stream",
			"application/javascript":    "application/octet-stream",
			"text/xml":                  "application/octet-stream",
			"application/x-msdownload":  "application/octet-stream",
			"multipart/mixed; boundary": "application/octet-stream",
			"not a media type":          "application/octet-stream",
			"":                          "application/octet-stream",
			"image/png":                 "image/png",
		}
		for in, want := range media {
			Expect(SafeMediaType(in)).To(Equal(want), "input %q", in)
		}
		// The header built from a sanitized name is one attachment line.
		for _, name := range []string{"report.csv", `a";x=y.csv`, "résumé.csv", "tab\t.csv", "\r\nSet-Cookie: x=y"} {
			h := gateway.ContentDisposition(SafeName(name))
			Expect(h).NotTo(ContainSubstring("\n"))
			Expect(h).NotTo(ContainSubstring("\r"))
			Expect(h).To(HavePrefix(`attachment; filename="`))
			Expect(strings.Count(h, `"`)).To(Equal(2), h)
		}
		Expect(gateway.ContentDisposition("résumé.csv")).To(Equal(`attachment; filename="r_sum_.csv"; filename*=UTF-8''r%C3%A9sum%C3%A9.csv`))
		// Even a name that was not sanitized cannot break out of the header.
		h := gateway.ContentDisposition("evil\"; inline; x=\r\nX: y")
		Expect(h).NotTo(ContainSubstring("\n"))
		Expect(h).To(HavePrefix(`attachment; filename="evil__ inline_ x=__X_ y"`))
		Expect(strings.Count(strings.SplitN(h, "; filename*", 2)[0], `"`)).To(Equal(2))
	})

	It("applies finite default limits and rejects partial or unbounded overrides", func() {
		f := newFixture(func(l *Limits) { *l = Limits{} })
		Expect(f.arts.Limits()).To(Equal(DefaultLimits()))
		Expect(DefaultLimits().Validate()).To(Succeed())
		d := DefaultLimits()
		Expect(d.MaxBytes).To(BeNumerically("<=", MaxArtifactBytes))
		Expect(d.Expiry).To(BeNumerically(">", 0))
		bad := map[string]func(*Limits){
			"partial":         func(l *Limits) { *l = Limits{MaxBytes: 1} },
			"no expiry":       func(l *Limits) { l.Expiry = 0 },
			"no transfers":    func(l *Limits) { l.Transfers = 0 },
			"user over total": func(l *Limits) { l.UserBytes = l.TotalBytes + 1 },
			"max over user":   func(l *Limits) { l.MaxBytes = l.UserBytes + 1 },
			"over relay bound": func(l *Limits) {
				l.MaxBytes = MaxArtifactBytes + 1
				l.UserBytes = 2 * MaxArtifactBytes
				l.TotalBytes = 4 * MaxArtifactBytes
			},
			"slow transfer":  func(l *Limits) { l.TransferTimeout = l.Expiry },
			"negative count": func(l *Limits) { l.UserCount = -1 },
		}
		for name, mutate := range bad {
			l := DefaultLimits()
			mutate(&l)
			_, err := Open(Options{Dir: GinkgoT().TempDir(), Policy: f.spy, Runtime: f.inst, Limits: l})
			Expect(err).To(HaveOccurred(), name)
		}
		_, err := Open(Options{Dir: "", Policy: f.spy, Runtime: f.inst})
		Expect(err).To(HaveOccurred())
		_, err = Open(Options{Dir: GinkgoT().TempDir(), Runtime: f.inst})
		Expect(err).To(HaveOccurred())
		_, err = Open(Options{Dir: GinkgoT().TempDir(), Policy: f.spy})
		Expect(err).To(HaveOccurred())
	})

	It("keeps staged bytes behind the object store boundary and publishes only complete writes", func() {
		root := GinkgoT().TempDir()
		s, err := openStaged(root)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		id := "a" + strings.Repeat("5", 32)
		// A write that fails leaves nothing visible.
		err = s.Put(ctx, id, io.MultiReader(strings.NewReader("abc"), &errReader{errors.New("boom")}))
		Expect(err).To(HaveOccurred())
		ok, err := s.Exists(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(files(root)).To(BeEmpty())
		// A write that is still running is not visible under the key.
		inFlight := newBlocking("ab", "cd")
		done := make(chan error, 1)
		go func() { done <- s.Put(ctx, id, inFlight) }()
		<-inFlight.started
		Eventually(func() int { return len(files(filepath.Join(root, stagingDir))) }).Should(Equal(1))
		_, err = s.Head(ctx, id)
		Expect(err).To(HaveOccurred())
		_, err = s.Get(ctx, id)
		Expect(err).To(HaveOccurred())
		close(inFlight.release)
		Expect(<-done).NotTo(HaveOccurred())
		meta, err := s.Head(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Size).To(Equal(int64(4)))
		r, err := s.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		b, _ := io.ReadAll(r)
		_ = r.Close()
		Expect(string(b)).To(Equal("abcd"))
		keys, err := s.List(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(Equal([]string{id}))
		info, err := os.Stat(filepath.Join(root, blobDir, id))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		// Keys that are not host identifiers never reach the file system.
		for _, key := range []string{"../x", "a/b", "", id + "/..", "A" + strings.Repeat("5", 32), "a" + strings.Repeat("5", 31)} {
			Expect(s.Put(ctx, key, strings.NewReader("x"))).To(MatchError(ErrNotFound), key)
			_, err = s.Get(ctx, key)
			Expect(err).To(MatchError(ErrNotFound), key)
			_, err = s.Head(ctx, key)
			Expect(err).To(MatchError(ErrNotFound), key)
			Expect(s.Delete(ctx, key)).To(MatchError(ErrNotFound), key)
		}
		// A cancelled write leaves no partial file.
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		Expect(s.Put(cancelled, "a"+strings.Repeat("6", 32), strings.NewReader("x"))).To(HaveOccurred())
		Expect(files(filepath.Join(root, stagingDir))).To(BeEmpty())
		Expect(s.Delete(ctx, id)).To(Succeed())
		Expect(s.Delete(ctx, id)).To(Succeed())
		Expect(files(root)).To(BeEmpty())
	})
})

var _ = Describe("artifact gateway adapter", func() {
	It("adopts every output of a result or none, and shows the owner only its own status", func() {
		f := newFixture(func(l *Limits) { l.UserCount = 2 })
		provider := f.arts.Gateway()
		blockers := map[string]*blocking{}
		result := contract.Result{Payload: json.RawMessage(`{"rows":2}`)}
		for _, h := range []string{"one", "two"} {
			b := newBlocking("ab", "cd")
			blockers[h] = b
			f.rt.set(h, func(context.Context) (io.ReadCloser, error) { return b, nil })
			result.Outputs = append(result.Outputs, out(h, h+".csv", "text/csv", 4))
		}
		adopted, err := provider.Adopt(context.Background(), f.alice, f.request("op-adopt"), result)
		Expect(err).NotTo(HaveOccurred())
		Expect(adopted.Payload).To(Equal(result.Payload))
		Expect(adopted.Outputs).To(HaveLen(2))
		for i, o := range adopted.Outputs {
			Expect(o.Handle).To(MatchRegexp(idShape.String()))
			Expect(o.Handle).NotTo(Equal(result.Outputs[i].Handle))
			Expect(o.Size).To(Equal(int64(4)))
			Expect(o.Name).To(Equal(result.Outputs[i].Name))
		}
		// Status is the owner's only.
		st, err := provider.Status(context.Background(), f.alice, adopted.Outputs[0].Handle)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.State).To(Equal(gateway.ArtifactTransferring))
		_, err = provider.Status(context.Background(), f.bob, adopted.Outputs[0].Handle)
		Expect(err).To(MatchError(ErrNotFound))
		for _, b := range blockers {
			<-b.started
			close(b.release)
		}
		for _, o := range adopted.Outputs {
			Expect(f.settled(f.alice, o.Handle).State).To(Equal(gateway.ArtifactComplete))
		}
		// A third output exceeds the user's record count: the request is
		// refused whole and starts nothing that stays behind.
		more := contract.Result{Outputs: []contract.OutputDescription{out("three", "three.csv", "text/csv", 4)}}
		f.rt.text("three", "abcd")
		_, err = provider.Adopt(context.Background(), f.alice, f.request("op-more"), more)
		Expect(err).To(MatchError(ErrQuota))
		// Within the limit, a result with one fitting and one refused output
		// leaves nothing started behind.
		g := newFixture(func(l *Limits) { l.UserCount = 1 })
		both := contract.Result{}
		for _, h := range []string{"p", "q"} {
			g.rt.text(h, "abcd")
			both.Outputs = append(both.Outputs, out(h, h+".csv", "text/csv", 4))
		}
		_, err = g.arts.Gateway().Adopt(context.Background(), g.alice, g.request("op-both"), both)
		Expect(err).To(MatchError(ErrQuota))
		Expect(g.arts.meta.all()).To(BeEmpty())
		Expect(g.stagedFiles()).To(BeEmpty())
		// Serve sets the headers before the first byte, from the record.
		var began Artifact
		var body bytes.Buffer
		Expect(provider.Serve(context.Background(), f.alice, adopted.Outputs[0].Handle, func(a Artifact) error { began = a; return nil }, &body)).To(Succeed())
		Expect(began.Name).To(Equal("one.csv"))
		Expect(body.String()).To(Equal("abcd"))
		// A begin that refuses stops the download before any byte.
		body.Reset()
		err = provider.Serve(context.Background(), f.alice, adopted.Outputs[0].Handle, func(Artifact) error { return gateway.ErrForbidden }, &body)
		Expect(err).To(MatchError(gateway.ErrForbidden))
		Expect(body.Len()).To(BeZero())
	})
})

// faultyStore wraps the staged store and fails reads on demand.
type faultyStore struct {
	storage.ObjectStore
	mu      sync.Mutex
	headErr error
	getErr  error
	// beforeHead runs before every Head, as a concurrent sweep would.
	beforeHead func()
}

func (s *faultyStore) set(head, get error) {
	s.mu.Lock()
	s.headErr, s.getErr = head, get
	s.mu.Unlock()
}

func (s *faultyStore) Head(ctx context.Context, key string) (*storage.ObjectMeta, error) {
	s.mu.Lock()
	err, hook := s.headErr, s.beforeHead
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	return s.ObjectStore.Head(ctx, key)
}

func (s *faultyStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	err := s.getErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.ObjectStore.Get(ctx, key)
}

// reopenWith reopens the artifact store of the fixture with another addon
// lifecycle answer.
func (f *fixture) reopenWith(addon func(context.Context, string) (AddonState, error)) {
	Expect(f.arts.Close()).To(Succeed())
	var err error
	f.arts, err = Open(Options{Dir: filepath.Join(f.dir, "artifacts"), Policy: f.spy, Runtime: f.inst, Addon: addon, Limits: f.limits, Now: f.clock.now})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = f.arts.Close() })
}

// decoy inserts a complete record of another addon whose identifier sorts
// before every issued one, so a sweep looks at it first.
func (f *fixture) decoy() {
	id := "a" + strings.Repeat("0", 32)
	Expect(f.arts.meta.mutate(func(d *document) error {
		d.Artifacts[id] = &record{ID: id, Principal: "alice", Mode: "named-user", Sources: []gateway.ArtifactResource{{AddonID: "other", ResourceID: f.key.ResourceID}},
			AddonID: "other", Generation: 1, OperationID: "op-decoy", Binding: strings.Repeat("0", 64), Name: "d.csv", MediaType: "text/csv", Size: 0,
			State: gateway.ArtifactComplete, CreatedAt: f.clock.now(), ExpiresAt: f.clock.now().Add(time.Hour)}
		return nil
	})).To(Succeed())
}

func (f *fixture) job(id string) *job {
	f.arts.mu.Lock()
	defer f.arts.mu.Unlock()
	return f.arts.jobs[id]
}

var _ = Describe("artifact bytes and metadata stay consistent", func() {
	It("keeps the bytes of a transfer that completes while a sweep is running", func() {
		f := newFixture()
		var hook atomic.Pointer[func()]
		f.reopenWith(func(ctx context.Context, addonID string) (AddonState, error) {
			if h := hook.Load(); addonID == "other" && h != nil {
				(*h)()
			}
			return f.addon.get(ctx, addonID)
		})
		f.decoy()
		art2, b2, err := f.blockedStart(f.alice, "op-live", "h-live", 10)
		Expect(err).NotTo(HaveOccurred())
		<-b2.started
		j2 := f.job(art2.ID)
		Expect(j2).NotTo(BeNil())
		// The sweep has the transferring record in its snapshot when it asks
		// about the decoy's addon. The transfer completes before it decides.
		var once sync.Once
		h := func() {
			once.Do(func() {
				close(b2.release)
				<-j2.done
			})
		}
		hook.Store(&h)
		_, err = f.arts.Sweep(context.Background())
		Expect(err).NotTo(HaveOccurred())
		cur, err := f.arts.Status(context.Background(), f.alice, art2.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(cur.State).To(Equal(gateway.ArtifactComplete))
		body, err := f.download(f.alice, art2.ID)
		Expect(err).NotTo(HaveOccurred(), "a complete artifact lost its bytes to the sweep")
		Expect(body).To(Equal("xxxxxyyyyy"))
	})

	It("never loses the bytes of a complete artifact when sweeps race with several completing transfers", func() {
		f := newFixture(func(l *Limits) { l.UserCount, l.TotalCount = 64, 64 })
		var hook atomic.Pointer[func()]
		f.reopenWith(func(ctx context.Context, addonID string) (AddonState, error) {
			if h := hook.Load(); addonID == "other" && h != nil {
				(*h)()
			}
			return f.addon.get(ctx, addonID)
		})
		f.decoy()
		for round := range 6 {
			var ids []string
			var blocked []*blocking
			var jobs []*job
			for i := range 4 {
				art, b, err := f.blockedStart(f.alice, fmt.Sprintf("op-%d-%d", round, i), fmt.Sprintf("h-%d-%d", round, i), 10)
				Expect(err).NotTo(HaveOccurred())
				<-b.started
				ids, blocked, jobs = append(ids, art.ID), append(blocked, b), append(jobs, f.job(art.ID))
			}
			var once sync.Once
			h := func() {
				once.Do(func() {
					for i := range blocked {
						close(blocked[i].release)
					}
					for _, j := range jobs {
						<-j.done
					}
				})
			}
			hook.Store(&h)
			var wg sync.WaitGroup
			for range 3 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = f.arts.Sweep(context.Background())
				}()
			}
			wg.Wait()
			hook.Store(nil)
			for _, id := range ids {
				body, err := f.download(f.alice, id)
				Expect(err).NotTo(HaveOccurred(), "round %d lost the bytes of %s", round, id)
				Expect(body).To(Equal("xxxxxyyyyy"))
			}
		}
	})

	It("answers a transient storage error as unavailable and keeps the bytes and the record", func() {
		f := newFixture()
		art := f.ingest(f.alice, "op-1", "h1", "keep me")
		fs := &faultyStore{ObjectStore: f.arts.blobs}
		f.arts.objects = fs
		for _, c := range []struct{ head, get error }{
			{head: syscall.EMFILE}, {head: syscall.EIO}, {get: syscall.EMFILE}, {get: errors.New("transient")},
		} {
			fs.set(c.head, c.get)
			_, err := f.download(f.alice, art.ID)
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(ErrCorrupt))
			Expect(gateway.StatusFor(err)).To(Equal(503))
			cur, serr := f.arts.Status(context.Background(), f.alice, art.ID)
			Expect(serr).NotTo(HaveOccurred())
			Expect(cur.State).To(Equal(gateway.ArtifactComplete))
			Expect(f.stagedFiles()).To(HaveLen(1))
		}
		fs.set(nil, nil)
		body, err := f.download(f.alice, art.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(Equal("keep me"))
	})

	It("marks an artifact corrupt only for missing bytes or a size mismatch", func() {
		f := newFixture()
		missing := f.ingest(f.alice, "op-1", "h1", "gone")
		short := f.ingest(f.alice, "op-2", "h2", "truncated")
		Expect(os.Remove(filepath.Join(f.dir, "artifacts", blobDir, missing.ID))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(f.dir, "artifacts", blobDir, short.ID), []byte("tr"), 0o600)).To(Succeed())
		for _, id := range []string{missing.ID, short.ID} {
			_, err := f.download(f.alice, id)
			Expect(err).To(MatchError(ErrCorrupt))
			cur, serr := f.arts.Status(context.Background(), f.alice, id)
			Expect(serr).NotTo(HaveOccurred())
			Expect(cur.State).To(Equal(gateway.ArtifactFailed))
		}
		Expect(f.stagedFiles()).To(BeEmpty())
	})

	It("answers a download that races with an expiry or a drop like any expired identifier", func() {
		f := newFixture()
		art := f.ingest(f.alice, "op-1", "h1", "dropped")
		_, expiredErr := f.download(f.alice, "a"+strings.Repeat("9", 32))
		Expect(expiredErr).To(MatchError(ErrNotFound))
		fs := &faultyStore{ObjectStore: f.arts.blobs}
		f.arts.objects = fs
		var once sync.Once
		// The sweep removes the record, then the bytes, between the lookup and
		// the read of the download.
		fs.beforeHead = func() {
			once.Do(func() {
				Expect(f.arts.meta.mutate(func(d *document) error { delete(d.Artifacts, art.ID); return nil })).To(Succeed())
				Expect(f.arts.blobs.Delete(context.Background(), art.ID)).To(Succeed())
			})
		}
		_, err := f.download(f.alice, art.ID)
		Expect(err).To(MatchError(ErrNotFound))
		Expect(err).NotTo(MatchError(ErrCorrupt))
		Expect(gateway.StatusFor(err)).To(Equal(403))
		Expect(err.Error()).To(Equal(expiredErr.Error()))
	})
})
