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
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/httpclient"
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
	// BaseDownloadURL is the base URL release assets are downloaded from
	// (defaults to https://github.com; overridable for testing)
	BaseDownloadURL string
	// RetryBackoff is the base wait between download attempts; the Nth retry
	// waits N*RetryBackoff (defaults to 1s; lowered in tests)
	RetryBackoff time.Duration
	// HTTPClient is the HTTP client used for downloads
	HTTPClient *http.Client
}

// NewReleaseManager creates a new release manager
func NewReleaseManager() *ReleaseManager {
	homeDir, _ := os.UserHomeDir()
	binaryPath := filepath.Join(homeDir, ".localai", "bin")
	checksumsPath := filepath.Join(homeDir, ".localai", "checksums")
	metadataPath := filepath.Join(homeDir, ".localai", "metadata")

	return &ReleaseManager{
		GitHubOwner:     "mudler",
		GitHubRepo:      "LocalAI",
		BinaryPath:      binaryPath,
		CurrentVersion:  internal.PrintableVersion(),
		ChecksumsPath:   checksumsPath,
		MetadataPath:    metadataPath,
		BaseDownloadURL: "https://github.com",
		RetryBackoff:    1 * time.Second,
		HTTPClient:      httpclient.NewWithTimeout(30*time.Second, httpclient.WithFollowRedirects()),
	}
}

// GetLatestRelease resolves the latest LocalAI release.
//
// It first follows the github.com "releases/latest" redirect, which reveals the
// latest tag in the final URL and—crucially—is NOT subject to the
// 60-requests/hour unauthenticated rate limit of api.github.com. That limit is
// per-IP, so on shared/NAT/CGNAT/cloud addresses the API returns 403 almost
// immediately (e.g. on a fresh install with no LocalAI present yet). The
// redirect avoids that entirely. The richer JSON API is kept only as a fallback.
//
// Only the version is consumed by callers, so the redirect's tag is sufficient.
func (rm *ReleaseManager) GetLatestRelease() (*Release, error) {
	version, redirectErr := rm.latestVersionFromRedirect()
	if redirectErr == nil {
		return &Release{Version: version}, nil
	}
	log.Printf("Could not resolve latest version via release redirect (%v); falling back to GitHub API", redirectErr)

	release, apiErr := rm.latestReleaseFromAPI()
	if apiErr != nil {
		// Surface both failures so a rate-limited API doesn't mask the (usually
		// more relevant) redirect error.
		return nil, fmt.Errorf("failed to fetch latest release: %v (redirect: %v)", apiErr, redirectErr)
	}
	return release, nil
}

// latestVersionFromRedirect returns the latest tag by following the github.com
// "releases/latest" redirect to ".../releases/tag/<tag>".
func (rm *ReleaseManager) latestVersionFromRedirect() (string, error) {
	url := fmt.Sprintf("%s/%s/%s/releases/latest", rm.BaseDownloadURL, rm.GitHubOwner, rm.GitHubRepo)

	resp, err := rm.HTTPClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	// After the redirect is followed, the final request URL is the tag page.
	version := path.Base(resp.Request.URL.Path)
	if version == "" || version == "." || version == "latest" {
		return "", fmt.Errorf("could not determine version from %s", resp.Request.URL.String())
	}
	return version, nil
}

// latestReleaseFromAPI fetches the latest release JSON from api.github.com. This
// is the fallback path; it is rate-limited unless GITHUB_TOKEN is set.
func (rm *ReleaseManager) latestReleaseFromAPI() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", rm.GitHubOwner, rm.GitHubRepo)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// An optional token lifts the unauthenticated 60/hour limit to 5000/hour.
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := rm.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
			resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub API rate limit exceeded (status %d); retry later or set GITHUB_TOKEN to raise the limit", resp.StatusCode)
		}
		return nil, fmt.Errorf("status %d", resp.StatusCode)
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
func (rm *ReleaseManager) DownloadRelease(version string, progressCallback func(downloaded, total int64)) error {
	// Ensure the binary directory exists
	if err := os.MkdirAll(rm.BinaryPath, 0755); err != nil {
		return fmt.Errorf("failed to create binary directory: %w", err)
	}

	// Determine the binary name based on OS and architecture
	binaryName := rm.GetBinaryName(version)
	localPath := filepath.Join(rm.BinaryPath, "local-ai")

	// Download the binary
	downloadURL := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s",
		rm.BaseDownloadURL, rm.GitHubOwner, rm.GitHubRepo, version, binaryName)

	if err := rm.downloadFile(downloadURL, localPath, progressCallback); err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}

	// Download and verify checksums
	checksumURL := fmt.Sprintf("%s/%s/%s/releases/download/%s/LocalAI-%s-checksums.txt",
		rm.BaseDownloadURL, rm.GitHubOwner, rm.GitHubRepo, version, version)

	checksumPath := filepath.Join(rm.BinaryPath, "checksums.txt")
	manualChecksumPath := filepath.Join(rm.ChecksumsPath, fmt.Sprintf("checksums-%s.txt", version))

	// First, check if there's already a checksum file (either manually placed or previously downloaded)
	// and honor that, skipping download entirely in such case
	var downloadErr error
	if _, err := os.Stat(manualChecksumPath); err == nil {
		log.Printf("Using existing checksums from: %s", manualChecksumPath)
		checksumPath = manualChecksumPath
	} else if _, err := os.Stat(checksumPath); err == nil {
		log.Printf("Using existing checksums from: %s", checksumPath)
	} else {
		// No existing checksum file found, try to download
		downloadErr = rm.downloadFile(checksumURL, checksumPath, nil)

		if downloadErr != nil {
			log.Printf("Warning: failed to download checksums: %v", downloadErr)
			log.Printf("Warning: Checksum verification will be skipped. For security, you can manually place checksums at: %s", manualChecksumPath)
			log.Printf("Download checksums from: %s", checksumURL)
			// Continue without verification - log warning but don't fail
		}
	}

	// Verify the checksum if we have a checksum file
	if _, err := os.Stat(checksumPath); err == nil {
		if err := rm.VerifyChecksum(localPath, checksumPath, binaryName); err != nil {
			// Discard the corrupt binary (and any leftover partial) so the next
			// retry starts from a clean slate rather than resuming corruption.
			os.Remove(localPath)
			os.Remove(localPath + ".part")
			return fmt.Errorf("checksum verification failed: %w", err)
		}
		log.Printf("Checksum verification successful")

		// Save checksums persistently for future verification
		if downloadErr == nil {
			if err := rm.saveChecksums(version, checksumPath, binaryName); err != nil {
				log.Printf("Warning: failed to save checksums: %v", err)
			}
		}
	} else {
		log.Printf("Warning: Proceeding without checksum verification")
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
func (rm *ReleaseManager) downloadFile(url, filepath string, progressCallback func(downloaded, total int64)) error {
	return rm.downloadFileWithRetry(url, filepath, progressCallback, 3)
}

// downloadFileWithRetry downloads a file with retry and HTTP Range resume.
//
// The body is streamed to "<dest>.part" and only renamed to dest on success, so
// a dropped connection leaves a partial file that the next attempt continues via
// a "Range: bytes=N-" request instead of restarting from zero. This matters for
// GitHub release downloads, which are large and flaky.
func (rm *ReleaseManager) downloadFileWithRetry(url, dest string, progressCallback func(downloaded, total int64), maxRetries int) error {
	partPath := dest + ".part"
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("Retrying download (attempt %d/%d): %s", attempt, maxRetries, url)
			time.Sleep(time.Duration(attempt) * rm.RetryBackoff)
		}

		// Resume from however much we already have on disk.
		var offset int64
		if fi, err := os.Stat(partPath); err == nil {
			offset = fi.Size()
		}

		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}

		resp, err := rm.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			// Server ignored the Range (or we had nothing): start fresh.
			offset = 0
		case http.StatusPartialContent:
			// Resume: append to the existing partial file.
		case http.StatusRequestedRangeNotSatisfiable:
			// Stale or already-complete partial: discard and restart fresh.
			resp.Body.Close()
			os.Remove(partPath)
			lastErr = fmt.Errorf("partial download no longer valid (status %s), restarting", resp.Status)
			continue
		default:
			resp.Body.Close()
			lastErr = fmt.Errorf("bad status: %s", resp.Status)
			continue
		}

		var out *os.File
		if offset > 0 {
			out, err = os.OpenFile(partPath, os.O_WRONLY|os.O_APPEND, 0644)
		} else {
			out, err = os.Create(partPath)
		}
		if err != nil {
			resp.Body.Close()
			return err
		}

		// On a 206 the Content-Length is the remaining bytes, so the full size
		// is what we already have plus what's still to come.
		total := resp.ContentLength
		if offset > 0 && total > 0 {
			total += offset
		}

		var reader io.Reader = resp.Body
		if progressCallback != nil && total > 0 {
			reader = &progressReader{
				Reader:   resp.Body,
				Total:    total,
				Current:  offset,
				Callback: progressCallback,
			}
		}

		_, err = io.Copy(out, reader)
		resp.Body.Close()
		out.Close()

		if err != nil {
			// Keep the partial file so the next attempt can resume from it.
			lastErr = err
			continue
		}

		if err := os.Rename(partPath, dest); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
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

// progressReader wraps an io.Reader to provide download progress as a
// (downloaded, total) byte count so callers can render both a progress bar and
// a human-readable size.
type progressReader struct {
	io.Reader
	Total    int64
	Current  int64
	Callback func(downloaded, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Current += int64(n)
	if pr.Callback != nil {
		pr.Callback(pr.Current, pr.Total)
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
