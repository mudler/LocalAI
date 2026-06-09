package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Run chat", func() {
	It("streams a single chat response", func() {
		var capturedModel string
		var capturedAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model","object":"model"}]}`)
				return
			}

			Expect(r.URL.Path).To(Equal("/v1/chat/completions"))
			capturedAuth = r.Header.Get("Authorization")

			var body struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			capturedModel = body.Model
			Expect(body.Messages).To(HaveLen(1))
			Expect(body.Messages[0].Role).To(Equal("user"))
			Expect(body.Messages[0].Content).To(Equal("hello"))

			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"!\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer server.Close()

		var out bytes.Buffer
		err := Run(GinkgoT().Context(), Options{
			Model:   "test-model",
			BaseURL: server.URL + "/v1",
			APIKey:  "secret",
			In:      strings.NewReader("hello\n/exit\n"),
			Out:     &out,
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(capturedModel).To(Equal("test-model"))
		Expect(capturedAuth).To(Equal("Bearer secret"))
		Expect(out.String()).To(ContainSubstring("assistant: hi!"))
		Expect(out.String()).To(ContainSubstring("bye"))
	})

	It("auto-selects the only available model", func() {
		server := chatTestServer([]string{"solo"}, nil)
		defer server.Close()

		var out bytes.Buffer
		err := Run(GinkgoT().Context(), Options{
			BaseURL: server.URL + "/v1",
			In:      strings.NewReader("/exit\n"),
			Out:     &out,
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("LocalAI chat (solo)"))
	})

	It("returns an actionable error when no models are installed", func() {
		server := chatTestServer(nil, nil)
		defer server.Close()

		err := Run(GinkgoT().Context(), Options{
			BaseURL: server.URL + "/v1",
			In:      strings.NewReader(""),
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no chat models are installed"))
		Expect(err.Error()).To(ContainSubstring("local-ai models install <model>"))
	})

	It("returns an actionable error when multiple models are available without a selection", func() {
		server := chatTestServer([]string{"alpha", "beta"}, nil)
		defer server.Close()

		err := Run(GinkgoT().Context(), Options{
			BaseURL: server.URL + "/v1",
			In:      strings.NewReader(""),
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("multiple models are available"))
		Expect(err.Error()).To(ContainSubstring("--model"))
		Expect(err.Error()).To(ContainSubstring("alpha"))
		Expect(err.Error()).To(ContainSubstring("beta"))
	})

	It("lists and switches models inside the chat", func() {
		requestedModels := []string{}
		server := chatTestServer([]string{"alpha", "beta"}, func(model string) {
			requestedModels = append(requestedModels, model)
		})
		defer server.Close()

		var out bytes.Buffer
		err := Run(GinkgoT().Context(), Options{
			Model:   "alpha",
			BaseURL: server.URL + "/v1",
			In:      strings.NewReader("/models\n/model beta\nhello\n/exit\n"),
			Out:     &out,
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("* alpha"))
		Expect(out.String()).To(ContainSubstring("  beta"))
		Expect(out.String()).To(ContainSubstring("switched to beta; conversation cleared"))
		Expect(requestedModels).To(Equal([]string{"beta"}))
	})
})

func chatTestServer(models []string, onChat func(model string)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[`)
			for i, model := range models {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%q,"object":"model"}`, model)
			}
			fmt.Fprint(w, `]}`)
		case "/v1/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			if onChat != nil {
				onChat(body.Model)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}
