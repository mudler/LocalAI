package modelartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

const siblingReuseRevision = "0123456789abcdef0123456789abcdef01234567"

// recordingResolver serves a fixed full file set filtered by each request's
// allow/ignore patterns, so a narrower request genuinely resolves to a strict
// subset of a broader sibling's files. The HTTP server behind it records every
// fetch, which is the signal the sibling-reuse fix is verified through. The
// function under fix is never mocked: a real Manager drives the real staging +
// commit path against this stub collaborator.
type recordingResolver struct {
	endpoint string
	repo     string
	files    []hfapi.SnapshotFile
	server   *httptest.Server

	mu      sync.Mutex
	fetched map[string]int
}

func newRecordingResolver(files []hfapi.SnapshotFile, contents map[string][]byte) *recordingResolver {
	r := &recordingResolver{
		endpoint: "https://huggingface.co",
		repo:     "owner/repo",
		files:    files,
		fetched:  map[string]int{},
	}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/file/")
		body, ok := contents[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		r.mu.Lock()
		r.fetched[name]++
		r.mu.Unlock()
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	return r
}

func (r *recordingResolver) ResolveSnapshot(_ context.Context, req hfapi.SnapshotRequest) (hfapi.Snapshot, error) {
	files, err := hfapi.FilterSnapshotFiles(r.files, req.AllowPatterns, req.IgnorePatterns)
	if err != nil {
		return hfapi.Snapshot{}, err
	}
	out := make([]hfapi.SnapshotFile, len(files))
	for i, f := range files {
		f.URL = r.server.URL + "/file/" + f.Path
		out[i] = f
	}
	return hfapi.Snapshot{
		Endpoint: r.endpoint, Repo: r.repo,
		RequestedRevision: req.Revision, ResolvedRevision: siblingReuseRevision, Files: out,
	}, nil
}

func (r *recordingResolver) fetchCount(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetched[path]
}

func (r *recordingResolver) resetFetches() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fetched = map[string]int{}
}

func siblingReuseFiles(contents map[string][]byte) []hfapi.SnapshotFile {
	paths := []string{"a/first.bin", "b/second.bin", "c/third.bin"}
	files := make([]hfapi.SnapshotFile, 0, len(paths))
	for _, p := range paths {
		sum := sha256.Sum256(contents[p])
		files = append(files, hfapi.SnapshotFile{
			Path: p, Size: int64(len(contents[p])), LFSOID: hex.EncodeToString(sum[:]),
		})
	}
	return files
}

// The narrow-request case proves the fix for #11047:
// a request with narrower allow_patterns (a strict subset) reuses files an
// already-committed broader sibling holds, hard-linking instead of re-fetching.
//
// On master this is RED: a narrower allow_patterns set hashes to a different
// CacheKey (path.go:62), so committedResult misses and materializeLocked
// re-fetches the file (fetches > 0) into a separate copy (no os.SameFile). On
// the branch it is GREEN: reuseFromCommittedSibling hits the broad sibling,
// verifies the file via verifyDownloadedFile, and hard-links it (fetches == 0,
// os.SameFile true).
var _ = Describe("committed sibling reuse", func() {
	It("reuses files from a broader committed sibling", func() {
		contents := map[string][]byte{
			"a/first.bin":  []byte("first-file-bytes"),
			"b/second.bin": []byte("second-file-bytes-longer"),
			"c/third.bin":  []byte("third-file"),
		}
		resolver := newRecordingResolver(siblingReuseFiles(contents), contents)
		defer resolver.server.Close()

		modelsPath := GinkgoT().TempDir()
		manager := modelartifacts.NewManager(resolver,
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return bypassedLocker{} }))

		// Commit the broad sibling: all three files, fetched from the resolver.
		broadSpec := modelartifacts.Spec{Source: modelartifacts.Source{
			Type: modelartifacts.SourceTypeHuggingFace, Repo: "owner/repo",
		}}
		broad, err := manager.Ensure(context.Background(), modelsPath, broadSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(broad.CacheHit).To(BeFalse())
		Expect(resolver.fetchCount("a/first.bin")).To(BeNumerically(">", 0),
			"the broad sibling must have fetched a/first.bin to commit it")

		resolver.resetFetches()

		// Narrowed request: a strict subset of the broad sibling's file set.
		narrowSpec := modelartifacts.Spec{Source: modelartifacts.Source{
			Type: modelartifacts.SourceTypeHuggingFace, Repo: "owner/repo",
			AllowPatterns: []string{"a/first.bin"},
		}}
		narrow, err := manager.Ensure(context.Background(), modelsPath, narrowSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(narrow.CacheHit).To(BeFalse())

		// (b) The sibling-present file must NOT be re-fetched: zero fetches. This is
		// the assertion that is RED on master (one fetch) and GREEN on the branch.
		Expect(resolver.fetchCount("a/first.bin")).To(Equal(0),
			"a/first.bin must be reused from the committed broad sibling, not re-fetched")

		// (a) The narrowed tree's staged file is the same inode as the broad
		// sibling's file (hard-link), not a freshly downloaded second copy. RED on
		// master (separate file), GREEN on the branch (hard-link).
		broadFile := filepath.Join(modelsPath, filepath.FromSlash(broad.RelativePath), "a", "first.bin")
		narrowFile := filepath.Join(modelsPath, filepath.FromSlash(narrow.RelativePath), "a", "first.bin")
		broadInfo, err := os.Stat(broadFile)
		Expect(err).NotTo(HaveOccurred())
		narrowInfo, err := os.Stat(narrowFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.SameFile(broadInfo, narrowInfo)).To(BeTrue(),
			"the narrowed request must hard-link the broad sibling's file rather than store a second copy")

		// The reused bytes are intact end to end.
		Expect(os.ReadFile(narrowFile)).To(Equal(contents["a/first.bin"]))
	})

	// The broader-request case is the manifest file-set guard: a broader request
	// against a narrower committed sibling must still fetch the files the sibling
	// lacks and commit a complete tree. Sibling-reuse can never serve an incomplete
	// model as complete, because each requested file is matched individually against
	// the sibling's manifest.
	It("fetches files missing from a narrower committed sibling", func() {
		contents := map[string][]byte{
			"a/first.bin":  []byte("first-file-bytes"),
			"b/second.bin": []byte("second-file-bytes-longer"),
			"c/third.bin":  []byte("third-file"),
		}
		resolver := newRecordingResolver(siblingReuseFiles(contents), contents)
		defer resolver.server.Close()

		modelsPath := GinkgoT().TempDir()
		manager := modelartifacts.NewManager(resolver,
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return bypassedLocker{} }))

		// Commit a NARROW sibling first: only a/first.bin and b/second.bin.
		narrowSpec := modelartifacts.Spec{Source: modelartifacts.Source{
			Type: modelartifacts.SourceTypeHuggingFace, Repo: "owner/repo",
			AllowPatterns: []string{"a/first.bin", "b/second.bin"},
		}}
		narrow, err := manager.Ensure(context.Background(), modelsPath, narrowSpec)
		Expect(err).NotTo(HaveOccurred())
		narrowPaths := make([]string, 0, len(narrow.Manifest.Files))
		for _, f := range narrow.Manifest.Files {
			narrowPaths = append(narrowPaths, f.Path)
		}
		Expect(narrowPaths).To(Equal([]string{"a/first.bin", "b/second.bin"}))

		resolver.resetFetches()

		// A BROADER request asks for all three files, including c/third.bin which the
		// narrow sibling does not hold.
		broadSpec := modelartifacts.Spec{Source: modelartifacts.Source{
			Type: modelartifacts.SourceTypeHuggingFace, Repo: "owner/repo",
		}}
		broad, err := manager.Ensure(context.Background(), modelsPath, broadSpec)
		Expect(err).NotTo(HaveOccurred())

		// The file the narrow sibling lacks MUST be fetched: sibling-reuse must not
		// inherit a narrower tree's gaps as if the broad request were complete.
		Expect(resolver.fetchCount("c/third.bin")).To(BeNumerically(">", 0),
			"c/third.bin is absent from the narrow sibling and must be fetched, not served as complete")

		// The broad tree's manifest file set is exactly the full set — never the
		// narrow sibling's subset. This file-set comparison proves no incomplete model
		// is ever served as complete via sibling-reuse.
		broadPaths := make([]string, 0, len(broad.Manifest.Files))
		for _, f := range broad.Manifest.Files {
			broadPaths = append(broadPaths, f.Path)
		}
		Expect(broadPaths).To(Equal([]string{"a/first.bin", "b/second.bin", "c/third.bin"}))

		// Every file is present on disk with the right bytes after commit.
		for _, p := range []string{"a/first.bin", "b/second.bin", "c/third.bin"} {
			Expect(os.ReadFile(filepath.Join(modelsPath, filepath.FromSlash(broad.RelativePath), filepath.FromSlash(p)))).
				To(Equal(contents[p]))
		}
	})
})
