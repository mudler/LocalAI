// SPDX-License-Identifier: MIT

package testresources_test

import (
	"crypto/sha256"
	"fmt"
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
})
