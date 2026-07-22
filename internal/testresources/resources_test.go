// SPDX-License-Identifier: MIT

package testresources_test

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/internal/testresources"
)

var _ = Describe("Declared test resources", func() {
	It("rejects mutable and unpinned resources", func() {
		manifest := testresources.Manifest{
			Version: testresources.ManifestVersion,
			Target:  "backend",
			Images:  []testresources.OCIImage{{Reference: "postgres:latest", SHA256: fmt.Sprintf("%064d", 0)}},
		}
		Expect(manifest.Validate()).To(MatchError(ContainSubstring("digest-pinned")))
	})

	It("requires HTTPS and unique mirrors", func() {
		digest := fmt.Sprintf("%064d", 0)
		manifest := testresources.Manifest{Version: 1, Target: "fixture", Files: []testresources.File{{
			URL: "https://primary.invalid/file", Mirrors: []string{"http://mirror.invalid/file"},
			SHA256: digest, Destination: "file",
		}}}
		Expect(manifest.Validate()).To(MatchError(ContainSubstring("mirror must use HTTPS")))
		manifest.Files[0].Mirrors = []string{"https://mirror.invalid/file", "https://mirror.invalid/file"}
		Expect(manifest.Validate()).To(MatchError(ContainSubstring("duplicate mirror")))
	})

	It("fails before tests when a CAS blob is missing or corrupt", func() {
		cache := GinkgoT().TempDir()
		digest := fmt.Sprintf("%064d", 0)
		_, err := testresources.VerifyBlob(cache, digest)
		Expect(err).To(MatchError(ContainSubstring("missing CAS blob")))

		path := testresources.BlobPath(cache, digest)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, []byte("corrupt"), 0o644)).To(Succeed())
		_, err = testresources.VerifyBlob(cache, digest)
		Expect(err).To(MatchError(ContainSubstring("corrupt CAS blob")))
	})

	It("accepts and verifies a content-addressed blob", func() {
		cache := GinkgoT().TempDir()
		content := []byte("offline fixture")
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		path := testresources.BlobPath(cache, digest)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, content, 0o644)).To(Succeed())
		Expect(testresources.VerifyBlob(cache, digest)).To(Equal(path))
	})

	It("persists response metadata and replays a verified body", func() {
		cache := GinkgoT().TempDir()
		content := []byte("cached response")
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		path := testresources.BlobPath(cache, digest)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, content, 0o644)).To(Succeed())
		index := map[string]testresources.HTTPEntry{
			"GET https://example.invalid/data": {
				Digest: digest, Size: int64(len(content)), Status: http.StatusPartialContent,
				Header: http.Header{"Content-Range": {"bytes 0-14/15"}},
			},
		}
		Expect(testresources.WriteHTTPIndex(cache, index)).To(Succeed())
		loaded, err := testresources.LoadHTTPIndex(cache)
		Expect(err).NotTo(HaveOccurred())
		recorder := httptest.NewRecorder()
		Expect(testresources.ReplayResponse(recorder, cache, loaded["GET https://example.invalid/data"])).To(Succeed())
		Expect(recorder.Code).To(Equal(http.StatusPartialContent))
		Expect(recorder.Body.Bytes()).To(Equal(content))
		Expect(recorder.Header().Get("Content-Range")).To(Equal("bytes 0-14/15"))
	})

	It("sanitizes connection-specific response headers", func() {
		header := http.Header{"Transfer-Encoding": {"chunked"}, "Authorization": {"secret"}, "X-Fixture": {"yes"}}
		clean := testresources.SanitizeHeaders(header)
		Expect(clean).NotTo(HaveKey("Transfer-Encoding"))
		Expect(clean).To(HaveKeyWithValue("Authorization", []string{"secret"}))
		Expect(clean).To(HaveKeyWithValue("X-Fixture", []string{"yes"}))
	})

	It("keys range and authorization variants without storing credentials", func() {
		header := http.Header{"Authorization": {"Bearer secret"}, "Range": {"bytes=4-"}}
		key := testresources.RequestKey(http.MethodGet, "https://example.invalid/model", header)
		Expect(key).To(ContainSubstring("range:bytes=4-"))
		Expect(key).To(ContainSubstring("authorization:sha256:"))
		Expect(key).NotTo(ContainSubstring("Bearer secret"))
		Expect(key).NotTo(Equal(testresources.RequestKey(http.MethodGet, "https://example.invalid/model")))
	})

	It("packs deterministically and restores a target cache", func() {
		cache := GinkgoT().TempDir()
		content := []byte("bundle fixture")
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		path := testresources.BlobPath(cache, digest)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, content, 0o644)).To(Succeed())
		manifest := testresources.Manifest{Version: 1, Target: "fixture", Files: []testresources.File{{URL: "https://example.invalid/file", SHA256: digest, Destination: "file"}}}
		first := filepath.Join(GinkgoT().TempDir(), "first.tar.gz")
		second := filepath.Join(GinkgoT().TempDir(), "second.tar.gz")
		firstDigest, err := testresources.PackBundle(cache, first, manifest)
		Expect(err).NotTo(HaveOccurred())
		secondDigest, err := testresources.PackBundle(cache, second, manifest)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondDigest).To(Equal(firstDigest))
		compressed, err := os.ReadFile(first)
		Expect(err).NotTo(HaveOccurred())
		Expect(compressed[:2]).To(Equal([]byte{0x1f, 0x8b}))

		restored := GinkgoT().TempDir()
		Expect(testresources.RestoreBundle(restored, first, firstDigest)).To(Succeed())
		Expect(os.ReadFile(testresources.BlobPath(restored, digest))).To(Equal(content))
	})
})
