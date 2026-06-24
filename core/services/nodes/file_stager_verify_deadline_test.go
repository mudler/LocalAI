package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The resumable-upload fast path (HEAD the worker, hash the local file, skip
// when the hashes match) is what makes resume work after a partial transfer.
// It reports ZERO upload bytes for its entire duration, so a stall window that
// watches only upload bytes cannot tell it apart from a wedged worker.
//
// Measured on the live cluster staging a 70 GB model with 56 GB already
// present: ~45s per skipped ~4 GB shard, six-plus consecutive minutes with no
// bytes uploaded at all. At the 600 GB scale this work exists to support, a
// single shard can plausibly hash for longer than the 5m stall window.

// verifyShardSize keeps each shard small enough to stay friendly to /tmp while
// still costing real, measurable hashing time.
const verifyShardSize = 16 << 20

// verifyShardCount is chosen so the cumulative hashing time across consecutive
// skipped shards comfortably outlasts the scaled-down stall window, mirroring
// the run of consecutive skips seen on the cluster.
const verifyShardCount = 6

func writeShard(path string, size int) string {
	f, err := os.Create(path)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = f.Close() }()

	h := sha256.New()
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i * 7)
	}
	for written := 0; written < size; written += len(chunk) {
		n := len(chunk)
		if remaining := size - written; remaining < n {
			n = remaining
		}
		_, err := f.Write(chunk[:n])
		Expect(err).ToNot(HaveOccurred())
		h.Write(chunk[:n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

var _ = Describe("staging verify phase and the cold-load stall window", func() {
	newStagerFor := func(srv *httptest.Server) *HTTPFileStager {
		return NewHTTPFileStager(func(string) (string, error) {
			u, err := url.Parse(srv.URL)
			if err != nil {
				return "", err
			}
			return u.Host, nil
		}, "")
	}

	It("survives a run of verified-and-skipped shards that upload no bytes at all", func() {
		tmp := GinkgoT().TempDir()
		paths := make([]string, verifyShardCount)
		hashes := make(map[string]string, verifyShardCount)
		for i := range paths {
			key := fmt.Sprintf("shard-%d", i)
			paths[i] = filepath.Join(tmp, key+".safetensors")
			hashes[key] = writeShard(paths[i], verifyShardSize)
		}

		// The worker already holds every shard from a previous attempt, so each
		// one is HEADed, hashed locally, and skipped.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodHead),
				"every shard must take the verify/skip path, never upload")
			key := filepath.Base(r.URL.Path)
			w.Header().Set(HeaderLocalPath, "/worker/models/"+key)
			w.Header().Set(HeaderContentSHA256, hashes[key])
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		// Scaled-down budgets: hashing these shards takes far longer than the
		// window, standing in for 600 GB shards against a 5m window.
		ctx, cancel := newLoadDeadlineContext(context.Background(),
			20*time.Millisecond, 20*time.Millisecond, time.Minute)
		defer cancel()

		stager := newStagerFor(srv)
		start := time.Now()
		for i, p := range paths {
			key := fmt.Sprintf("shard-%d", i)
			remotePath, err := stager.EnsureRemote(ctx, "nvidia-thor", p, key)
			Expect(err).ToNot(HaveOccurred(),
				"verifying shard %d does real work and must not be mistaken for a stall", i)
			Expect(remotePath).To(Equal("/worker/models/" + key))
		}
		Expect(time.Since(start)).To(BeNumerically(">", 20*time.Millisecond),
			"the verify run must actually have outlasted the stall window")
	})

	It("does not silently succeed on the verify path after the deadline has expired", func() {
		// The verify path consulted no context at all: it hashed to completion
		// and returned success even on a dead context, so an expired cold load
		// looked like a staged file.
		tmp := GinkgoT().TempDir()
		localPath := filepath.Join(tmp, "shard.safetensors")
		hash := writeShard(localPath, 1<<20)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(HeaderLocalPath, "/worker/models/shard")
			w.Header().Set(HeaderContentSHA256, hash)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := newStagerFor(srv).EnsureRemote(ctx, "nvidia-thor", localPath, "shard")
		Expect(err).To(HaveOccurred(),
			"a cancelled load must not report a file as staged")
	})

	It("still kills a worker that goes silent during the verify phase", func() {
		tmp := GinkgoT().TempDir()
		localPath := filepath.Join(tmp, "small.safetensors")
		writeShard(localPath, 1<<20)

		// The worker accepted the connection and then died: no HEAD response,
		// no bytes, no hashing. Nothing here is real work.
		//
		// Ordering matters: srv.Close() blocks until in-flight handlers return,
		// so the handler must be released BEFORE Close runs. Defers are LIFO.
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
		}))
		defer srv.Close()
		defer close(release)

		ctx, cancel := newLoadDeadlineContext(context.Background(),
			100*time.Millisecond, 100*time.Millisecond, time.Minute)
		defer cancel()

		start := time.Now()
		_, err := newStagerFor(srv).EnsureRemote(ctx, "dead-node", localPath, "shard")
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred(),
			"a silent worker must still be caught by the stall window")
		Expect(elapsed).To(BeNumerically("<", 10*time.Second),
			"the advisory lock must be released promptly")
	})
})
