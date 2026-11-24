package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/gomega"
)

// helper: run a CloudProxy in-process via grpc.Provide so tests can
// call Forward through the public Backend interface without listening
// on a real socket.
func newInProcClient(t *testing.T, proxy *CloudProxy) grpc.Backend {
	t.Helper()
	addr := "test://" + t.Name()
	grpc.Provide(addr, proxy)
	return grpc.NewClient(addr, true, nil, false)
}

func TestForward_PassthroughEcho(t *testing.T) {
	g := NewWithT(t)
	// Fake upstream: echoes the request body back, prefixed with a
	// canary so the test can assert both that the body reached the
	// upstream and the response made it back to the client.
	gotBody := make(chan string, 1)
	gotAuth := make(chan string, 1)
	gotPath := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody <- string(body)
		gotAuth <- r.Header.Get("Authorization")
		gotPath <- r.URL.Path
		w.Header().Set("X-Echo", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("echo: " + string(body)))
	}))
	defer upstream.Close()

	t.Setenv("CLOUD_PROXY_FAKE_KEY", "sk-fake")

	cp := NewCloudProxy()
	err := cp.Load(&pb.ModelOptions{
		Proxy: &pb.ProxyOptions{
			UpstreamUrl: upstream.URL,
			Mode:        modePassthrough,
			ApiKeyEnv:   "CLOUD_PROXY_FAKE_KEY",
		},
	})
	g.Expect(err).NotTo(HaveOccurred())

	c := newInProcClient(t, cp)
	stream, err := c.Forward(context.Background())
	g.Expect(err).NotTo(HaveOccurred())

	err = stream.Send(&pb.ForwardRequest{
		Path:      "/v1/chat/completions",
		Method:    "POST",
		Headers:   []*pb.ForwardHeader{{Name: "Content-Type", Value: "application/json"}},
		BodyChunk: []byte(`{"prompt":`),
	})
	g.Expect(err).NotTo(HaveOccurred())
	err = stream.Send(&pb.ForwardRequest{BodyChunk: []byte(`"hi"}`)})
	g.Expect(err).NotTo(HaveOccurred())
	err = stream.CloseSend()
	g.Expect(err).NotTo(HaveOccurred())

	// First reply: status + headers.
	first, err := stream.Recv()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(first.Status).To(Equal(int32(http.StatusOK)))
	g.Expect(hasHeader(first.Headers, "X-Echo", "true")).To(BeTrue())

	// Subsequent replies: body.
	var body []byte
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		g.Expect(err).NotTo(HaveOccurred())
		body = append(body, r.BodyChunk...)
	}
	g.Expect(string(body)).To(Equal(`echo: {"prompt":"hi"}`))

	// Upstream observations.
	var gotBodyVal, gotAuthVal, gotPathVal string
	g.Eventually(gotBody).Should(Receive(&gotBodyVal), "upstream never saw body")
	g.Expect(gotBodyVal).To(Equal(`{"prompt":"hi"}`))
	g.Eventually(gotAuth).Should(Receive(&gotAuthVal), "upstream never saw auth header")
	g.Expect(gotAuthVal).To(Equal("Bearer sk-fake"))
	g.Eventually(gotPath).Should(Receive(&gotPathVal), "upstream never saw path")
	g.Expect(gotPathVal).To(Equal("/v1/chat/completions"))
}

func TestForward_AnthropicAuthHeader(t *testing.T) {
	g := NewWithT(t)
	gotXAPIKey := make(chan string, 1)
	gotVersion := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXAPIKey <- r.Header.Get("x-api-key")
		gotVersion <- r.Header.Get("anthropic-version")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Setenv("CLOUD_PROXY_ANTHROPIC_KEY", "sk-ant-fake")

	cp := NewCloudProxy()
	err := cp.Load(&pb.ModelOptions{
		Proxy: &pb.ProxyOptions{
			UpstreamUrl: upstream.URL,
			Mode:        modePassthrough,
			Provider:    providerAnthropic,
			ApiKeyEnv:   "CLOUD_PROXY_ANTHROPIC_KEY",
		},
	})
	g.Expect(err).NotTo(HaveOccurred())

	c := newInProcClient(t, cp)
	stream, err := c.Forward(context.Background())
	g.Expect(err).NotTo(HaveOccurred())
	err = stream.Send(&pb.ForwardRequest{Path: "/v1/messages", Method: "POST"})
	g.Expect(err).NotTo(HaveOccurred())
	_ = stream.CloseSend()
	_, _ = stream.Recv() // drain status
	for {
		if _, err := stream.Recv(); errors.Is(err, io.EOF) || err != nil {
			break
		}
	}

	g.Expect(<-gotXAPIKey).To(Equal("sk-ant-fake"))
	g.Expect(<-gotVersion).NotTo(BeEmpty())
}

func TestLoad_ValidatesConfig(t *testing.T) {
	g := NewWithT(t)
	cp := NewCloudProxy()

	err := cp.Load(&pb.ModelOptions{})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("ProxyOptions"))

	err = cp.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{}})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("upstream_url"))

	err = cp.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
		UpstreamUrl: "https://example.com",
		Mode:        "rewrite",
	}})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("unknown mode"))

	// translate + openai should load successfully (Phase 5).
	err = cp.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
		UpstreamUrl: "https://example.com/v1/chat/completions",
		Mode:        modeTranslate,
		Provider:    providerOpenAI,
	}})
	g.Expect(err).NotTo(HaveOccurred())

	// translate + anthropic should load successfully (Phase 6).
	err = cp.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
		UpstreamUrl: "https://example.com/v1/messages",
		Mode:        modeTranslate,
		Provider:    providerAnthropic,
	}})
	g.Expect(err).NotTo(HaveOccurred())

	err = cp.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
		UpstreamUrl: "https://example.com",
		ApiKeyEnv:   "DEFINITELY_UNSET_ENV_VAR_XYZ",
	}})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("unset"))
}

func TestForward_RejectsWithoutLoad(t *testing.T) {
	g := NewWithT(t)
	cp := NewCloudProxy()
	c := newInProcClient(t, cp)
	stream, err := c.Forward(context.Background())
	g.Expect(err).NotTo(HaveOccurred())
	_ = stream.CloseSend()
	_, err = stream.Recv()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("not loaded"))
}

func hasHeader(hs []*pb.ForwardHeader, name, value string) bool {
	for _, h := range hs {
		if strings.EqualFold(h.GetName(), name) && h.GetValue() == value {
			return true
		}
	}
	return false
}
