package downloader_test

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A download interrupted mid-transfer leaves a `<file>.partial` behind. The
// next attempt has to cope with it, and for a non-http(s) scheme (every
// gallery entry uses huggingface://) it used to return an error wrapping a nil
// error, which made the model permanently uninstallable until someone deleted
// the partial by hand.
var _ = Describe("DownloadFile with a leftover .partial", func() {
	var (
		payload     []byte
		payloadSHA  string
		destDir     string
		destPath    string
		partialPath string
		noProgress  = func(string, string, string, float64) {}
	)

	// rangeServer serves payload at any path, optionally honouring Range
	// requests, and records the Range headers it received so a spec can tell
	// a resume apart from a restart.
	rangeServer := func(supportsRange bool, seenRanges *[]string, mu *sync.Mutex) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				if supportsRange {
					w.Header().Set("Accept-Ranges", "bytes")
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			rangeHeader := r.Header.Get("Range")
			if seenRanges != nil {
				mu.Lock()
				*seenRanges = append(*seenRanges, rangeHeader)
				mu.Unlock()
			}
			start := 0
			if supportsRange && strings.HasPrefix(rangeHeader, "bytes=") {
				spec := strings.TrimPrefix(rangeHeader, "bytes=")
				n, err := strconv.Atoi(strings.TrimSuffix(spec, "-"))
				Expect(err).ToNot(HaveOccurred())
				start = n
			}
			body := payload[start:]
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if start > 0 {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			_, _ = w.Write(body)
		}))
	}

	writePartial := func(content []byte) {
		Expect(os.WriteFile(partialPath, content, 0600)).To(Succeed())
	}

	expectDownloadedPayload := func() {
		got, err := os.ReadFile(destPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(payload))
		Expect(partialPath).ToNot(BeAnExistingFile())
	}

	BeforeEach(func() {
		payload = make([]byte, 16384)
		_, err := rand.Read(payload)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(payload)
		payloadSHA = fmt.Sprintf("%x", sum[:])

		destDir, err = os.MkdirTemp("", "partial-resume")
		Expect(err).ToNot(HaveOccurred())
		destPath = filepath.Join(destDir, "model.gguf")
		partialPath = destPath + PartialFileSuffix
	})

	AfterEach(func() {
		_ = os.RemoveAll(destDir)
	})

	// The reported bug: `huggingface://` is not an http(s) URI, so the resume
	// branch was skipped and the fall-through reported a stat error that had
	// not happened.
	It("restarts and produces a correct file for a non-HTTP URI", func() {
		server := rangeServer(true, nil, nil)
		defer server.Close()

		originalEndpoint := HF_ENDPOINT
		HF_ENDPOINT = server.URL
		defer func() { HF_ENDPOINT = originalEndpoint }()

		writePartial([]byte("stale bytes from an interrupted download"))

		uri := URI("huggingface://owner/repo/model.gguf")
		Expect(uri.DownloadFile(destPath, payloadSHA, 1, 1, noProgress)).To(Succeed())
		expectDownloadedPayload()
	})

	It("resumes an HTTP download when the server supports ranges", func() {
		var mu sync.Mutex
		var seenRanges []string
		server := rangeServer(true, &seenRanges, &mu)
		defer server.Close()

		writePartial(payload[:8192])

		Expect(URI(server.URL).DownloadFile(destPath, payloadSHA, 1, 1, noProgress)).To(Succeed())
		expectDownloadedPayload()

		mu.Lock()
		defer mu.Unlock()
		Expect(seenRanges).To(ContainElement("bytes=8192-"),
			"expected a Range request resuming from the partial, got %v", seenRanges)
	})

	It("restarts an HTTP download when the server does not support ranges", func() {
		var mu sync.Mutex
		var seenRanges []string
		server := rangeServer(false, &seenRanges, &mu)
		defer server.Close()

		writePartial([]byte("stale bytes"))

		Expect(URI(server.URL).DownloadFile(destPath, payloadSHA, 1, 1, noProgress)).To(Succeed())
		expectDownloadedPayload()

		mu.Lock()
		defer mu.Unlock()
		Expect(seenRanges).To(ConsistOf(""), "expected a full restart, got ranges %v", seenRanges)
	})

	It("downloads normally when no partial is present", func() {
		server := rangeServer(true, nil, nil)
		defer server.Close()

		Expect(URI(server.URL).DownloadFile(destPath, payloadSHA, 1, 1, noProgress)).To(Succeed())
		expectDownloadedPayload()
	})

	It("reports an informative error when the partial cannot be stat'd", func() {
		server := rangeServer(true, nil, nil)
		defer server.Close()

		// A symlink pointing at itself makes os.Stat fail with ELOOP: a real
		// stat failure that is not "does not exist".
		Expect(os.Symlink(partialPath, partialPath)).To(Succeed())

		err := URI(server.URL).DownloadFile(destPath, payloadSHA, 1, 1, noProgress)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(partialPath))
		Expect(err.Error()).ToNot(ContainSubstring("<nil>"))
		Expect(destPath).ToNot(BeAnExistingFile())
	})
})
