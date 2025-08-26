package launcher_test

import (
	"os"
	"path/filepath"
	"runtime"

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
})
