package workerregistry

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWorkerRegistry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WorkerRegistry")
}

// fakeRegister returns a sequence of canned responses/errors, one per call, and
// records how many times it was invoked. The last entry repeats once exhausted.
type fakeRegister struct {
	mu    sync.Mutex
	steps []step
	calls int
}

type step struct {
	res *RegisterResponse
	err error
}

func (f *fakeRegister) fn() RegisterFunc {
	return func(context.Context) (*RegisterResponse, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		i := f.calls
		f.calls++
		if i >= len(f.steps) {
			i = len(f.steps) - 1
		}
		return f.steps[i].res, f.steps[i].err
	}
}

func (f *fakeRegister) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ = Describe("CredentialManager", func() {
	approved := func(tunnelToken string) *RegisterResponse {
		return &RegisterResponse{ID: "node-1", Status: "healthy", TunnelToken: tunnelToken}
	}
	pending := &RegisterResponse{ID: "node-1", Status: "pending"}

	Describe("Acquire (wait through admin approval)", func() {
		It("keeps re-registering until the node is approved", func() {
			f := &fakeRegister{steps: []step{
				{res: pending},              // not approved yet
				{res: pending},              // still not approved
				{res: approved("tunnel-1")}, // approved, and handed its tunnel credential
			}}
			m := NewCredentialManager(f.fn(), true /* requireApproval */)
			m.initialBackoff = time.Millisecond
			m.maxBackoff = time.Millisecond

			res, err := m.Acquire(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(res.ID).To(Equal("node-1"))
			Expect(f.count()).To(Equal(3))
			Expect(m.TunnelToken()).To(Equal("tunnel-1"))
			Expect(m.NodeID()).To(Equal("node-1"))
		})

		It("returns immediately on the first success when approval is not required", func() {
			f := &fakeRegister{steps: []step{{res: pending}}}
			m := NewCredentialManager(f.fn(), false /* requireApproval */)

			res, err := m.Acquire(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Status).To(Equal("pending"))
			Expect(f.count()).To(Equal(1))
			Expect(m.TunnelToken()).To(BeEmpty())
		})

		It("aborts when the context is cancelled while waiting for approval", func() {
			f := &fakeRegister{steps: []step{{res: pending}}}
			m := NewCredentialManager(f.fn(), true)
			m.initialBackoff = 10 * time.Millisecond

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := m.Acquire(ctx)
			Expect(err).To(MatchError(context.Canceled))
		})

		It("gives up after a bounded number of attempts so the worker exits and alerts", func() {
			f := &fakeRegister{steps: []step{{res: pending}}} // never approved
			m := NewCredentialManager(f.fn(), true)
			m.initialBackoff = time.Millisecond
			m.maxBackoff = time.Millisecond
			m.maxAttempts = 5

			_, err := m.Acquire(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("after 5 attempts"))
			Expect(err.Error()).To(ContainSubstring("pending admin approval"))
			Expect(f.count()).To(Equal(5))
		})
	})

	// The behaviour that survived the rename, and which nothing pinned while
	// the manager was about a JWT.
	//
	// The frontend mints a FRESH tunnel token on every registration and keeps
	// only its hash, so the previous one stops working the moment a new one is
	// issued. A manager that handed out the first value it saw would lock the
	// worker out of its own tunnel after any re-registration, and the symptom
	// would be a 401 on a dial rather than anything at registration time.
	Describe("TunnelToken (rotation)", func() {
		It("serves the token from the most recent registration, not the first", func() {
			m := NewCredentialManager(nil, false)
			m.store(approved("tunnel-1"))
			Expect(m.TunnelToken()).To(Equal("tunnel-1"))

			m.store(approved("tunnel-2"))
			Expect(m.TunnelToken()).To(Equal("tunnel-2"),
				"the frontend keeps only the newest token's hash, so serving the first one locks this worker out of its own tunnel")
		})

		It("keeps a working token when a registration carries none", func() {
			// A frontend that predates tunnel tokens, or one whose minting
			// failed, sends the field empty. Empty means "nothing new", not
			// "revoked": overwriting would lock the tunnel out until the next
			// registration that did carry one.
			m := NewCredentialManager(nil, false)
			m.store(approved("tunnel-1"))
			m.store(approved(""))
			Expect(m.TunnelToken()).To(Equal("tunnel-1"))
		})
	})
})

// Deleted with the bus: the "RefreshLoop" Describe and the "jwtExpiry default"
// Describe.
//
// RefreshLoop pinned that a worker re-registers before its minted broker JWT
// expires and serves the new credential to the next connection. Every noun in
// that sentence is gone: there is no JWT, no expiry to read, and no connection
// to serve it to. It is retired rather than moved. The one credential a
// registration still mints, the tunnel token, is rotated by a REGISTRATION
// rather than by a clock, and the property that matters about it is that the
// dialer reads the current value, which the rotation Describe above pins.
//
// jwtExpiry pinned that a real minted worker JWT's expiry decoded, which was
// the only thing in this package that needed a broker library at all.
