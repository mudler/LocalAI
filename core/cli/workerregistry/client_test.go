package workerregistry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The case these specs exist for: a worker of this release registering against
// a frontend that predates it. A worker no longer sends an address, the old
// frontend requires one, and it answers 400 with the reason in the body. Two
// things used to go wrong there at once. The reason was discarded, so the
// operator saw only "status 400" and had to guess which of several mistakes
// they had made; and the retry ladder spent four minutes on a verdict the
// frontend reached instantly.
var _ = Describe("Registration client refusals", func() {
	var (
		attempts atomic.Int32
		status   atomic.Int32
		body     atomic.Value // string
		server   *httptest.Server
		client   *RegistrationClient
		// seen carries one token per request the handler served, so a spec can
		// wait for the Nth attempt instead of sleeping for however long the
		// ladder's backoff happens to be.
		seen chan struct{}
	)

	BeforeEach(func() {
		attempts.Store(0)
		status.Store(int32(http.StatusBadRequest))
		body.Store(`{"error":{"code":400,"message":"address is required for backend workers"}}`)
		seen = make(chan struct{}, 64)
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			select {
			case seen <- struct{}{}:
			default:
			}
			w.WriteHeader(int(status.Load()))
			_, _ = w.Write([]byte(body.Load().(string)))
		}))
		client = &RegistrationClient{FrontendURL: server.URL, HTTPTimeout: 2 * time.Second}
	})

	AfterEach(func() { server.Close() })

	It("quotes what the frontend said", func() {
		_, err := client.RegisterFull(context.Background(), map[string]any{"name": "w1"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("status 400"))
		Expect(err.Error()).To(ContainSubstring("address is required for backend workers"))
	})

	It("marks a 4xx as a refusal", func() {
		_, err := client.RegisterFull(context.Background(), map[string]any{"name": "w1"})
		Expect(err).To(MatchError(ErrRegistrationRejected))
	})

	It("does not mark a 5xx as a refusal", func() {
		// A frontend that is restarting or wedged has not judged anything, and
		// retrying it is the whole reason the ladder exists.
		status.Store(int32(http.StatusBadGateway))
		body.Store("bad gateway")
		_, err := client.RegisterFull(context.Background(), map[string]any{"name": "w1"})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrRegistrationRejected)).To(BeFalse())
	})

	DescribeTable("treats a status that asks for the same request again as retryable",
		func(code int) {
			status.Store(int32(code))
			body.Store("later")
			_, err := client.RegisterFull(context.Background(), map[string]any{"name": "w1"})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrRegistrationRejected)).To(BeFalse())
		},
		Entry("408 Request Timeout", http.StatusRequestTimeout),
		Entry("429 Too Many Requests", http.StatusTooManyRequests),
	)

	It("stops the retry ladder on the first refusal", func() {
		// Ten attempts on a 400 is roughly four minutes of backoff before the
		// operator is told anything, and the answer is the same one the
		// frontend gave immediately.
		_, err := client.RegisterFullWithRetry(context.Background(), map[string]any{"name": "w1"}, 10)
		Expect(err).To(MatchError(ErrRegistrationRejected))
		Expect(err.Error()).To(ContainSubstring("address is required for backend workers"))
		Expect(attempts.Load()).To(Equal(int32(1)))
	})

	It("still retries something that is not a refusal", func() {
		// The control. Without it, a change that returned on EVERY error would
		// pass the spec above and silently delete the retry behaviour a worker
		// booting alongside its frontend depends on.
		status.Store(int32(http.StatusServiceUnavailable))
		body.Store("starting up")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			_, err := client.RegisterFullWithRetry(ctx, map[string]any{"name": "w1"}, 10)
			done <- err
		}()

		// Two tokens is the whole assertion: the ladder came back for a second
		// attempt on a status that is not a verdict. Waiting on the handler
		// rather than on a duration makes it exact instead of tolerant.
		Eventually(seen).Should(Receive())
		Eventually(seen, "10s").Should(Receive())
		cancel()

		var ladderErr error
		Eventually(done).Should(Receive(&ladderErr))
		Expect(ladderErr).To(HaveOccurred())
		Expect(errors.Is(ladderErr, ErrRegistrationRejected)).To(BeFalse())
		Expect(attempts.Load()).To(BeNumerically(">=", 2))
	})

	It("stops the credential manager's acquire loop on a refusal", func() {
		// The default worker path goes through Acquire, not the ladder above,
		// and its bound is 100 attempts rather than 10. A refusal there is the
		// same verdict and has to end the same way.
		mgr := NewNATSCredentialManager(func(ctx context.Context) (*RegisterResponse, error) {
			return client.RegisterFull(ctx, map[string]any{"name": "w1"})
		}, true)
		_, err := mgr.Acquire(context.Background())
		Expect(err).To(MatchError(ErrRegistrationRejected))
		Expect(attempts.Load()).To(Equal(int32(1)))
	})
})
