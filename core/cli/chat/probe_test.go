package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Probe", func() {
	It("returns the advertised models", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]string{
					{"id": "model-a", "object": "model"},
					{"id": "model-b", "object": "model"},
				},
			})).To(Succeed())
		}))
		defer srv.Close()

		models, err := Probe(context.Background(), srv.URL+"/v1", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(Equal([]string{"model-a", "model-b"}))
	})

	It("reports an unreachable server distinguishably", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close() // nothing is listening now

		_, err := Probe(context.Background(), url+"/v1", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUnreachable)).To(BeTrue(), "want ErrUnreachable, got %v", err)
	})

	It("reports an auth failure distinguishably", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		_, err := Probe(context.Background(), srv.URL+"/v1", "bad-key")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUnauthorized)).To(BeTrue(), "want ErrUnauthorized, got %v", err)
	})

	// LocalAI's normal error handler replies with an OpenAI error envelope, and
	// its opaque-errors handler replies with a bare status and no body. Those
	// reach the client as two different go-openai types, so both have to be
	// classified the same way.
	It("reports an auth failure carrying an error envelope distinguishably", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			Expect(json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "invalid api key", "code": http.StatusUnauthorized},
			})).To(Succeed())
		}))
		defer srv.Close()

		_, err := Probe(context.Background(), srv.URL+"/v1", "bad-key")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUnauthorized)).To(BeTrue(), "want ErrUnauthorized, got %v", err)
	})

	It("does not call a server that answered with an error unreachable", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		_, err := Probe(context.Background(), srv.URL+"/v1", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUnreachable)).To(BeFalse(), "a server that replied is not unreachable, got %v", err)
		Expect(errors.Is(err, ErrUnauthorized)).To(BeFalse(), "500 is not an auth failure, got %v", err)
	})

	// Pointing chat at some other service that happens to be listening is a
	// different problem from nothing listening, and needs different advice.
	It("does not call a reply it could not parse unreachable", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, err := w.Write([]byte("<html><body>not LocalAI</body></html>"))
			Expect(err).ToNot(HaveOccurred())
		}))
		defer srv.Close()

		_, err := Probe(context.Background(), srv.URL+"/v1", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUnreachable)).To(BeFalse(), "something answered, got %v", err)
	})

	It("returns every advertised id, including ones that are not models", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]string{
					{"id": "zeta", "object": "model"},
					{"id": ".gitignore", "object": "model"},
					{"id": "alpha", "object": "model"},
					{"id": "voice.tar.bz2", "object": "model"},
				},
			})).To(Succeed())
		}))
		defer srv.Close()

		// Verbatim and in server order: deciding which of these are real, and
		// what order to show them in, belongs to the caller.
		models, err := Probe(context.Background(), srv.URL+"/v1", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(Equal([]string{"zeta", ".gitignore", "alpha", "voice.tar.bz2"}))
	})

	It("stops early when the context is already cancelled", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})).To(Succeed())
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := Probe(ctx, srv.URL+"/v1", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "want the cancellation preserved, got %v", err)
		// A cancelled probe learned nothing about the endpoint, so it must not
		// send the caller off to start a server that may already be running.
		Expect(errors.Is(err, ErrUnreachable)).To(BeFalse(), "cancelling is not a verdict on the server, got %v", err)
	})

	It("reports a server that never answers as unreachable", func() {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
		}))
		defer srv.Close()
		defer close(release)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := Probe(ctx, srv.URL+"/v1", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUnreachable)).To(BeTrue(), "want ErrUnreachable, got %v", err)
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "want the deadline preserved, got %v", err)
	})

	It("returns an empty list when the server has no models", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})).To(Succeed())
		}))
		defer srv.Close()

		models, err := Probe(context.Background(), srv.URL+"/v1", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(BeEmpty())
	})
})
