package nodes

import (
	"bytes"
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
})
