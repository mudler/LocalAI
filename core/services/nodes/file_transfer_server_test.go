package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FileTransferServer", func() {
	setupTestServer := func(token string, maxUploadSize int64) (*httptest.Server, string, string, string) {
		stagingDir := GinkgoT().TempDir()
		modelsDir := GinkgoT().TempDir()
		dataDir := GinkgoT().TempDir()

		mux := http.NewServeMux()

		mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
			if !checkBearerToken(r, token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			key := strings.TrimPrefix(r.URL.Path, "/v1/files/")
			switch r.Method {
			case http.MethodHead:
				handleHead(w, r, stagingDir, modelsDir, dataDir, key)
			case http.MethodPut:
				handleUpload(w, r, stagingDir, modelsDir, dataDir, key, maxUploadSize)
			case http.MethodGet:
				handleDownload(w, r, stagingDir, modelsDir, dataDir, key)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		ts := httptest.NewServer(mux)
		DeferCleanup(ts.Close)

		return ts, stagingDir, modelsDir, dataDir
	}

	Describe("Upload and Download", func() {
		It("round-trips file content correctly", func() {
			ts, _, _, _ := setupTestServer("secret-token", 0)

			content := "hello distributed world"

			// Upload
			req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/myfile.txt", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer secret-token")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			// Download
			req, err = http.NewRequest(http.MethodGet, ts.URL+"/v1/files/myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer secret-token")
			resp, err = http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			body, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(Equal(content))
		})
	})

	Describe("MaxBytesReader", func() {
		It("rejects oversized uploads", func() {
			ts, _, _, _ := setupTestServer("tok", 100)

			bigPayload := bytes.Repeat([]byte("x"), 200)
			req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/big.bin", bytes.NewReader(bigPayload))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()

			Expect(resp.StatusCode).ToNot(Equal(http.StatusOK))
		})
	})

	Describe("Path Traversal Prevention", func() {
		It("rejects path traversal via validatePathInDir", func() {
			stagingDir := GinkgoT().TempDir()
			err := validatePathInDir(filepath.Join(stagingDir, "..", "..", "etc", "passwd"), stagingDir)
			Expect(err).To(HaveOccurred())
		})

		It("accepts clean paths within the directory", func() {
			stagingDir := GinkgoT().TempDir()
			err := validatePathInDir(filepath.Join(stagingDir, "subdir", "file.txt"), stagingDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects symlinks pointing outside the directory", func() {
			stagingDir := GinkgoT().TempDir()
			outsideDir := GinkgoT().TempDir()
			outsideFile := filepath.Join(outsideDir, "secret.txt")
			err := os.WriteFile(outsideFile, []byte("secret"), 0644)
			Expect(err).ToNot(HaveOccurred())

			symlink := filepath.Join(stagingDir, "link")
			err = os.Symlink(outsideFile, symlink)
			if err != nil {
				Skip("symlink creation not supported on this platform")
			}

			err = validatePathInDir(symlink, stagingDir)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("resolveKeyToDir", func() {
		tests := []struct {
			key         string
			wantDir     string
			wantRelName string
		}{
			{"models/foo", "/tmp/models", "foo"},
			{"models/subdir/bar.bin", "/tmp/models", "subdir/bar.bin"},
			{"data/bar", "/tmp/data", "bar"},
			{"data/nested/file.txt", "/tmp/data", "nested/file.txt"},
			{"other", "/tmp/staging", "other"},
			{"random/path/file.gguf", "/tmp/staging", "random/path/file.gguf"},
		}

		for _, tt := range tests {
			tt := tt
			It("resolves "+tt.key+" correctly", func() {
				gotDir, gotRel := resolveKeyToDir(tt.key, "/tmp/staging", "/tmp/models", "/tmp/data")
				Expect(gotDir).To(Equal(tt.wantDir))
				Expect(gotRel).To(Equal(tt.wantRelName))
			})
		}
	})

	Describe("Bearer Token Auth", func() {
		var (
			ts      *httptest.Server
			content string
		)

		BeforeEach(func() {
			ts, _, _, _ = setupTestServer("correct-token", 0)
			content = "auth test content"

			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/auth-test.txt", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer correct-token")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()
			Expect(putResp.StatusCode).To(Equal(http.StatusOK))
		})

		It("rejects requests with no token", func() {
			req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/files/auth-test.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("rejects requests with wrong token", func() {
			req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/files/auth-test.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer wrong-token")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("accepts requests with correct token and returns content", func() {
			req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/files/auth-test.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer correct-token")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			body, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(Equal(content))
		})
	})

	// --- HEAD handler tests ---

	Describe("HEAD probe", func() {
		It("returns 404 for non-existent file", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			req, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/missing.bin", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("returns 200 with hash, size, and path for an uploaded file", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			content := "hello distributed world"
			expectedHash := sha256Hex([]byte(content))

			// Upload first
			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/probe.txt", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer tok")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()
			Expect(putResp.StatusCode).To(Equal(http.StatusOK))

			// HEAD should return hash + size + path
			headReq, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/probe.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			headReq.Header.Set("Authorization", "Bearer tok")
			headResp, err := http.DefaultClient.Do(headReq)
			Expect(err).ToNot(HaveOccurred())
			headResp.Body.Close()
			Expect(headResp.StatusCode).To(Equal(http.StatusOK))
			Expect(headResp.Header.Get(HeaderContentSHA256)).To(Equal(expectedHash))
			Expect(headResp.Header.Get(HeaderFileSize)).To(Equal("23"))
			Expect(headResp.Header.Get(HeaderLocalPath)).ToNot(BeEmpty())
		})

		It("routes models/ prefixed keys to modelsDir", func() {
			ts, _, modelsDir, _ := setupTestServer("tok", 0)

			content := "model data"

			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/models/test/w.bin", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer tok")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()

			headReq, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/models/test/w.bin", nil)
			Expect(err).ToNot(HaveOccurred())
			headReq.Header.Set("Authorization", "Bearer tok")
			headResp, err := http.DefaultClient.Do(headReq)
			Expect(err).ToNot(HaveOccurred())
			headResp.Body.Close()
			Expect(headResp.StatusCode).To(Equal(http.StatusOK))
			Expect(headResp.Header.Get(HeaderLocalPath)).To(HavePrefix(modelsDir))
		})

		It("computes and caches hash for pre-existing file without sidecar", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			// Write file directly (no upload, no sidecar)
			content := []byte("pre-existing content")
			err := os.WriteFile(filepath.Join(stagingDir, "legacy.bin"), content, 0644)
			Expect(err).ToNot(HaveOccurred())

			expectedHash := sha256Hex(content)

			headReq, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/legacy.bin", nil)
			Expect(err).ToNot(HaveOccurred())
			headReq.Header.Set("Authorization", "Bearer tok")
			headResp, err := http.DefaultClient.Do(headReq)
			Expect(err).ToNot(HaveOccurred())
			headResp.Body.Close()
			Expect(headResp.StatusCode).To(Equal(http.StatusOK))
			Expect(headResp.Header.Get(HeaderContentSHA256)).To(Equal(expectedHash))

			// Sidecar should now be cached
			sidecar, err := os.ReadFile(filepath.Join(stagingDir, "legacy.bin.sha256"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(sidecar)).To(Equal(expectedHash))
		})

		It("returns updated hash after re-upload with different content", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			// Upload v1
			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/changing.bin", strings.NewReader("version1"))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer tok")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()

			hash1 := sha256Hex([]byte("version1"))

			headReq, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/changing.bin", nil)
			Expect(err).ToNot(HaveOccurred())
			headReq.Header.Set("Authorization", "Bearer tok")
			headResp, err := http.DefaultClient.Do(headReq)
			Expect(err).ToNot(HaveOccurred())
			headResp.Body.Close()
			Expect(headResp.Header.Get(HeaderContentSHA256)).To(Equal(hash1))

			// Re-upload v2
			putReq2, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/changing.bin", strings.NewReader("version2"))
			Expect(err).ToNot(HaveOccurred())
			putReq2.Header.Set("Authorization", "Bearer tok")
			putResp2, err := http.DefaultClient.Do(putReq2)
			Expect(err).ToNot(HaveOccurred())
			putResp2.Body.Close()

			hash2 := sha256Hex([]byte("version2"))
			Expect(hash2).ToNot(Equal(hash1))

			headReq2, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/changing.bin", nil)
			Expect(err).ToNot(HaveOccurred())
			headReq2.Header.Set("Authorization", "Bearer tok")
			headResp2, err := http.DefaultClient.Do(headReq2)
			Expect(err).ToNot(HaveOccurred())
			headResp2.Body.Close()
			Expect(headResp2.Header.Get(HeaderContentSHA256)).To(Equal(hash2))
		})

		It("enforces bearer token auth on HEAD", func() {
			ts, _, _, _ := setupTestServer("secret", 0)

			content := "authed"
			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/auth-head.txt", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer secret")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()

			// No token
			req, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/auth-head.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

			// Wrong token
			req2, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/auth-head.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			req2.Header.Set("Authorization", "Bearer wrong")
			resp2, err := http.DefaultClient.Do(req2)
			Expect(err).ToNot(HaveOccurred())
			resp2.Body.Close()
			Expect(resp2.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("rejects path traversal on HEAD", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			// Place a file that a traversal path might resolve to
			target := filepath.Join(filepath.Dir(stagingDir), "escape.txt")
			err := os.WriteFile(target, []byte("secret"), 0644)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { os.Remove(target) })

			// Use a raw request to prevent Go's URL cleaning from collapsing ".."
			req, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/../escape.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			// Go's HTTP mux cleans the URL, so the request becomes /v1/files/escape.txt
			// which resolves inside stagingDir. The traversal is handled by validatePathInDir.
			// A direct "../" in the key is caught as 400, but Go cleans it first.
			// Either 400 (traversal blocked) or 404 (file not in staging) is acceptable.
			Expect(resp.StatusCode).To(SatisfyAny(
				Equal(http.StatusBadRequest),
				Equal(http.StatusNotFound),
			))
		})
	})

	// --- Upload sidecar tests ---

	Describe("Upload hash sidecar", func() {
		It("creates a .sha256 sidecar alongside uploaded file", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			content := "sidecar test"
			expectedHash := sha256Hex([]byte(content))

			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/sidecar.txt", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer tok")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()
			Expect(putResp.StatusCode).To(Equal(http.StatusOK))

			sidecar, err := os.ReadFile(filepath.Join(stagingDir, "sidecar.txt.sha256"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(sidecar)).To(Equal(expectedHash))
		})

		It("creates sidecar under modelsDir for models/ prefix", func() {
			ts, _, modelsDir, _ := setupTestServer("tok", 0)

			content := "model sidecar test"
			expectedHash := sha256Hex([]byte(content))

			putReq, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/models/m1/w.bin", strings.NewReader(content))
			Expect(err).ToNot(HaveOccurred())
			putReq.Header.Set("Authorization", "Bearer tok")
			putResp, err := http.DefaultClient.Do(putReq)
			Expect(err).ToNot(HaveOccurred())
			putResp.Body.Close()

			sidecar, err := os.ReadFile(filepath.Join(modelsDir, "m1", "w.bin.sha256"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(sidecar)).To(Equal(expectedHash))
		})
	})

	// --- EnsureRemote skip tests ---

	Describe("EnsureRemote skip-if-exists", func() {
		It("skips upload when file exists with matching hash", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			content := []byte("already on worker")
			expectedHash := sha256Hex(content)

			// Pre-place file and sidecar on the "worker"
			err := os.WriteFile(filepath.Join(stagingDir, "present.bin"), content, 0644)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(stagingDir, "present.bin.sha256"), []byte(expectedHash), 0644)
			Expect(err).ToNot(HaveOccurred())

			// Create matching local file
			localDir := GinkgoT().TempDir()
			localPath := filepath.Join(localDir, "present.bin")
			err = os.WriteFile(localPath, content, 0644)
			Expect(err).ToNot(HaveOccurred())

			addr := strings.TrimPrefix(ts.URL, "http://")
			stager := NewHTTPFileStager(func(nodeID string) (string, error) {
				return addr, nil
			}, "tok")

			remotePath, err := stager.EnsureRemote(context.Background(), "node-1", localPath, "present.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(remotePath).To(Equal(filepath.Join(stagingDir, "present.bin")))
		})

		It("uploads when file exists but hash differs", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			oldContent := []byte("old version")
			oldHash := sha256Hex(oldContent)

			// Pre-place old file and sidecar
			err := os.WriteFile(filepath.Join(stagingDir, "changed.bin"), oldContent, 0644)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(stagingDir, "changed.bin.sha256"), []byte(oldHash), 0644)
			Expect(err).ToNot(HaveOccurred())

			// Create local file with different content
			newContent := []byte("new version")
			localDir := GinkgoT().TempDir()
			localPath := filepath.Join(localDir, "changed.bin")
			err = os.WriteFile(localPath, newContent, 0644)
			Expect(err).ToNot(HaveOccurred())

			addr := strings.TrimPrefix(ts.URL, "http://")
			stager := NewHTTPFileStager(func(nodeID string) (string, error) {
				return addr, nil
			}, "tok")

			remotePath, err := stager.EnsureRemote(context.Background(), "node-1", localPath, "changed.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(remotePath).ToNot(BeEmpty())

			// Verify new content was uploaded
			uploaded, err := os.ReadFile(filepath.Join(stagingDir, "changed.bin"))
			Expect(err).ToNot(HaveOccurred())
			Expect(uploaded).To(Equal(newContent))

			// Verify sidecar was updated
			sidecar, err := os.ReadFile(filepath.Join(stagingDir, "changed.bin.sha256"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(sidecar)).To(Equal(sha256Hex(newContent)))
		})

		It("uploads when HEAD returns 404", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			content := []byte("fresh upload")
			localDir := GinkgoT().TempDir()
			localPath := filepath.Join(localDir, "new.bin")
			err := os.WriteFile(localPath, content, 0644)
			Expect(err).ToNot(HaveOccurred())

			addr := strings.TrimPrefix(ts.URL, "http://")
			stager := NewHTTPFileStager(func(nodeID string) (string, error) {
				return addr, nil
			}, "tok")

			remotePath, err := stager.EnsureRemote(context.Background(), "node-1", localPath, "new.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(remotePath).ToNot(BeEmpty())

			uploaded, err := os.ReadFile(filepath.Join(stagingDir, "new.bin"))
			Expect(err).ToNot(HaveOccurred())
			Expect(uploaded).To(Equal(content))
		})

		It("falls through to upload when HEAD returns 405 (old server)", func() {
			// Set up a server that does NOT support HEAD (simulates old server)
			stagingDir := GinkgoT().TempDir()
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
				key := strings.TrimPrefix(r.URL.Path, "/v1/files/")
				switch r.Method {
				case http.MethodPut:
					handleUpload(w, r, stagingDir, "", "", key, 0)
				default:
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				}
			})
			ts := httptest.NewServer(mux)
			DeferCleanup(ts.Close)

			content := []byte("should still upload")
			localDir := GinkgoT().TempDir()
			localPath := filepath.Join(localDir, "compat.bin")
			err := os.WriteFile(localPath, content, 0644)
			Expect(err).ToNot(HaveOccurred())

			addr := strings.TrimPrefix(ts.URL, "http://")
			stager := NewHTTPFileStager(func(nodeID string) (string, error) {
				return addr, nil
			}, "")

			remotePath, err := stager.EnsureRemote(context.Background(), "node-1", localPath, "compat.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(remotePath).ToNot(BeEmpty())

			uploaded, err := os.ReadFile(filepath.Join(stagingDir, "compat.bin"))
			Expect(err).ToNot(HaveOccurred())
			Expect(uploaded).To(Equal(content))
		})
	})
})

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
