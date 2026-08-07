package buildproxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/buildproxy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var _ = Describe("Handler", func() {
	It("retries an idempotent transient response and records bytes", func() {
		dir := GinkgoT().TempDir()
		recorder, err := buildproxy.NewRecorder(dir + "/events.jsonl")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = recorder.Close() }()
		var calls atomic.Int32
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			status, body := http.StatusServiceUnavailable, "retry"
			if calls.Add(1) == 2 {
				status, body = http.StatusOK, "complete"
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
		})
		handler := buildproxy.NewHandler(buildproxy.Options{Transport: transport, Recorder: recorder, SpoolDir: dir, BaseDelay: time.Nanosecond})
		req := httptest.NewRequest(http.MethodGet, "https://example.test/archive", nil)
		response := httptest.NewRecorder()
		handler(response, req, "example.test")
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(Equal("complete"))
		Expect(calls.Load()).To(Equal(int32(2)))
	})

	It("does not retry a mutating request", func() {
		dir := GinkgoT().TempDir()
		recorder, err := buildproxy.NewRecorder(dir + "/events.jsonl")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = recorder.Close() }()
		var calls atomic.Int32
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("no")), ContentLength: 2}, nil
		})
		handler := buildproxy.NewHandler(buildproxy.Options{Transport: transport, Recorder: recorder, SpoolDir: dir})
		handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "https://example.test/token", strings.NewReader("secret")), "example.test")
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("preserves HEAD metadata without expecting a response body", func() {
		dir := GinkgoT().TempDir()
		recorder, err := buildproxy.NewRecorder(dir + "/events.jsonl")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = recorder.Close() }()
		var calls atomic.Int32
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, ContentLength: 1234}, nil
		})
		handler := buildproxy.NewHandler(buildproxy.Options{Transport: transport, Recorder: recorder, SpoolDir: dir})
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodHead, "https://example.test/blob", nil), "example.test")
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(response.Header().Get("Content-Length")).To(Equal("1234"))
	})
})
