package workerregistry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		mgr := NewCredentialManager(func(ctx context.Context) (*RegisterResponse, error) {
			return client.RegisterFull(ctx, map[string]any{"name": "w1"})
		}, true)
		_, err := mgr.Acquire(context.Background())
		Expect(err).To(MatchError(ErrRegistrationRejected))
		Expect(attempts.Load()).To(Equal(int32(1)))
	})
})

// The other direction of the same upgrade: a worker of this release registering
// against a frontend that still mints a per-node broker credential.
//
// The fields are gone from RegisterResponse, so the only question is what
// happens to the keys still on the wire. encoding/json ignores a key with no
// field, and that is asserted rather than assumed, because a decoder switched
// to DisallowUnknownFields would turn every registration against an older
// frontend into a hard failure with no other symptom.
var _ = Describe("Registering against a frontend that still mints broker credentials", func() {
	It("decodes the response and drops the keys it no longer has fields for", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"node-7","status":"healthy","api_token":"key-7","tunnel_token":"tunnel-7","nats_jwt":"eyJ0","nats_user_seed":"SUUSER"}`))
		}))
		DeferCleanup(server.Close)

		client := &RegistrationClient{FrontendURL: server.URL, HTTPTimeout: 2 * time.Second}
		res, err := client.RegisterFull(context.Background(), map[string]any{"name": "w1"})
		Expect(err).ToNot(HaveOccurred())

		// The fields it DOES have, so the assertion below is about the two
		// unknown keys and not about a decode that produced nothing.
		Expect(res.ID).To(Equal("node-7"))
		Expect(res.APIToken).To(Equal("key-7"))
		Expect(res.TunnelToken).To(Equal("tunnel-7"))

		// And nowhere for a broker credential to land: asserted on the struct's
		// own type, because a value assertion would need a field to read and
		// would stop compiling exactly when the field came back.
		t := reflect.TypeOf(*res)
		for _, gone := range []string{"NatsJWT", "NatsUserSeed"} {
			_, found := t.FieldByName(gone)
			Expect(found).To(BeFalse(),
				"RegisterResponse.%s is back: a worker that stores a broker credential is a worker something expects to dial a broker", gone)
		}
	})
})
