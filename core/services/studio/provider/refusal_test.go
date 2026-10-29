package provider_test

import (
	"context"
	"net/http"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/model"
)

var _ = Describe("refused and failing model requests", func() {
	It("reports a refused request with a marker the loop recognizes", func() {
		for code, mark := range map[int]string{http.StatusForbidden: session.MarkNotAllowed, http.StatusTooManyRequests: session.MarkQuota} {
			tr := provider.NewTransport()
			tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			_, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/v1/chat/completions")
			Expect(err).To(HaveOccurred(), "status %d", code)
			Expect(err.Error()).To(ContainSubstring(mark), "status %d", code)
			// No retry fixes a refusal: the model client must stop at once.
			Expect(cogito.IsPermanent(err)).To(BeTrue(), "status %d: the error is not marked permanent, so the agent loop would retry it", code)
		}
	})

	// A model whose last load failed is held in a cooldown, and the route says so.
	// Retrying inside the loop only waits for the same failure, so the turn must end
	// with the reason instead of staying running.
	It("ends the turn at once for a model that cannot load", func() {
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.Header().Set(model.LoadFailedHeader, "true")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"message":"model \"m\" load is in cooldown after a recent failure; retry after 30s"}}`))
		}))
		_, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/v1/chat/completions")
		Expect(err).To(HaveOccurred())
		Expect(cogito.IsPermanent(err)).To(BeTrue(), "want a permanent error, got %v", err)
		Expect(err.Error()).To(ContainSubstring("could not be loaded"))
	})

	It("still retries an overloaded model", func() {
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		resp, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/v1/chat/completions")
		Expect(err).ToNot(HaveOccurred(), "a plain 503 became an error")
		_ = resp.Body.Close()
	})
})
