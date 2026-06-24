package oci

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// droppingBlobRegistry emulates how quay.io serves layer blobs from S3/Akamai
// with a short-lived pre-signed URL: a full-blob GET on a slow connection is
// always cut off mid-transfer, so a client that restarts from byte zero can
// never complete the download. Only a client that resumes with a Range request
// (like docker pull does) receives the remaining bytes and can finish.
type droppingBlobRegistry struct {
	inner http.Handler

	mu            sync.Mutex
	rangeRequests []int64
	fullRequests  int
}

// dropThreshold separates real layer blobs from small metadata blobs (image
// config), which are served untouched.
const dropThreshold = 1024

func (h *droppingBlobRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/blobs/sha256:") {
		h.inner.ServeHTTP(w, r)
		return
	}

	// Fetch the full blob from the inner registry (which does not speak
	// Range) and apply the Range semantics here.
	inner := r.Clone(r.Context())
	inner.Header.Del("Range")
	rec := httptest.NewRecorder()
	h.inner.ServeHTTP(rec, inner)
	body := rec.Body.Bytes()
	if rec.Code != http.StatusOK || len(body) <= dropThreshold {
		for k, vv := range rec.Header() {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
		return
	}

	if rh := r.Header.Get("Range"); rh != "" {
		offset, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rh, "bytes="), "-"), 10, 64)
		if err != nil || offset < 0 || offset >= int64(len(body)) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		h.mu.Lock()
		h.rangeRequests = append(h.rangeRequests, offset)
		h.mu.Unlock()
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(body)-1, len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)-int(offset)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[offset:])
		return
	}

	h.mu.Lock()
	h.fullRequests++
	h.mu.Unlock()

	// Announce the full size but deliver only half, then sever the
	// connection, like a pre-signed URL expiring mid-download.
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body[:len(body)/2])
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	panic(http.ErrAbortHandler)
}

var _ = Describe("DownloadOCIImageTar resume", func() {
	var (
		server      *httptest.Server
		reg         *droppingBlobRegistry
		tmpDir      string
		restoreWait func()
	)

	BeforeEach(func() {
		reg = &droppingBlobRegistry{inner: registry.New()}
		server = httptest.NewServer(reg)

		var err error
		tmpDir, err = os.MkdirTemp("", "oci-resume-e2e-*")
		Expect(err).NotTo(HaveOccurred())

		prev := layerRetryBackoff
		layerRetryBackoff = func(int) time.Duration { return 0 }
		restoreWait = func() { layerRetryBackoff = prev }
	})

	AfterEach(func() {
		restoreWait()
		server.Close()
		_ = os.RemoveAll(tmpDir)
	})

	It("completes the download by resuming interrupted layer transfers with Range requests", func() {
		img, err := random.Image(4096, 1)
		Expect(err).NotTo(HaveOccurred())

		imageRef := strings.TrimPrefix(server.URL, "http://") + "/testrepo/backend:latest"
		ref, err := name.ParseReference(imageRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(remote.Write(ref, img)).To(Succeed())

		pulled, err := GetImage(imageRef, "", nil, nil)
		Expect(err).NotTo(HaveOccurred())

		tarPath := filepath.Join(tmpDir, "image.tar")
		err = DownloadOCIImageTar(context.Background(), pulled, imageRef, tarPath, nil)
		Expect(err).NotTo(HaveOccurred())

		// The full-blob attempt was cut off, so success is only possible
		// through at least one Range request picking up where it stopped.
		reg.mu.Lock()
		defer reg.mu.Unlock()
		Expect(reg.rangeRequests).NotTo(BeEmpty())
		for _, off := range reg.rangeRequests {
			Expect(off).To(BeNumerically(">", 0))
		}

		fi, err := os.Stat(tarPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(fi.Size()).To(BeNumerically(">", 0))
	})
})
