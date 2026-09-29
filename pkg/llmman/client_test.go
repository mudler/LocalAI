package llmman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Endpoint", func() {
	It("defaults to llmman serve's default", func() {
		GinkgoT().Setenv(HostEnv, "")
		Expect(Endpoint()).To(Equal("http://127.0.0.1:17434"))
	})

	DescribeTable("parses host forms",
		func(in, want string) {
			GinkgoT().Setenv(HostEnv, in)
			Expect(Endpoint()).To(Equal(want))
		},
		Entry("host and port", "1.2.3.4:9999", "http://1.2.3.4:9999"),
		Entry("host only", "1.2.3.4", "http://1.2.3.4:17434"),
		Entry("scheme", "http://1.2.3.4:9999", "http://1.2.3.4:9999"),
		Entry("path ignored", "http://1.2.3.4:9999/ignored", "http://1.2.3.4:9999"),
		Entry("quoted", `"1.2.3.4:9999"`, "http://1.2.3.4:9999"),
		// A wildcard bind is meaningful to the server but not to a client,
		// which cannot connect to "every interface".
		Entry("IPv4 wildcard", "0.0.0.0:9999", "http://127.0.0.1:9999"),
		Entry("IPv6 wildcard", "[::]:9999", "http://[::1]:9999"),
	)
})

var _ = Describe("CheckDaemon", func() {
	It("accepts a llmman daemon", func() {
		var path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "0.1.0", "pid": 1})
		}))
		DeferCleanup(srv.Close)

		Expect(CheckDaemon(context.Background(), srv.Client(), srv.URL)).To(Succeed())
		Expect(path).To(Equal("/api/version"))
	})

	It("rejects a server that is not llmman", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"hello":"world"}`))
		}))
		DeferCleanup(srv.Close)

		err := CheckDaemon(context.Background(), srv.Client(), srv.URL)
		Expect(err).To(MatchError(ContainSubstring("not an llmman daemon")))
	})

	It("reports nothing listening with an actionable error", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		err := CheckDaemon(context.Background(), ProbeClient(), url)
		Expect(err).To(MatchError(ContainSubstring("llmman serve")))
	})
})

func ndjson(lines ...map[string]any) string {
	var b strings.Builder
	for _, l := range lines {
		raw, _ := json.Marshal(l)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

type pullRequest struct {
	method, path, model string
}

// pullServer replies with body and records the request it received.
func pullServer(body string, status int) (*httptest.Server, *pullRequest) {
	got := &pullRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		*got = pullRequest{method: r.Method, path: r.URL.Path, model: req["model"]}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	DeferCleanup(srv.Close)
	return srv, got
}

var _ = Describe("Pull", func() {
	It("succeeds and forwards progress", func() {
		srv, got := pullServer(ndjson(
			map[string]any{"status": "pulling manifest"},
			map[string]any{"status": "pulling blobs", "completed": 50, "total": 100},
			map[string]any{"status": "success"},
		), http.StatusOK)

		var seen []string
		var lastCompleted, lastTotal int64
		err := Pull(context.Background(), srv.Client(), srv.URL, "ghcr.io/org/model:tag",
			func(status string, completed, total int64) {
				seen = append(seen, status)
				lastCompleted, lastTotal = completed, total
			})

		Expect(err).ToNot(HaveOccurred())
		Expect(*got).To(Equal(pullRequest{method: http.MethodPost, path: "/api/pull", model: "ghcr.io/org/model:tag"}))
		Expect(seen).To(Equal([]string{"pulling manifest", "pulling blobs"}))
		Expect(lastCompleted).To(Equal(int64(50)))
		Expect(lastTotal).To(Equal(int64(100)))
	})

	It("reports an in-band error at HTTP 200", func() {
		// The daemon streams errors in-band, so a 200 does not mean success.
		srv, _ := pullServer(ndjson(
			map[string]any{"status": "pulling manifest"},
			map[string]any{"error": "unauthorized"},
		), http.StatusOK)

		err := Pull(context.Background(), srv.Client(), srv.URL, "ref", nil)
		Expect(err).To(MatchError(ContainSubstring("unauthorized")))
	})

	It("rejects a stream that ends without success", func() {
		srv, _ := pullServer(ndjson(map[string]any{"status": "pulling blobs"}), http.StatusOK)

		err := Pull(context.Background(), srv.Client(), srv.URL, "ref", nil)
		Expect(err).To(MatchError(ContainSubstring("without reporting success")))
	})

	It("reports a non-OK status", func() {
		srv, _ := pullServer(`{"error":"bad request"}`, http.StatusBadRequest)

		Expect(Pull(context.Background(), srv.Client(), srv.URL, "ref", nil)).ToNot(Succeed())
	})

	It("tolerates a non-JSON diagnostic line", func() {
		srv, _ := pullServer("not json\n"+ndjson(map[string]any{"status": "success"}), http.StatusOK)

		Expect(Pull(context.Background(), srv.Client(), srv.URL, "ref", nil)).To(Succeed())
	})
})

var _ = Describe("parseResolveOutput", func() {
	var dir string

	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("reads the path from the JSON line", func() {
		got, err := parseResolveOutput(`{"reference":"r","path":"`+dir+`","format":"safetensors"}`, "r")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(dir))
	})

	It("ignores a diagnostic that leaks onto stdout", func() {
		got, err := parseResolveOutput("pulling...\n{\"path\":\""+dir+"\"}\n", "r")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(dir))
	})

	It("ignores unknown fields so the contract can grow", func() {
		_, err := parseResolveOutput(`{"path":"`+dir+`","mmproj":"/x","future":1}`, "r")
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("rejects unusable output",
		func(out string) {
			_, err := parseResolveOutput(out, "r")
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("blank lines", "   \n\n"),
		Entry("not JSON", "not json"),
		Entry("no path", `{"no_path":1}`),
		Entry("empty path", `{"path":""}`),
		Entry("missing path on disk", `{"path":"/nonexistent/xyzzy"}`),
	)
})

var _ = Describe("Binary", func() {
	It("honours the override", func() {
		GinkgoT().Setenv(BinEnv, "")
		Expect(Binary()).To(Equal("llmman"))
		GinkgoT().Setenv(BinEnv, "/opt/bin/llmman")
		Expect(Binary()).To(Equal("/opt/bin/llmman"))
	})

	It("treats a blank override as a mistake, not a request to run the empty string", func() {
		GinkgoT().Setenv(BinEnv, "   ")
		Expect(Binary()).To(Equal("llmman"))
	})
})

var _ = Describe("Resolve", func() {
	It("reports a missing binary with an actionable error", func() {
		GinkgoT().Setenv(BinEnv, filepath.Join(GinkgoT().TempDir(), "definitely-not-here"))

		_, err := Resolve(context.Background(), "ref")
		Expect(err).To(MatchError(ContainSubstring("install llmman")))
	})
})
