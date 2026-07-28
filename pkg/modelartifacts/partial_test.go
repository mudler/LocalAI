package modelartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

const (
	testCacheKey  = "b79e23e0b9c50af094d627582df30109eff8637438864172d64be07dfc5a98f9"
	testWriterID  = "00112233445566aa"
	otherWriterID = "ffeeddccbbaa9988"
)

// backdate rewinds every mtime in tree so it looks abandoned. The sweep and the
// adoption scan both read the newest mtime anywhere inside the tree, not the
// directory's own, so every entry has to move.
func backdate(tree string, age time.Duration) {
	when := time.Now().Add(-age)
	Expect(filepath.Walk(tree, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, when, when)
	})).To(Succeed())
}

func partialRoot(modelsPath string) string {
	return filepath.Join(modelsPath, ".artifacts", ".partial")
}

// stageOrphan writes a partial tree as if a writer had died holding it, with
// blob carrying however many bytes of file it had managed to fetch.
func stageOrphan(modelsPath, writerID, hubPath string, blob []byte) string {
	tree := filepath.Join(partialRoot(modelsPath), testCacheKey+"."+writerID)
	nameSum := sha256.Sum256([]byte(hubPath))
	downloads := filepath.Join(tree, ".downloads")
	Expect(os.MkdirAll(downloads, 0o750)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(tree, "snapshot"), 0o750)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(downloads, hex.EncodeToString(nameSum[:])+".partial"), blob, 0o600)).To(Succeed())
	return tree
}

var _ = Describe("writer-unique artifact partial trees", func() {
	It("gives each writer its own staging path and rejects a malformed identity", func() {
		spec := modelartifacts.Spec{
			Source:   modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"},
			Resolved: &modelartifacts.Resolved{Endpoint: "https://huggingface.co", Revision: "0123456789abcdef0123456789abcdef01234567", CacheKey: testCacheKey},
		}
		layout, err := modelartifacts.LayoutFor("/models", spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Partial).To(BeEmpty(), "a layout without a writer identity must not name a staging tree")

		mine, err := layout.WithWriter(testWriterID)
		Expect(err).NotTo(HaveOccurred())
		theirs, err := layout.WithWriter(otherWriterID)
		Expect(err).NotTo(HaveOccurred())
		Expect(mine.Partial).To(Equal(filepath.Join("/models", ".artifacts", ".partial", testCacheKey+"."+testWriterID)))
		Expect(mine.Partial).NotTo(Equal(theirs.Partial))

		// A writer identity reaches the filesystem as a path component, so
		// anything that is not the exact drawn shape is refused rather than
		// sanitised.
		for _, bad := range []string{"", "../escape", "not-hex", strings.Repeat("a", 17)} {
			_, err := layout.WithWriter(bad)
			Expect(err).To(MatchError(ContainSubstring("invalid artifact writer id")), "accepted writer id %q", bad)
		}
	})

	Describe("reclaiming abandoned trees", func() {
		var modelsPath string

		BeforeEach(func() {
			modelsPath = GinkgoT().TempDir()
			Expect(os.MkdirAll(partialRoot(modelsPath), 0o750)).To(Succeed())
		})

		It("removes a tree nothing has touched for the whole window", func() {
			tree := stageOrphan(modelsPath, otherWriterID, "model.safetensors", []byte("half"))
			backdate(tree, 48*time.Hour)

			removed, err := modelartifacts.SweepStalePartialTrees(modelsPath, modelartifacts.PartialOrphanTTL, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(removed).To(Equal(1))
			Expect(tree).NotTo(BeADirectory())
		})

		// This is the property that keeps the fix from becoming a data-loss
		// bug: a running download writes continuously, so the freshness of the
		// bytes inside the tree - not the directory's own mtime, which nothing
		// updates - is what proves the writer is still alive.
		It("never removes a tree whose contents are still being written", func() {
			tree := stageOrphan(modelsPath, otherWriterID, "model.safetensors", []byte("half"))
			// An old directory with a fresh blob inside is exactly what a
			// long-running download looks like.
			backdate(tree, 48*time.Hour)
			now := time.Now()
			blob := filepath.Join(tree, ".downloads",
				hex.EncodeToString(func() []byte { sum := sha256.Sum256([]byte("model.safetensors")); return sum[:] }()))
			Expect(os.Chtimes(blob+".partial", now, now)).To(Succeed())

			removed, err := modelartifacts.SweepStalePartialTrees(modelsPath, modelartifacts.PartialOrphanTTL, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(removed).To(BeZero())
			Expect(tree).To(BeADirectory())
		})

		It("never removes the caller's own tree, however old it looks", func() {
			tree := stageOrphan(modelsPath, testWriterID, "model.safetensors", []byte("half"))
			backdate(tree, 48*time.Hour)

			removed, err := modelartifacts.SweepStalePartialTrees(modelsPath, modelartifacts.PartialOrphanTTL, tree)
			Expect(err).NotTo(HaveOccurred())
			Expect(removed).To(BeZero())
			Expect(tree).To(BeADirectory())
		})

		It("never removes a directory it did not create", func() {
			stranger := filepath.Join(partialRoot(modelsPath), "not-an-artifact")
			Expect(os.MkdirAll(stranger, 0o750)).To(Succeed())
			backdate(stranger, 48*time.Hour)

			removed, err := modelartifacts.SweepStalePartialTrees(modelsPath, modelartifacts.PartialOrphanTTL, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(removed).To(BeZero())
			Expect(stranger).To(BeADirectory())
		})

		It("treats a missing partial root as nothing to do", func() {
			removed, err := modelartifacts.SweepStalePartialTrees(GinkgoT().TempDir(), modelartifacts.PartialOrphanTTL, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(removed).To(BeZero())
		})
	})

	// Writer-unique staging would otherwise cost the resume a restarted replica
	// depends on to ever finish a tens-of-gigabytes repo: its predecessor's
	// bytes sit under a writer id it will never generate again.
	It("adopts an abandoned tree for the same artifact and resumes from its bytes", func() {
		payload := []byte(strings.Repeat("artifact-bytes-", 512))
		sum := sha256.Sum256(payload)
		already := len(payload) / 2

		var servedFrom atomic.Int64
		servedFrom.Store(-1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Accept-Ranges", "bytes")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			start := int64(0)
			if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start); err != nil {
				start = 0
			}
			servedFrom.Store(start)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start:])
		}))
		DeferCleanup(server.Close)

		modelsPath := GinkgoT().TempDir()
		spec := modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}
		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			RequestedRevision: "main", ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files: []hfapi.SnapshotFile{{
				Path: "model.safetensors", Size: int64(len(payload)),
				LFSOID: hex.EncodeToString(sum[:]), URL: server.URL + "/model",
			}},
		}}
		key, err := modelartifacts.CacheKey(modelartifacts.Spec{
			Source:   spec.Source,
			Resolved: &modelartifacts.Resolved{Endpoint: "https://huggingface.co", Revision: "0123456789abcdef0123456789abcdef01234567"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal(testCacheKey), "the staged orphan must belong to the artifact under test")

		orphan := stageOrphan(modelsPath, otherWriterID, "model.safetensors", payload[:already])
		// Only a tree idle for the adoption window is claimable: the artifact
		// lock already proves a peer that respects it is not writing, and this
		// is the second line of defence for when the lock does not exclude.
		backdate(orphan, time.Hour)

		result, err := modelartifacts.NewManager(resolver).Ensure(context.Background(), modelsPath, spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(servedFrom.Load()).To(Equal(int64(already)), "the adopted partial's bytes were re-downloaded instead of resumed")
		Expect(os.ReadFile(filepath.Join(modelsPath, filepath.FromSlash(result.RelativePath), "model.safetensors"))).To(Equal(payload))
		Expect(orphan).NotTo(BeADirectory(), "adoption must move the tree, not copy it")
	})

	It("leaves a freshly written tree for its owner instead of adopting it", func() {
		payload := []byte("weight-bytes")
		sum := sha256.Sum256(payload)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		}))
		DeferCleanup(server.Close)

		modelsPath := GinkgoT().TempDir()
		// No backdating: this stands for a peer that is mid-download right now.
		orphan := stageOrphan(modelsPath, otherWriterID, "model.safetensors", []byte("partial"))

		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			RequestedRevision: "main", ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files: []hfapi.SnapshotFile{{
				Path: "model.safetensors", Size: int64(len(payload)),
				LFSOID: hex.EncodeToString(sum[:]), URL: server.URL + "/model",
			}},
		}}
		_, err := modelartifacts.NewManager(resolver).Ensure(context.Background(), modelsPath,
			modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(orphan).To(BeADirectory(), "a live peer's staging tree must survive another writer's materialization")
	})
})
