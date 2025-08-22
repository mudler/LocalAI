package launcher

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mudler/LocalAI/internal"
)

// Release represents a LocalAI release
type Release struct {
	Version     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ReleaseManager handles LocalAI release management
type ReleaseManager struct {
	// GitHubOwner is the GitHub repository owner
	GitHubOwner string
	// GitHubRepo is the GitHub repository name
	GitHubRepo string
	// BinaryPath is where the LocalAI binary is stored locally
	BinaryPath string
	// CurrentVersion is the currently installed version
	CurrentVersion string
}

// NewReleaseManager creates a new release manager
func NewReleaseManager() *ReleaseManager {
	homeDir, _ := os.UserHomeDir()
	binaryPath := filepath.Join(homeDir, ".localai", "bin")

	return &ReleaseManager{
		GitHubOwner:    "mudler",
		GitHubRepo:     "LocalAI",
		BinaryPath:     binaryPath,
		CurrentVersion: internal.PrintableVersion(),
	}
}

// GetLatestRelease fetches the latest release information from GitHub
func (rm *ReleaseManager) GetLatestRelease() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", rm.GitHubOwner, rm.GitHubRepo)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch latest release: status %d", resp.StatusCode)
	}

	// Parse the JSON response properly
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	release := &Release{}
	if err := json.Unmarshal(body, release); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Validate the release data
	if release.Version == "" {
		return nil, fmt.Errorf("no version found in release data")
	}

	return release, nil
}

// DownloadRelease downloads a specific version of LocalAI
func (rm *ReleaseManager) DownloadRelease(version string, progressCallback func(float64)) error {
	// Ensure the binary directory exists
	if err := os.MkdirAll(rm.BinaryPath, 0755); err != nil {
		return fmt.Errorf("failed to create binary directory: %w", err)
	}

	// Determine the binary name based on OS and architecture
	binaryName := rm.GetBinaryName(version)
	localPath := filepath.Join(rm.BinaryPath, "local-ai")

	// Download the binary
	downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		rm.GitHubOwner, rm.GitHubRepo, version, binaryName)

	if err := rm.downloadFile(downloadURL, localPath, progressCallback); err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}

	// Download and verify checksums
	checksumURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/LocalAI-%s-checksums.txt",
		rm.GitHubOwner, rm.GitHubRepo, version, version)

	checksumPath := filepath.Join(rm.BinaryPath, "checksums.txt")
	if err := rm.downloadFile(checksumURL, checksumPath, nil); err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}

	// Verify the checksum
	if err := rm.VerifyChecksum(localPath, checksumPath, binaryName); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Make the binary executable
	if err := os.Chmod(localPath, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	return nil
}

// GetBinaryName returns the appropriate binary name for the current platform
func (rm *ReleaseManager) GetBinaryName(version string) string {
	versionStr := strings.TrimPrefix(version, "v")
	os := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go arch names to the release naming convention
	switch arch {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	default:
		arch = "amd64" // fallback
	}

	return fmt.Sprintf("local-ai-v%s-%s-%s", versionStr, os, arch)
}

// downloadFile downloads a file from a URL to a local path with optional progress callback
func (rm *ReleaseManager) downloadFile(url, filepath string, progressCallback func(float64)) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Create a progress reader if callback is provided
	var reader io.Reader = resp.Body
	if progressCallback != nil && resp.ContentLength > 0 {
		reader = &progressReader{
			Reader:   resp.Body,
			Total:    resp.ContentLength,
			Callback: progressCallback,
		}
	}

	_, err = io.Copy(out, reader)
	return err
}

// progressReader wraps an io.Reader to provide download progress
type progressReader struct {
	io.Reader
	Total    int64
	Current  int64
	Callback func(float64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Current += int64(n)
	if pr.Callback != nil {
		progress := float64(pr.Current) / float64(pr.Total)
		pr.Callback(progress)
	}
	return n, err
}

// VerifyChecksum verifies the downloaded file against the provided checksums
func (rm *ReleaseManager) VerifyChecksum(filePath, checksumPath, binaryName string) error {
	// Calculate the SHA256 of the downloaded file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	calculatedHash := hex.EncodeToString(hasher.Sum(nil))

	// Read the checksums file
	checksumFile, err := os.Open(checksumPath)
	if err != nil {
		return fmt.Errorf("failed to open checksums file: %w", err)
	}
	defer checksumFile.Close()

	scanner := bufio.NewScanner(checksumFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, binaryName) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				expectedHash := parts[0]
				if calculatedHash == expectedHash {
					return nil // Checksum verified
				}
				return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, calculatedHash)
			}
		}
	}

	return fmt.Errorf("checksum not found for %s", binaryName)
}

// GetInstalledVersion returns the currently installed version
func (rm *ReleaseManager) GetInstalledVersion() string {
	// Check if the LocalAI binary exists and try to get its version
	binaryPath := rm.GetBinaryPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return "" // No version installed
	}

	// Try to execute the binary to get version
	// For now, return the build version as fallback
	return rm.CurrentVersion
}

// GetBinaryPath returns the path to the LocalAI binary
func (rm *ReleaseManager) GetBinaryPath() string {
	return filepath.Join(rm.BinaryPath, "local-ai")
}

// IsUpdateAvailable checks if an update is available
func (rm *ReleaseManager) IsUpdateAvailable() (bool, string, error) {
	latest, err := rm.GetLatestRelease()
	if err != nil {
		return false, "", err
	}

	current := rm.GetInstalledVersion()
	if current == "" {
		// No version installed, offer to download latest
		return true, latest.Version, nil
	}

	return latest.Version != current, latest.Version, nil
}

// IsLocalAIInstalled checks if LocalAI binary exists
func (rm *ReleaseManager) IsLocalAIInstalled() bool {
	binaryPath := rm.GetBinaryPath()
	_, err := os.Stat(binaryPath)
	return err == nil
}
