package launcher_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	launcher "github.com/mudler/LocalAI/cmd/launcher/internal"
)

var _ = Describe("ReleaseManager", func() {
	var (
		rm      *launcher.ReleaseManager
		tempDir string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "launcher-test-*")
		Expect(err).ToNot(HaveOccurred())

		rm = launcher.NewReleaseManager()
		// Override binary path for testing
		rm.BinaryPath = tempDir
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewReleaseManager", func() {
		It("should create a release manager with correct defaults", func() {
			newRM := launcher.NewReleaseManager()
			Expect(newRM.GitHubOwner).To(Equal("mudler"))
			Expect(newRM.GitHubRepo).To(Equal("LocalAI"))
			Expect(newRM.BinaryPath).To(ContainSubstring(".localai"))
			Expect(newRM.HTTPClient).ToNot(BeNil())
			Expect(newRM.HTTPClient.Timeout).To(Equal(30 * time.Second))
		})
	})

	Describe("GetBinaryName", func() {
		It("should return correct binary name for current platform", func() {
			binaryName := rm.GetBinaryName("v3.4.0")
			expectedOS := runtime.GOOS
			expectedArch := runtime.GOARCH

			expected := "local-ai-v3.4.0-" + expectedOS + "-" + expectedArch
			Expect(binaryName).To(Equal(expected))
		})

		It("should handle version with and without 'v' prefix", func() {
			withV := rm.GetBinaryName("v3.4.0")
			withoutV := rm.GetBinaryName("3.4.0")

			// Both should produce the same result
			Expect(withV).To(Equal(withoutV))
		})
	})

	Describe("GetBinaryPath", func() {
		It("should return the correct binary path", func() {
			path := rm.GetBinaryPath()
			expected := filepath.Join(tempDir, "local-ai")
			Expect(path).To(Equal(expected))
		})
	})

	Describe("GetInstalledVersion", func() {
		It("should return empty when no binary exists", func() {
			version := rm.GetInstalledVersion()
			Expect(version).To(BeEmpty()) // No binary installed in test
		})

		It("should return empty version when binary exists but no metadata", func() {
			// Create a fake binary for testing
			err := os.MkdirAll(rm.BinaryPath, 0755)
			Expect(err).ToNot(HaveOccurred())

			binaryPath := rm.GetBinaryPath()
			err = os.WriteFile(binaryPath, []byte("fake binary"), 0755)
			Expect(err).ToNot(HaveOccurred())

			version := rm.GetInstalledVersion()
			Expect(version).To(BeEmpty())
		})
	})

	Context("with mocked responses", func() {
		// Note: In a real implementation, we'd mock HTTP responses
		// For now, we'll test the structure and error handling

		Describe("GetLatestRelease", func() {
			It("should handle network errors gracefully", func() {
				// This test would require mocking HTTP client
				// For demonstration, we're just testing the method exists
				_, err := rm.GetLatestRelease()
				// We expect either success or a network error, not a panic
				// In a real test, we'd mock the HTTP response
				if err != nil {
					Expect(err.Error()).To(ContainSubstring("failed to fetch"))
				}
			})
		})

		Describe("DownloadRelease", func() {
			It("should create binary directory if it doesn't exist", func() {
				// Remove the temp directory to test creation
				os.RemoveAll(tempDir)

				// This will fail due to network, but should create the directory
				rm.DownloadRelease("v3.4.0", nil)

				// Check if directory was created
				_, err := os.Stat(tempDir)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

	Describe("VerifyChecksum functionality", func() {
		var (
			testFile     string
			checksumFile string
		)

		BeforeEach(func() {
			testFile = filepath.Join(tempDir, "test-binary")
			checksumFile = filepath.Join(tempDir, "checksums.txt")
		})

		It("should verify checksums correctly", func() {
			// Create a test file with known content
			testContent := []byte("test content for checksum")
			err := os.WriteFile(testFile, testContent, 0644)
			Expect(err).ToNot(HaveOccurred())

			// Calculate expected SHA256
			// This is a simplified test - in practice we'd use the actual checksum
			checksumContent := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  test-binary\n"
			err = os.WriteFile(checksumFile, []byte(checksumContent), 0644)
			Expect(err).ToNot(HaveOccurred())

			// Test checksum verification
			// Note: This will fail because our content doesn't match the empty string hash
			// In a real test, we'd calculate the actual hash
			err = rm.VerifyChecksum(testFile, checksumFile, "test-binary")
			// We expect this to fail since we're using a dummy checksum
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("checksum mismatch"))
		})

		It("should handle missing checksum file", func() {
			// Create test file but no checksum file
			err := os.WriteFile(testFile, []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = rm.VerifyChecksum(testFile, checksumFile, "test-binary")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to open checksums file"))
		})

		It("should handle missing binary in checksums", func() {
			// Create files but checksum doesn't contain our binary
			err := os.WriteFile(testFile, []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			checksumContent := "hash  other-binary\n"
			err = os.WriteFile(checksumFile, []byte(checksumContent), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = rm.VerifyChecksum(testFile, checksumFile, "test-binary")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("checksum not found"))
		})
	})

	Describe("DownloadRelease resume and retry", func() {
		var (
			version    string
			binaryName string
			content    []byte
			checksums  string
			finalPath  string
			partPath   string
		)

		BeforeEach(func() {
			version = "v9.9.9"
			binaryName = rm.GetBinaryName(version)

			// Deterministic, non-trivial content so resume/append bugs surface.
			content = make([]byte, 4096)
			for i := range content {
				content[i] = byte(i % 251)
			}
			sum := sha256.Sum256(content)
			checksums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), binaryName)

			finalPath = filepath.Join(tempDir, "local-ai")
			partPath = finalPath + ".part"

			// Isolate the persistent checksum/metadata dirs to the temp dir so
			// the test never touches the real ~/.localai and existing checksum
			// files don't short-circuit the download.
			rm.ChecksumsPath = filepath.Join(tempDir, "checksums")
			rm.MetadataPath = filepath.Join(tempDir, "metadata")
			rm.GitHubOwner = "owner"
			rm.GitHubRepo = "repo"
			rm.RetryBackoff = time.Millisecond

			Expect(os.MkdirAll(tempDir, 0755)).To(Succeed())
		})

		It("resumes from a partial .part file using a Range request", func() {
			Expect(os.WriteFile(partPath, content[:1024], 0644)).To(Succeed())

			var mu sync.Mutex
			sawRange := false
			binBytesServed := 0

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = w.Write([]byte(checksums))
					return
				}
				if rangeHdr := r.Header.Get("Range"); rangeHdr != "" {
					var start int
					_, _ = fmt.Sscanf(rangeHdr, "bytes=%d-", &start)
					mu.Lock()
					sawRange = true
					mu.Unlock()
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(content)-1, len(content)))
					w.WriteHeader(http.StatusPartialContent)
					n, _ := w.Write(content[start:])
					mu.Lock()
					binBytesServed += n
					mu.Unlock()
					return
				}
				w.WriteHeader(http.StatusOK)
				n, _ := w.Write(content)
				mu.Lock()
				binBytesServed += n
				mu.Unlock()
			}))
			defer srv.Close()
			rm.BaseDownloadURL = srv.URL

			err := rm.DownloadRelease(version, nil)
			Expect(err).ToNot(HaveOccurred())

			got, err := os.ReadFile(finalPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(content))
			Expect(sawRange).To(BeTrue(), "expected the download to resume with a Range request")
			Expect(binBytesServed).To(Equal(len(content)-1024), "expected only the remaining bytes to be served")
			Expect(partPath).ToNot(BeAnExistingFile())
		})

		It("starts fresh when the server ignores the Range header (200)", func() {
			// A stale/garbage partial that must NOT be appended to.
			Expect(os.WriteFile(partPath, []byte("garbage-garbage-garbage"), 0644)).To(Succeed())

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = w.Write([]byte(checksums))
					return
				}
				// Ignore any Range and always serve the full body.
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(content)
			}))
			defer srv.Close()
			rm.BaseDownloadURL = srv.URL

			err := rm.DownloadRelease(version, nil)
			Expect(err).ToNot(HaveOccurred())

			got, err := os.ReadFile(finalPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(content))
		})

		It("restarts the download when the partial is stale (416)", func() {
			// Oversized partial -> requested Range start is beyond the content.
			Expect(os.WriteFile(partPath, make([]byte, len(content)+10), 0644)).To(Succeed())

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = w.Write([]byte(checksums))
					return
				}
				if rangeHdr := r.Header.Get("Range"); rangeHdr != "" {
					var start int
					_, _ = fmt.Sscanf(rangeHdr, "bytes=%d-", &start)
					if start >= len(content) {
						w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
						return
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(content)-1, len(content)))
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(content[start:])
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(content)
			}))
			defer srv.Close()
			rm.BaseDownloadURL = srv.URL

			err := rm.DownloadRelease(version, nil)
			Expect(err).ToNot(HaveOccurred())

			got, err := os.ReadFile(finalPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(content))
		})

		It("removes the downloaded file when checksum verification fails", func() {
			bad := []byte("this is definitely not the expected binary content")

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					// Checksums are for `content`, but we serve `bad`.
					_, _ = w.Write([]byte(checksums))
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(bad)
			}))
			defer srv.Close()
			rm.BaseDownloadURL = srv.URL

			err := rm.DownloadRelease(version, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("checksum"))
			Expect(finalPath).ToNot(BeAnExistingFile())
			Expect(partPath).ToNot(BeAnExistingFile())
		})

		It("reports progress as downloaded and total byte counts", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = w.Write([]byte(checksums))
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(content)))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(content)
			}))
			defer srv.Close()
			rm.BaseDownloadURL = srv.URL

			var mu sync.Mutex
			var lastDownloaded, lastTotal int64
			err := rm.DownloadRelease(version, func(downloaded, total int64) {
				mu.Lock()
				lastDownloaded = downloaded
				lastTotal = total
				mu.Unlock()
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(lastTotal).To(Equal(int64(len(content))))
			Expect(lastDownloaded).To(Equal(int64(len(content))))
		})
	})

	Describe("GetLatestRelease", func() {
		It("resolves the latest version from the releases/latest redirect", func() {
			// The github.com redirect path must be preferred over the
			// rate-limited api.github.com, so a working redirect yields the tag
			// without ever needing the API.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/releases/latest"):
					http.Redirect(w, r, "/owner/repo/releases/tag/v9.9.9", http.StatusFound)
				case strings.HasSuffix(r.URL.Path, "/releases/tag/v9.9.9"):
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			rm.BaseDownloadURL = srv.URL
			rm.GitHubOwner = "owner"
			rm.GitHubRepo = "repo"

			release, err := rm.GetLatestRelease()
			Expect(err).ToNot(HaveOccurred())
			Expect(release.Version).To(Equal("v9.9.9"))
		})
	})
})
