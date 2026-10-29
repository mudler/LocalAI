package provider_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func post(c *http.Client, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://studio.invalid/v1/chat/completions", strings.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	return c.Do(req)
}

var _ = Describe("the model failure budget", func() {
	// A model that keeps failing must end the turn, not keep the loop retrying for
	// minutes: after a few failures in a row the client says so for good.
	It("stops the retries for a model that keeps failing", func() {
		var calls atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Error(w, "backend down", http.StatusInternalServerError)
		}))
		c := tr.Client(nil)
		var err error
		for i := 0; i < provider.MaxModelFailures+3; i++ {
			var resp *http.Response
			if resp, err = post(c, `{}`); err != nil {
				break
			}
			_ = resp.Body.Close()
		}
		Expect(err).To(HaveOccurred(), "%d failed requests in a row were all let through", calls.Load())
		Expect(cogito.IsPermanent(err)).To(BeTrue(), "want a permanent error, got %v", err)
		Expect(err.Error()).To(ContainSubstring("failed"))
		Expect(int(calls.Load())).To(Equal(provider.MaxModelFailures), "requests that reached the model")
	})

	It("resets the count of failures after a success", func() {
		var n atomic.Int32
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail MaxModelFailures-1 times, succeed once, and do it again: never refused.
			if n.Add(1)%int32(provider.MaxModelFailures) != 0 {
				http.Error(w, "busy", http.StatusBadGateway)
				return
			}
			_, _ = io.WriteString(w, `{}`)
		}))
		c := tr.Client(nil)
		for i := 0; i < provider.MaxModelFailures*3; i++ {
			resp, err := post(c, `{}`)
			Expect(err).ToNot(HaveOccurred(), "request %d", i)
			_ = resp.Body.Close()
		}
	})

	// Real providers reject a request that names a tool twice.
	It("sends a tool named twice once", func() {
		var got string
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			got = string(b)
		}))
		body := `{"model":"m","tools":[{"type":"function","function":{"name":"a"}},{"type":"function","function":{"name":"b"}},{"type":"function","function":{"name":"a","description":"second"}}]}`
		resp, err := post(tr.Client(nil), body)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(strings.Count(got, `"name":"a"`)).To(Equal(1), "the model got %s", got)
		Expect(got).To(ContainSubstring(`"name":"b"`))
		Expect(got).To(ContainSubstring(`"model":"m"`))
		// A body with no repeated tool is passed on untouched.
		plain := `{"model":"m","tools":[{"type":"function","function":{"name":"a"}}]}`
		resp, err = post(tr.Client(nil), plain)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(got).To(Equal(plain), "a clean body was changed")
	})
})
