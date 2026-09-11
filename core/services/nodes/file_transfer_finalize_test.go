// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Recovering unfinished file finalization", func() {
	It("stages a full-size unfinished upload and subsequently skips the committed file", func() {
		dir := GinkgoT().TempDir()
		content := []byte("complete model bytes")
		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		remote := filepath.Join(dir, "model.bin")
		Expect(os.WriteFile(remote, content, 0600)).To(Succeed())
		Expect(os.WriteFile(remote+targetSidecarSuffix, []byte(hash), 0600)).To(Succeed())
		local := filepath.Join(GinkgoT().TempDir(), "model.bin")
		Expect(os.WriteFile(local, content, 0600)).To(Succeed())

		puts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodHead:
				handleHead(w, r, dir, "", "", "model.bin")
			case http.MethodPut:
				puts++
				handleUpload(w, r, dir, "", "", "model.bin", 0)
			}
		}))
		DeferCleanup(server.Close)
		stager := NewHTTPFileStager(func(string) (string, error) {
			return strings.TrimPrefix(server.URL, "http://"), nil
		}, "")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result, err := stager.EnsureRemote(ctx, "worker", local, "model.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(remote))
		Expect(remote + targetSidecarSuffix).NotTo(BeAnExistingFile())
		Expect(os.ReadFile(remote + hashSidecarSuffix)).To(Equal([]byte(hash)))
		Expect(os.ReadFile(remote)).To(Equal(content))
		result, err = stager.EnsureRemote(ctx, "worker", local, "model.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(remote))
		Expect(puts).To(Equal(1))
	})

	DescribeTable("validates existing bytes before acknowledging a retry",
		func(content string, expectedStatus int) {
			dir := GinkgoT().TempDir()
			remote := filepath.Join(dir, "model.bin")
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte("good")))
			Expect(os.WriteFile(remote, []byte(content), 0600)).To(Succeed())
			Expect(os.WriteFile(remote+targetSidecarSuffix, []byte(hash), 0600)).To(Succeed())
			// A cached hash must not conceal corrupt bytes left by an interrupted upload.
			Expect(os.WriteFile(remote+hashSidecarSuffix, []byte(hash), 0600)).To(Succeed())
			req := httptest.NewRequest(http.MethodPut, "/v1/files/model.bin", strings.NewReader("good"))
			req.Header.Set("Content-Range", "bytes 0-3/4")
			req.Header.Set(HeaderContentSHA256, hash)
			response := httptest.NewRecorder()
			handleUpload(response, req, dir, "", "", "model.bin", 0)
			Expect(response.Code).To(Equal(expectedStatus))
			if expectedStatus == http.StatusBadRequest {
				Expect(response.Body.String()).To(ContainSubstring("sha256 mismatch"))
				Expect(remote).NotTo(BeAnExistingFile())
				Expect(remote + targetSidecarSuffix).NotTo(BeAnExistingFile())
				Expect(remote + hashSidecarSuffix).NotTo(BeAnExistingFile())
			} else {
				Expect(os.ReadFile(remote)).To(Equal([]byte(content)))
				Expect(remote + targetSidecarSuffix).To(BeAnExistingFile())
			}
		},
		Entry("rejects corrupt full-size content", "evil", http.StatusBadRequest),
		Entry("preserves partial content for resume", "go", http.StatusRequestedRangeNotSatisfiable),
		Entry("does not finalize oversized content", "good-extra", http.StatusRequestedRangeNotSatisfiable),
	)
})
