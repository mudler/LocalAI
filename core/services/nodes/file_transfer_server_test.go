package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

	// --- Resumable upload (Content-Range) tests ---

	Describe("Resumable upload (Content-Range)", func() {
		// doPut sends a PUT to ts with the given body, headers, and key.
		doPut := func(ts *httptest.Server, token, key string, body []byte, headers map[string]string) (*http.Response, []byte) {
			req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/files/"+key, bytes.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			respBody, _ := io.ReadAll(resp.Body)
			return resp, respBody
		}

		It("accepts two consecutive Content-Range chunks and produces the full file", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			full := bytes.Repeat([]byte("abcdefghij"), 20) // 200 bytes
			fullHash := sha256Hex(full)

			// Chunk 1: bytes 0-99
			resp1, _ := doPut(ts, "tok", "chunked.bin", full[:100], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 0-99/%d", len(full)),
				HeaderContentSHA256: fullHash,
			})
			Expect(resp1.StatusCode).To(Equal(http.StatusPermanentRedirect))
			Expect(resp1.Header.Get(HeaderFileSize)).To(Equal("100"))

			// Chunk 2: bytes 100-199
			resp2, _ := doPut(ts, "tok", "chunked.bin", full[100:], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 100-199/%d", len(full)),
				HeaderContentSHA256: fullHash,
			})
			Expect(resp2.StatusCode).To(Equal(http.StatusOK))

			// File matches the full content
			got, err := os.ReadFile(filepath.Join(stagingDir, "chunked.bin"))
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(full))

			// Sidecar holds the final hash
			sidecar, err := os.ReadFile(filepath.Join(stagingDir, "chunked.bin.sha256"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(sidecar)).To(Equal(fullHash))

			// Target sidecar (in-progress marker) is cleared once complete
			_, err = os.Stat(filepath.Join(stagingDir, "chunked.bin.sha256.target"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("returns 416 when Content-Range start does not match current file size", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			full := bytes.Repeat([]byte("x"), 200)
			fullHash := sha256Hex(full)

			// First chunk: bytes 0-49
			resp1, _ := doPut(ts, "tok", "mismatch.bin", full[:50], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 0-49/%d", len(full)),
				HeaderContentSHA256: fullHash,
			})
			Expect(resp1.StatusCode).To(Equal(http.StatusPermanentRedirect))

			// Skip ahead: server has 50 bytes but client tries to send 100-199.
			resp2, _ := doPut(ts, "tok", "mismatch.bin", full[100:200], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 100-199/%d", len(full)),
				HeaderContentSHA256: fullHash,
			})
			Expect(resp2.StatusCode).To(Equal(http.StatusRequestedRangeNotSatisfiable))
			Expect(resp2.Header.Get(HeaderFileSize)).To(Equal("50"))
		})

		It("returns 409 when X-Content-SHA256 changes between resumed chunks", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			a := bytes.Repeat([]byte("a"), 200)
			b := bytes.Repeat([]byte("b"), 200)

			// Chunk 1 (file A): bytes 0-49
			resp1, _ := doPut(ts, "tok", "drifted.bin", a[:50], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 0-49/%d", len(a)),
				HeaderContentSHA256: sha256Hex(a),
			})
			Expect(resp1.StatusCode).To(Equal(http.StatusPermanentRedirect))

			// Chunk 2 claims file B's hash for the *same* key — should be rejected.
			resp2, _ := doPut(ts, "tok", "drifted.bin", b[50:100], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 50-99/%d", len(b)),
				HeaderContentSHA256: sha256Hex(b),
			})
			Expect(resp2.StatusCode).To(Equal(http.StatusConflict))
		})

		It("returns 400 when final SHA-256 does not match the declared target", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			full := bytes.Repeat([]byte("z"), 100)
			wrongHash := sha256Hex([]byte("definitely-not-this"))

			resp, _ := doPut(ts, "tok", "bad-hash.bin", full, map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 0-99/%d", len(full)),
				HeaderContentSHA256: wrongHash,
			})
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("HEAD on a partial upload exposes X-Target-SHA256 and current size", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			full := bytes.Repeat([]byte("q"), 200)
			fullHash := sha256Hex(full)

			// One chunk uploaded, file is partial.
			resp1, _ := doPut(ts, "tok", "partial.bin", full[:60], map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 0-59/%d", len(full)),
				HeaderContentSHA256: fullHash,
			})
			Expect(resp1.StatusCode).To(Equal(http.StatusPermanentRedirect))

			req, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/partial.bin", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get(HeaderFileSize)).To(Equal("60"))
			Expect(resp.Header.Get("Accept-Ranges")).To(Equal("bytes"))
			Expect(resp.Header.Get(HeaderTargetSHA256)).To(Equal(fullHash))
			// While the upload is in progress we must NOT expose a misleading
			// X-Content-SHA256 of the bytes-so-far — clients use HeaderContentSHA256
			// only for completed files.
			Expect(resp.Header.Get(HeaderContentSHA256)).To(BeEmpty())
		})

		It("transparently overwrites an existing finished file when client starts from byte 0 with a new hash", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			// Pre-place a finished file (sidecar present, no target sidecar).
			oldContent := []byte("ancient version")
			err := os.WriteFile(filepath.Join(stagingDir, "overwrite.bin"), oldContent, 0644)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(stagingDir, "overwrite.bin.sha256"), []byte(sha256Hex(oldContent)), 0644)
			Expect(err).ToNot(HaveOccurred())

			// New upload with a different target hash, range 0-N/total.
			newContent := bytes.Repeat([]byte("new"), 50) // 150 bytes
			newHash := sha256Hex(newContent)

			resp, _ := doPut(ts, "tok", "overwrite.bin", newContent, map[string]string{
				"Content-Range":     fmt.Sprintf("bytes 0-%d/%d", len(newContent)-1, len(newContent)),
				HeaderContentSHA256: newHash,
			})
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			got, err := os.ReadFile(filepath.Join(stagingDir, "overwrite.bin"))
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(newContent))
		})

		It("HEAD advertises Accept-Ranges: bytes on completed files", func() {
			ts, _, _, _ := setupTestServer("tok", 0)

			content := "done"
			doPut(ts, "tok", "ranges-advert.txt", []byte(content), nil)

			req, err := http.NewRequest(http.MethodHead, ts.URL+"/v1/files/ranges-advert.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Accept-Ranges")).To(Equal("bytes"))
			Expect(resp.Header.Get("Content-Length")).To(Equal(strconv.Itoa(len(content))))
		})
	})

	// --- End-to-end client/server resume tests ---

	Describe("HTTPFileStager resume via EnsureRemote", func() {
		It("resumes from server's reported offset when a partial upload exists", func() {
			ts, stagingDir, _, _ := setupTestServer("tok", 0)

			// Create the local file (the master's source-of-truth).
			localDir := GinkgoT().TempDir()
			localPath := filepath.Join(localDir, "resume.bin")
			content := bytes.Repeat([]byte("R"), 500)
			Expect(os.WriteFile(localPath, content, 0644)).To(Succeed())
			fullHash := sha256Hex(content)

			// Pre-seed the "worker" with the first 200 bytes as if a prior
			// attempt had transferred that much, plus a target-hash sidecar
			// claiming the full file's hash.
			dst := filepath.Join(stagingDir, "resume.bin")
			Expect(os.WriteFile(dst, content[:200], 0644)).To(Succeed())
			Expect(os.WriteFile(dst+".sha256.target", []byte(fullHash), 0644)).To(Succeed())

			addr := strings.TrimPrefix(ts.URL, "http://")
			stager := NewHTTPFileStager(func(nodeID string) (string, error) {
				return addr, nil
			}, "tok")

			remotePath, err := stager.EnsureRemote(context.Background(), "node-1", localPath, "resume.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(remotePath).To(Equal(dst))

			got, err := os.ReadFile(dst)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(content))

			// Final sidecar should hold the full-file hash.
			sidecar, err := os.ReadFile(dst + ".sha256")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(string(sidecar))).To(Equal(fullHash))
		})

		It("survives a mid-stream connection drop and resumes on retry", func() {
			// Server that drops the connection after writing the first N bytes
			// on the FIRST PUT attempt, then behaves normally.
			stagingDir := GinkgoT().TempDir()
			modelsDir := GinkgoT().TempDir()
			dataDir := GinkgoT().TempDir()

			var (
				attemptCount int
				attemptMu    sync.Mutex
			)
			const dropAfter = 80 // bytes the server "accepts" before crashing

			mux := http.NewServeMux()
			mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
				if !checkBearerToken(r, "tok") {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				key := strings.TrimPrefix(r.URL.Path, "/v1/files/")
				if r.Method == http.MethodHead {
					handleHead(w, r, stagingDir, modelsDir, dataDir, key)
					return
				}
				if r.Method != http.MethodPut {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}

				attemptMu.Lock()
				attemptCount++
				thisAttempt := attemptCount
				attemptMu.Unlock()

				if thisAttempt == 1 {
					// Read a bounded prefix into the partial file, then hijack
					// the connection and close abruptly to simulate the drop.
					cr, err := parseContentRange(r.Header.Get("Content-Range"))
					if err != nil || cr == nil {
						http.Error(w, "expected content-range", http.StatusBadRequest)
						return
					}
					dst := filepath.Join(stagingDir, key)
					f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					target := r.Header.Get(HeaderContentSHA256)
					if target != "" {
						_ = os.WriteFile(dst+".sha256.target", []byte(target), 0640)
					}
					_, _ = io.CopyN(f, r.Body, dropAfter)
					_ = f.Close()

					hj, ok := w.(http.Hijacker)
					if !ok {
						http.Error(w, "hijack unsupported", http.StatusInternalServerError)
						return
					}
					conn, _, err := hj.Hijack()
					if err == nil {
						_ = conn.Close() // abrupt close — client sees a transport error
					}
					return
				}

				// Subsequent attempts: behave normally.
				handleUpload(w, r, stagingDir, modelsDir, dataDir, key, 0)
			})
			ts := httptest.NewServer(mux)
			DeferCleanup(ts.Close)

			// Build a small "model" file to upload (300 bytes for speed).
			localDir := GinkgoT().TempDir()
			localPath := filepath.Join(localDir, "flaky.bin")
			content := bytes.Repeat([]byte("F"), 300)
			Expect(os.WriteFile(localPath, content, 0644)).To(Succeed())

			addr := strings.TrimPrefix(ts.URL, "http://")
			stager := NewHTTPFileStager(func(nodeID string) (string, error) {
				return addr, nil
			}, "tok")

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			remotePath, err := stager.EnsureRemote(ctx, "node-1", localPath, "flaky.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(remotePath).To(Equal(filepath.Join(stagingDir, "flaky.bin")))

			// Final file is correct
			got, err := os.ReadFile(filepath.Join(stagingDir, "flaky.bin"))
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(content))

			// At least one retry happened
			attemptMu.Lock()
			Expect(attemptCount).To(BeNumerically(">=", 2))
			attemptMu.Unlock()
		})
	})
})

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

var _ = Describe("StartFileTransferServerWithListener", func() {
	start := func(token string) (string, func()) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		staging := GinkgoT().TempDir()
		models := GinkgoT().TempDir()
		data := GinkgoT().TempDir()
		srv, err := StartFileTransferServerWithListener(lis, staging, models, data, token, 0)
		Expect(err).NotTo(HaveOccurred())
		base := "http://" + lis.Addr().String()
		return base, func() { ShutdownFileTransferServer(srv) }
	}

	// Exercises the empty-token fail-open warning branch: the server serves
	// file requests with no Authorization header at all.
	It("serves unauthenticated when started without a token", func() {
		base, stop := start("")
		defer stop()

		resp, err := http.Get(base + "/v1/files/missing.bin")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		// No 401 — the empty token fails open. The file is absent so we get 404.
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("rejects requests without the bearer token when a token is set", func() {
		base, stop := start("s3cret")
		defer stop()

		resp, err := http.Get(base + "/v1/files/missing.bin")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("serves the unauthenticated health endpoints regardless of token", func() {
		base, stop := start("s3cret")
		defer stop()

		resp, err := http.Get(base + "/healthz")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})
})
