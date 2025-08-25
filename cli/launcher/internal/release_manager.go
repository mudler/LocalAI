package launcher

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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
	// ChecksumsPath is where checksums are stored
	ChecksumsPath string
	// MetadataPath is where version metadata is stored
	MetadataPath string
}

// NewReleaseManager creates a new release manager
func NewReleaseManager() *ReleaseManager {
	homeDir, _ := os.UserHomeDir()
	binaryPath := filepath.Join(homeDir, ".localai", "bin")
	checksumsPath := filepath.Join(homeDir, ".localai", "checksums")
	metadataPath := filepath.Join(homeDir, ".localai", "metadata")

	return &ReleaseManager{
		GitHubOwner:    "mudler",
		GitHubRepo:     "LocalAI",
		BinaryPath:     binaryPath,
		CurrentVersion: internal.PrintableVersion(),
		ChecksumsPath:  checksumsPath,
		MetadataPath:   metadataPath,
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

	// Save checksums persistently for future verification
	if err := rm.saveChecksums(version, checksumPath, binaryName); err != nil {
		log.Printf("Warning: failed to save checksums: %v", err)
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

// saveChecksums saves checksums persistently for future verification
func (rm *ReleaseManager) saveChecksums(version, checksumPath, binaryName string) error {
	// Ensure checksums directory exists
	if err := os.MkdirAll(rm.ChecksumsPath, 0755); err != nil {
		return fmt.Errorf("failed to create checksums directory: %w", err)
	}

	// Read the downloaded checksums file
	checksumData, err := os.ReadFile(checksumPath)
	if err != nil {
		return fmt.Errorf("failed to read checksums file: %w", err)
	}

	// Save to persistent location with version info
	persistentPath := filepath.Join(rm.ChecksumsPath, fmt.Sprintf("checksums-%s.txt", version))
	if err := os.WriteFile(persistentPath, checksumData, 0644); err != nil {
		return fmt.Errorf("failed to write persistent checksums: %w", err)
	}

	// Also save a "latest" checksums file for the current version
	latestPath := filepath.Join(rm.ChecksumsPath, "checksums-latest.txt")
	if err := os.WriteFile(latestPath, checksumData, 0644); err != nil {
		return fmt.Errorf("failed to write latest checksums: %w", err)
	}

	// Save version metadata
	if err := rm.saveVersionMetadata(version); err != nil {
		log.Printf("Warning: failed to save version metadata: %v", err)
	}

	log.Printf("Checksums saved for version %s", version)
	return nil
}

// saveVersionMetadata saves the installed version information
func (rm *ReleaseManager) saveVersionMetadata(version string) error {
	// Ensure metadata directory exists
	if err := os.MkdirAll(rm.MetadataPath, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}

	// Create metadata structure
	metadata := struct {
		Version     string    `json:"version"`
		InstalledAt time.Time `json:"installed_at"`
		BinaryPath  string    `json:"binary_path"`
	}{
		Version:     version,
		InstalledAt: time.Now(),
		BinaryPath:  rm.GetBinaryPath(),
	}

	// Marshal to JSON
	metadataData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Save metadata file
	metadataPath := filepath.Join(rm.MetadataPath, "installed-version.json")
	if err := os.WriteFile(metadataPath, metadataData, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	log.Printf("Version metadata saved: %s", version)
	return nil
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

	// Fallback: Check if the LocalAI binary exists and try to get its version
	binaryPath := rm.GetBinaryPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return "" // No version installed
	}

	// try to get version from metadata
	if version := rm.loadVersionMetadata(); version != "" {
		return version
	}

	// Try to run the binary to get the version (fallback method)
	version, err := exec.Command(binaryPath, "--version").Output()
	if err != nil {
		// If binary exists but --version fails, try to determine from filename or other means
		log.Printf("Binary exists but --version failed: %v", err)
		return ""
	}

	stringVersion := strings.TrimSpace(string(version))
	stringVersion = strings.TrimRight(stringVersion, "\n")

	return stringVersion
}

// loadVersionMetadata loads the installed version from metadata file
func (rm *ReleaseManager) loadVersionMetadata() string {
	metadataPath := filepath.Join(rm.MetadataPath, "installed-version.json")

	// Check if metadata file exists
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		return ""
	}

	// Read metadata file
	metadataData, err := os.ReadFile(metadataPath)
	if err != nil {
		log.Printf("Failed to read metadata file: %v", err)
		return ""
	}

	// Parse metadata
	var metadata struct {
		Version     string    `json:"version"`
		InstalledAt time.Time `json:"installed_at"`
		BinaryPath  string    `json:"binary_path"`
	}

	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		log.Printf("Failed to parse metadata file: %v", err)
		return ""
	}

	// Verify that the binary path in metadata matches current binary path
	if metadata.BinaryPath != rm.GetBinaryPath() {
		log.Printf("Binary path mismatch in metadata, ignoring")
		return ""
	}

	log.Printf("Loaded version from metadata: %s (installed at %s)", metadata.Version, metadata.InstalledAt.Format("2006-01-02 15:04:05"))
	return metadata.Version
}

// GetBinaryPath returns the path to the LocalAI binary
func (rm *ReleaseManager) GetBinaryPath() string {
	return filepath.Join(rm.BinaryPath, "local-ai")
}

// IsUpdateAvailable checks if an update is available
func (rm *ReleaseManager) IsUpdateAvailable() (bool, string, error) {
	log.Printf("IsUpdateAvailable: checking for updates...")

	latest, err := rm.GetLatestRelease()
	if err != nil {
		log.Printf("IsUpdateAvailable: failed to get latest release: %v", err)
		return false, "", err
	}
	log.Printf("IsUpdateAvailable: latest release version: %s", latest.Version)

	current := rm.GetInstalledVersion()
	log.Printf("IsUpdateAvailable: current installed version: %s", current)

	if current == "" {
		// No version installed, offer to download latest
		log.Printf("IsUpdateAvailable: no version installed, offering latest: %s", latest.Version)
		return true, latest.Version, nil
	}

	updateAvailable := latest.Version != current
	log.Printf("IsUpdateAvailable: update available: %v (latest: %s, current: %s)", updateAvailable, latest.Version, current)
	return updateAvailable, latest.Version, nil
}

// IsLocalAIInstalled checks if LocalAI binary exists and is valid
func (rm *ReleaseManager) IsLocalAIInstalled() bool {
	binaryPath := rm.GetBinaryPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return false
	}

	// Verify the binary integrity
	if err := rm.VerifyInstalledBinary(); err != nil {
		log.Printf("Binary integrity check failed: %v", err)
		// Remove corrupted binary
		if removeErr := os.Remove(binaryPath); removeErr != nil {
			log.Printf("Failed to remove corrupted binary: %v", removeErr)
		}
		return false
	}

	return true
}

// VerifyInstalledBinary verifies the installed binary against saved checksums
func (rm *ReleaseManager) VerifyInstalledBinary() error {
	binaryPath := rm.GetBinaryPath()

	// Check if we have saved checksums
	latestChecksumsPath := filepath.Join(rm.ChecksumsPath, "checksums-latest.txt")
	if _, err := os.Stat(latestChecksumsPath); os.IsNotExist(err) {
		return fmt.Errorf("no saved checksums found")
	}

	// Get the binary name for the current version from metadata
	currentVersion := rm.loadVersionMetadata()
	if currentVersion == "" {
		return fmt.Errorf("cannot determine current version from metadata")
	}

	binaryName := rm.GetBinaryName(currentVersion)

	// Verify against saved checksums
	return rm.VerifyChecksum(binaryPath, latestChecksumsPath, binaryName)
}

// CleanupPartialDownloads removes any partial or corrupted downloads
func (rm *ReleaseManager) CleanupPartialDownloads() error {
	binaryPath := rm.GetBinaryPath()

	// Check if binary exists but is corrupted
	if _, err := os.Stat(binaryPath); err == nil {
		// Binary exists, verify it
		if verifyErr := rm.VerifyInstalledBinary(); verifyErr != nil {
			log.Printf("Found corrupted binary, removing: %v", verifyErr)
			if removeErr := os.Remove(binaryPath); removeErr != nil {
				log.Printf("Failed to remove corrupted binary: %v", removeErr)
			}
			// Clear metadata since binary is corrupted
			rm.clearVersionMetadata()
		}
	}

	// Clean up any temporary checksum files
	tempChecksumsPath := filepath.Join(rm.BinaryPath, "checksums.txt")
	if _, err := os.Stat(tempChecksumsPath); err == nil {
		if removeErr := os.Remove(tempChecksumsPath); removeErr != nil {
			log.Printf("Failed to remove temporary checksums: %v", removeErr)
		}
	}

	return nil
}

// clearVersionMetadata clears the version metadata (used when binary is corrupted or removed)
func (rm *ReleaseManager) clearVersionMetadata() {
	metadataPath := filepath.Join(rm.MetadataPath, "installed-version.json")
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Failed to clear version metadata: %v", err)
	} else {
		log.Printf("Version metadata cleared")
	}
}
