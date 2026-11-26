package provider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

const loadingBody = `{"error":{"message":"model m is loading on node n1 (60%)","code":"model_loading","type":"model_loading"},` +
	`"loading":{"model":"m","state":"loading","node":"n1","progress":60,"bytes_sent":6,"total_bytes":10}}`

// fakeSleep records the waits and returns at once.
type fakeSleep struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (f *fakeSleep) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.waits = append(f.waits, d)
	f.mu.Unlock()
	return ctx.Err()
}

func (f *fakeSleep) total() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t time.Duration
	for _, w := range f.waits {
		t += w
	}
	return t
}

// loadingThenOK answers n loading 503s, then 200.
func loadingThenOK(n int32, retryAfter string, calls *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= n {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, loadingBody)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	})
}

var _ = Describe("a model that is still loading", func() {
	// A cold load can take many minutes. The 503 the route answers meanwhile is
	// not a failure of the model: the client waits and asks again, for longer
	// than the failure budget would allow.
	It("waits for the load and lets the request through", func() {
		var calls atomic.Int32
		n := int32(provider.MaxModelFailures + 2)
		tr := provider.NewTransport()
		tr.SetHandler(loadingThenOK(n, "7", &calls))
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)
		var seen []provider.Loading
		c := tr.ClientOnLoading(nil, func(l provider.Loading) { seen = append(seen, l) })

		resp, err := post(c, `{"model":"m"}`)
		Expect(err).ToNot(HaveOccurred())
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(string(body)).To(Equal(`{"model":"m"}`), "the retried request lost its body")
		Expect(calls.Load()).To(Equal(n + 1))
		Expect(fs.waits).To(HaveLen(int(n)))
		Expect(fs.waits[0]).To(Equal(7 * time.Second))

		Expect(seen).To(HaveLen(int(n)))
		Expect(seen[0].Model).To(Equal("m"))
		Expect(seen[0].Node).To(Equal("n1"))
		Expect(seen[0].Progress).To(BeNumerically("==", 60))
		Expect(seen[0].RetryAfter).To(Equal(7 * time.Second))
		Expect(seen[1].Waited).To(Equal(7 * time.Second))

		// The waits counted as no failure: a real failure budget is left whole.
		for range provider.MaxModelFailures - 1 {
			resp, err := post(c, `{}`)
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
		}
	})

	It("waits a default time when the answer has no Retry-After", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(loadingThenOK(1, "", &calls))
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)
		resp, err := post(tr.Client(nil), `{}`)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(fs.waits).To(Equal([]time.Duration{provider.DefaultLoadRetry}))
	})

	It("ends the turn with its own error when the load takes longer than the budget", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(loadingThenOK(1<<30, "30", &calls))
		tr.SetLoadWait(2 * time.Minute)
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)

		_, err := post(tr.Client(nil), `{}`)
		Expect(err).To(HaveOccurred())
		Expect(cogito.IsPermanent(err)).To(BeTrue(), "want a permanent error, got %v", err)
		Expect(err.Error()).To(ContainSubstring(`model "m" is still loading after 2 minutes`))
		Expect(err.Error()).NotTo(ContainSubstring("failed"))
		Expect(fs.total()).To(Equal(2 * time.Minute))

		f := session.Classify(err)
		Expect(f.Code).To(Equal(session.CodeModelLoading))
		Expect(f.Model).To(Equal("m"))

		// A new client serves a new turn, with a whole budget of its own.
		before := len(fs.waits)
		_, err = post(tr.Client(nil), `{}`)
		Expect(err).To(HaveOccurred())
		Expect(len(fs.waits)).To(BeNumerically(">", before), "a new client is a new turn with a new budget")
	})

	It("stops waiting when the turn is cancelled", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(loadingThenOK(1<<30, "30", &calls))
		ctx, cancel := context.WithCancel(context.Background())
		provider.SetSleep(tr, func(c context.Context, d time.Duration) error {
			cancel()
			<-c.Done()
			return c.Err()
		})
		_, err := get(tr.Client(nil), ctx, "http://studio.invalid/v1/chat/completions")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got %v", err)
		Expect(cogito.IsPermanent(err)).To(BeFalse())
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("cancels a real wait as soon as the context ends", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(loadingThenOK(1<<30, "60", &calls))
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := get(tr.Client(nil), ctx, "http://studio.invalid/v1/chat/completions")
		Expect(err).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	It("waits for a model at capacity but not for a spent quota", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","code":"admission_rejected","message":"at capacity"}}`)
				return
			}
			_, _ = io.WriteString(w, `{}`)
		}))
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)
		resp, err := post(tr.Client(nil), `{}`)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(fs.waits).To(Equal([]time.Duration{2 * time.Second}))

		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"type":"quota_exceeded","code":429,"message":"quota"}}`)
		}))
		_, err = post(tr.Client(nil), `{}`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(session.MarkQuota))
		Expect(cogito.IsPermanent(err)).To(BeTrue())
	})

	It("still counts a 503 without Retry-After as a failure", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Error(w, "no nodes", http.StatusServiceUnavailable)
		}))
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)
		c := tr.Client(nil)
		var err error
		for range provider.MaxModelFailures + 3 {
			var resp *http.Response
			if resp, err = post(c, `{}`); err != nil {
				break
			}
			_ = resp.Body.Close()
		}
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed"))
		Expect(int(calls.Load())).To(Equal(provider.MaxModelFailures))
		Expect(fs.waits).To(BeEmpty())
	})

	// A load that failed holds its node for a short window, and the route
	// answers 503 model_loading with the failed state and a Retry-After for
	// that window. Waiting does not fix it: the turn ends at once, with the
	// cause of the failure.
	It("ends the turn at once for a load that failed", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "30")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"loading model m: out of memory on the GPU","code":"model_loading","type":"model_loading"},`+
				`"loading":{"model":"m","state":"failed","node":"n1","progress":0,"last_error":"out of memory on the GPU","retry_after":30}}`)
		}))
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)

		_, err := post(tr.Client(nil), `{}`)

		Expect(err).To(HaveOccurred())
		Expect(cogito.IsPermanent(err)).To(BeTrue(), "want a permanent error, got %v", err)
		Expect(err.Error()).To(ContainSubstring("out of memory on the GPU"))
		Expect(err.Error()).NotTo(ContainSubstring("still loading"))
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(fs.waits).To(BeEmpty())
		Expect(session.Classify(err).Code).To(Equal(session.CodeLoadFailed))
	})

	// No node for the model (no room, an upgrade in progress) is a 503 with a
	// Retry-After too. The turn may wait for it, but when the wait runs out
	// the error says what the server said, not that the model is loading.
	It("names the real cause when a 503 that is not a load outlasts the budget", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "10")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"no node has enough free memory for model m (needs about 40 GB): no available nodes","type":""}}`)
		}))
		tr.SetLoadWait(time.Minute)
		fs := &fakeSleep{}
		provider.SetSleep(tr, fs.sleep)

		_, err := post(tr.Client(nil), `{}`)

		Expect(err).To(HaveOccurred())
		Expect(cogito.IsPermanent(err)).To(BeTrue(), "want a permanent error, got %v", err)
		Expect(err.Error()).To(ContainSubstring("no node has enough free memory for model m"))
		Expect(err.Error()).NotTo(ContainSubstring("still loading"))
		Expect(err.Error()).NotTo(ContainSubstring(session.MarkStillLoading))
		Expect(fs.total()).To(Equal(time.Minute))
		Expect(session.Classify(err).Code).NotTo(Equal(session.CodeModelLoading))
	})

	DescribeTable("reads Retry-After",
		func(v string, want time.Duration) {
			now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			Expect(provider.ParseRetryAfter(v, now)).To(Equal(want))
		},
		Entry("seconds", "12", 12*time.Second),
		Entry("absent", "", provider.DefaultLoadRetry),
		Entry("garbage", "soon", provider.DefaultLoadRetry),
		Entry("zero is raised to the floor", "0", time.Second),
		Entry("huge is cut to the ceiling", "300", time.Minute),
		Entry("an HTTP date", "Thu, 01 Jan 2026 12:00:20 GMT", 20*time.Second),
		Entry("an HTTP date in the past", "Thu, 01 Jan 2026 11:00:00 GMT", time.Second),
	)
})
