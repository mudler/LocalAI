package workerregistry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"

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

var _ = Describe("NATSCredentialManager", func() {
	approved := func(jwt, seed string) *RegisterResponse {
		return &RegisterResponse{ID: "node-1", Status: "healthy", NatsJWT: jwt, NatsUserSeed: seed}
	}
	pending := &RegisterResponse{ID: "node-1", Status: "pending"}

	Describe("Acquire (#4 — wait through admin approval)", func() {
		It("keeps re-registering until the node is approved and credentials are minted", func() {
			f := &fakeRegister{steps: []step{
				{res: pending},                     // not approved yet
				{res: approved("", "")},            // approved but JWT not minted yet
				{res: approved("jwt-1", "seed-1")}, // finally minted
			}}
			m := NewNATSCredentialManager(f.fn(), true /* requireCreds */)
			m.initialBackoff = time.Millisecond
			m.maxBackoff = time.Millisecond

			res, err := m.Acquire(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(res.ID).To(Equal("node-1"))
			Expect(f.count()).To(Equal(3))

			jwt, seed := m.Current()
			Expect(jwt).To(Equal("jwt-1"))
			Expect(seed).To(Equal("seed-1"))
			Expect(m.HasCredentials()).To(BeTrue())
			Expect(m.NodeID()).To(Equal("node-1"))
		})

		It("returns immediately on the first success when credentials are not required (anonymous NATS)", func() {
			f := &fakeRegister{steps: []step{{res: pending}}}
			m := NewNATSCredentialManager(f.fn(), false /* requireCreds */)

			res, err := m.Acquire(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Status).To(Equal("pending"))
			Expect(f.count()).To(Equal(1))
			Expect(m.HasCredentials()).To(BeFalse())
		})

		It("aborts when the context is cancelled while waiting for approval", func() {
			f := &fakeRegister{steps: []step{{res: pending}}}
			m := NewNATSCredentialManager(f.fn(), true)
			m.initialBackoff = 10 * time.Millisecond

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := m.Acquire(ctx)
			Expect(err).To(MatchError(context.Canceled))
		})

		It("gives up after a bounded number of attempts so the worker exits and alerts", func() {
			f := &fakeRegister{steps: []step{{res: pending}}} // never approved
			m := NewNATSCredentialManager(f.fn(), true)
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

	Describe("RefreshLoop (#5 — renew before the JWT expires)", func() {
		It("re-registers before expiry and updates the credentials served to new connections", func() {
			f := &fakeRegister{steps: []step{{res: approved("jwt-2", "seed-2")}}}
			m := NewNATSCredentialManager(f.fn(), true)
			m.refreshLead = 0.5
			m.refreshRetry = time.Millisecond
			// jwt-1 expires soon; jwt-2 is long-lived so the loop then idles.
			m.expiryOf = func(jwt string) (time.Time, bool) {
				switch jwt {
				case "jwt-1":
					return time.Now().Add(40 * time.Millisecond), true
				case "jwt-2":
					return time.Now().Add(time.Hour), true
				default:
					return time.Time{}, false
				}
			}
			m.store(approved("jwt-1", "seed-1"))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = m.RefreshLoop(ctx) }()

			Eventually(func() string {
				jwt, _ := m.Current()
				return jwt
			}, "2s", "10ms").Should(Equal("jwt-2"))
		})

		It("returns an error after the bounded number of consecutive failures so the caller can exit", func() {
			f := &fakeRegister{steps: []step{{err: context.DeadlineExceeded}}} // refresh always fails
			m := NewNATSCredentialManager(f.fn(), true)
			m.refreshLead = 0.5
			m.refreshRetry = time.Millisecond
			m.maxAttempts = 3
			m.expiryOf = func(string) (time.Time, bool) { return time.Now().Add(time.Millisecond), true }
			m.store(approved("jwt-1", "seed-1"))

			errCh := make(chan error, 1)
			go func() { errCh <- m.RefreshLoop(context.Background()) }()
			Eventually(errCh, "2s").Should(Receive(MatchError(ContainSubstring("3 times in a row"))))
		})

		It("exits promptly when the current credential has no expiry (nothing to refresh)", func() {
			f := &fakeRegister{steps: []step{{res: approved("x", "y")}}}
			m := NewNATSCredentialManager(f.fn(), true)
			m.expiryOf = func(string) (time.Time, bool) { return time.Time{}, false }
			m.store(approved("static", "seed"))

			done := make(chan struct{})
			go func() { _ = m.RefreshLoop(context.Background()); close(done) }()
			Eventually(done, "1s").Should(BeClosed())
			Expect(f.count()).To(Equal(0)) // never tried to re-register
		})
	})

	Describe("jwtExpiry default", func() {
		It("decodes the expiry of a real minted worker JWT", func() {
			akp, err := nkeys.CreateAccount()
			Expect(err).ToNot(HaveOccurred())
			seed, err := akp.Seed()
			Expect(err).ToNot(HaveOccurred())

			cfg := natsauth.Config{AccountSeed: string(seed), WorkerJWTTTL: time.Hour}
			token, _, err := cfg.MintWorkerJWT("node-1", "backend")
			Expect(err).ToNot(HaveOccurred())

			exp, ok := jwtExpiry(token)
			Expect(ok).To(BeTrue())
			Expect(exp).To(BeTemporally("~", time.Now().Add(time.Hour), 2*time.Minute))
		})

		It("reports no expiry for an empty or undecodable token", func() {
			_, ok := jwtExpiry("")
			Expect(ok).To(BeFalse())
			_, ok = jwtExpiry("not-a-jwt")
			Expect(ok).To(BeFalse())
		})
	})
})
