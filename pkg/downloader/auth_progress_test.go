package downloader_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/downloader"
)

var _ = Describe("authenticated HTTP downloads", func() {
	It("sends bearer auth to the origin and strips it at a cross-host redirect", func() {
		var originAuth, cdnAuth string
		cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cdnAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Length", "5")
			_, _ = w.Write([]byte("model"))
		}))
		DeferCleanup(cdn.Close)

		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originAuth = r.Header.Get("Authorization")
			http.Redirect(w, r, cdn.URL+"/weights", http.StatusTemporaryRedirect)
		}))
		DeferCleanup(origin.Close)

		target := filepath.Join(GinkgoT().TempDir(), "weights.bin")
		err := downloader.URI(origin.URL+"/resolve").DownloadFileWithContext(
			context.Background(),
			target,
			"",
			0,
			1,
			nil,
			downloader.WithBearerToken("secret-token"),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(originAuth).To(Equal("Bearer secret-token"))
		Expect(cdnAuth).To(BeEmpty())
	})

	It("authenticates range probes and reports resumed bytes", func() {
		var mu sync.Mutex
		seen := make(map[string]string)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[r.Method] = r.Header.Get("Authorization")
			mu.Unlock()
			if r.Method == http.MethodHead {
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Content-Length", "6")
				return
			}
			Expect(r.Header.Get("Range")).To(Equal("bytes=3-"))
			w.Header().Set("Content-Length", "3")
			w.Header().Set("Content-Range", "bytes 3-5/6")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("def"))
		}))
		DeferCleanup(server.Close)

		target := filepath.Join(GinkgoT().TempDir(), "resume.bin")
		Expect(os.WriteFile(target+".partial", []byte("abc"), 0o600)).To(Succeed())
		events := []downloader.TransferProgress{}
		err := downloader.URI(server.URL+"/weights").DownloadFileWithContext(
			context.Background(),
			target,
			"",
			0,
			1,
			nil,
			downloader.WithBearerToken("resume-token"),
			downloader.WithTransferProgress(func(event downloader.TransferProgress) {
				events = append(events, event)
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(HaveKeyWithValue(http.MethodHead, "Bearer resume-token"))
		Expect(seen).To(HaveKeyWithValue(http.MethodGet, "Bearer resume-token"))
		Expect(events).NotTo(BeEmpty())
		Expect(events[len(events)-1].Written).To(Equal(int64(6)))
		Expect(events[len(events)-1].Total).To(Equal(int64(6)))
		Expect(os.ReadFile(target)).To(Equal([]byte("abcdef")))
	})

	It("rejects an origin that ignores a resume range", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Content-Length", "6")
				return
			}
			Expect(r.Header.Get("Range")).To(Equal("bytes=3-"))
			w.Header().Set("Content-Length", "6")
			_, _ = w.Write([]byte("abcdef"))
		}))
		DeferCleanup(server.Close)

		target := filepath.Join(GinkgoT().TempDir(), "ignored-range.bin")
		Expect(os.WriteFile(target+".partial", []byte("abc"), 0o600)).To(Succeed())
		err := downloader.URI(server.URL).DownloadFileWithContext(
			context.Background(),
			target,
			"",
			0,
			1,
			nil,
		)
		Expect(err).To(MatchError(ContainSubstring("status 200 instead of 206")))
		Expect(target).NotTo(BeAnExistingFile())
		Expect(target + ".partial").NotTo(BeAnExistingFile())
	})

	It("creates resumable partial files with owner-only permissions", func() {
		ctx, cancel := context.WithCancel(context.Background())
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "5")
			_, _ = w.Write([]byte("model"))
		}))
		DeferCleanup(server.Close)

		target := filepath.Join(GinkgoT().TempDir(), "private.bin")
		err := downloader.URI(server.URL).DownloadFileWithContext(
			ctx,
			target,
			"",
			0,
			1,
			nil,
			downloader.WithTransferProgress(func(downloader.TransferProgress) {
				cancel()
			}),
		)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		info, statErr := os.Stat(target + ".partial")
		Expect(statErr).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("keeps the legacy total empty when the response length is unknown", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = w.Write([]byte("model"))
		}))
		DeferCleanup(server.Close)

		target := filepath.Join(GinkgoT().TempDir(), "unknown-size.bin")
		totals := []string{}
		err := downloader.URI(server.URL).DownloadFileWithContext(
			context.Background(),
			target,
			"",
			0,
			1,
			func(_, _, total string, _ float64) {
				totals = append(totals, total)
			},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(totals).NotTo(BeEmpty())
		Expect(totals[len(totals)-1]).To(BeEmpty())
	})
})
