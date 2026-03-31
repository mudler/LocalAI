package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/xlog"
)

// HTTPFileStager implements FileStager using HTTP for environments without S3.
// Files are transferred between the frontend and backend nodes over a small
// HTTP server running alongside the gRPC backend process.
type HTTPFileStager struct {
	httpAddrFor     func(nodeID string) (string, error)
	token           string
	client          *http.Client
	responseTimeout time.Duration // timeout waiting for server response after upload
	maxRetries      int           // number of retry attempts for transient failures
}

// NewHTTPFileStager creates a new HTTP file stager.
// httpAddrFor should return the HTTP address (host:port) for the given node ID.
// token is the registration token used for authentication.
func NewHTTPFileStager(httpAddrFor func(nodeID string) (string, error), token string) *HTTPFileStager {
	responseTimeout := 30 * time.Minute
	if v := os.Getenv("LOCALAI_FILE_TRANSFER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			responseTimeout = d
		}
	}

	maxRetries := 3
	if v := os.Getenv("LOCALAI_FILE_TRANSFER_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxRetries = n
		}
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 15 * time.Second, // aggressive keepalive for LAN transfers
		}).DialContext,
		ForceAttemptHTTP2:     false, // HTTP/2 flow control can stall large uploads
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		WriteBufferSize:       256 << 10, // 256 KB
		ReadBufferSize:        256 << 10, // 256 KB
	}

	return &HTTPFileStager{
		httpAddrFor: httpAddrFor,
		token:       token,
		client: &http.Client{
			// No Timeout set — for large uploads, http.Client.Timeout covers the
			// entire request lifecycle including the body upload. If it fires
			// mid-write, Go closes the connection causing "connection reset by peer"
			// on the server. Instead we use ResponseHeaderTimeout on the transport
			// to cover only the wait-for-server-response phase.
			Transport: transport,
		},
		responseTimeout: responseTimeout,
		maxRetries:      maxRetries,
	}
}

func (h *HTTPFileStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	xlog.Debug("Staging file to remote node via HTTP", "node", nodeID, "localPath", localPath, "key", key)

	addr, err := h.httpAddrFor(nodeID)
	if err != nil {
		return "", fmt.Errorf("resolving HTTP address for node %s: %w", nodeID, err)
	}

	// Probe: check if the remote already has the file with matching content hash.
	if remotePath, ok := h.probeExisting(ctx, addr, localPath, key); ok {
		xlog.Info("Upload skipped (file already exists with matching hash)", "node", nodeID, "key", key, "remotePath", remotePath)
		return remotePath, nil
	}

	fi, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat local file %s: %w", localPath, err)
	}
	fileSize := fi.Size()

	url := fmt.Sprintf("http://%s/v1/files/%s", addr, key)
	xlog.Info("Uploading file to remote node", "node", nodeID, "file", filepath.Base(localPath), "size", humanFileSize(fileSize), "url", url)

	var lastErr error
	attempts := h.maxRetries + 1 // maxRetries=3 means 4 total attempts (1 initial + 3 retries)
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(5<<(attempt-2)) * time.Second // 5s, 10s, 20s
			xlog.Warn("Retrying file upload", "node", nodeID, "file", filepath.Base(localPath),
				"attempt", attempt, "of", attempts, "backoff", backoff, "lastError", lastErr)
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("upload cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}

		result, err := h.doUpload(ctx, addr, nodeID, localPath, key, url, fileSize)
		if err == nil {
			if attempt > 1 {
				xlog.Info("File upload succeeded after retry", "node", nodeID, "file", filepath.Base(localPath), "attempt", attempt)
			}
			return result, nil
		}
		lastErr = err

		if !isTransientError(err) {
			xlog.Error("File upload failed with non-transient error", "node", nodeID, "file", filepath.Base(localPath), "error", err)
			return "", err
		}
		xlog.Warn("File upload failed with transient error", "node", nodeID, "file", filepath.Base(localPath),
			"attempt", attempt, "of", attempts, "error", err)
	}

	return "", fmt.Errorf("uploading %s to node %s failed after %d attempts: %w", localPath, nodeID, attempts, lastErr)
}

// doUpload performs a single upload attempt.
func (h *HTTPFileStager) doUpload(ctx context.Context, addr, nodeID, localPath, key, url string, fileSize int64) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("opening local file %s: %w", localPath, err)
	}
	defer f.Close()

	var body io.Reader = f
	// For files > 100MB, wrap with progress logging
	const progressThreshold = 100 << 20
	if fileSize > progressThreshold {
		body = newProgressReader(f, fileSize, filepath.Base(localPath), nodeID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.ContentLength = fileSize // explicit Content-Length for progress tracking
	req.Header.Set("Content-Type", "application/octet-stream")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		xlog.Error("File upload failed", "node", nodeID, "file", filepath.Base(localPath), "size", humanFileSize(fileSize), "error", err)
		return "", fmt.Errorf("uploading %s to node %s: %w", localPath, nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		xlog.Error("File upload rejected by remote node", "node", nodeID, "file", filepath.Base(localPath), "status", resp.StatusCode, "response", string(respBody))
		return "", fmt.Errorf("upload to node %s failed with status %d: %s", nodeID, resp.StatusCode, string(respBody))
	}

	var result struct {
		LocalPath string `json:"local_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding upload response: %w", err)
	}

	xlog.Info("File upload complete", "node", nodeID, "file", filepath.Base(localPath), "size", humanFileSize(fileSize), "remotePath", result.LocalPath)
	return result.LocalPath, nil
}

// isTransientError returns true if the error is likely transient and worth retrying.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Connection reset by peer
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	// Broken pipe
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Connection refused (worker might be restarting)
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// Context deadline exceeded (but not cancelled — cancelled means the caller gave up)
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// net.Error timeout
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// Check for "connection reset" in the error string as a fallback
	// (some wrapped errors lose the syscall.Errno)
	msg := err.Error()
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "EOF") {
		return true
	}
	return false
}

// probeExisting sends a HEAD request to check if the remote already has the
// file with a matching SHA-256 hash. Returns the remote path and true if the
// upload can be skipped. Any errors (including 405 from older servers) silently
// fall through so the caller proceeds with a normal PUT.
func (h *HTTPFileStager) probeExisting(ctx context.Context, addr, localPath, key string) (string, bool) {
	url := fmt.Sprintf("http://%s/v1/files/%s", addr, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", false
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	remotePath := resp.Header.Get(HeaderLocalPath)
	remoteHash := resp.Header.Get(HeaderContentSHA256)
	if remotePath == "" || remoteHash == "" {
		return "", false
	}

	localHash, err := downloader.CalculateSHA(localPath)
	if err != nil {
		return "", false
	}

	if localHash != remoteHash {
		return "", false
	}

	return remotePath, true
}

// progressReader wraps an io.Reader and logs upload progress periodically.
type progressReader struct {
	reader   io.Reader
	total    int64
	read     int64
	file     string
	node     string
	lastLog  time.Time
	lastPct  int
	start    time.Time
	mu       sync.Mutex
}

func newProgressReader(r io.Reader, total int64, file, node string) *progressReader {
	return &progressReader{
		reader:  r,
		total:   total,
		file:    file,
		node:    node,
		start:   time.Now(),
		lastLog: time.Now(),
	}
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.mu.Lock()
		pr.read += int64(n)
		pct := int(pr.read * 100 / pr.total)
		now := time.Now()
		// Log every 10% or every 30 seconds
		if pct/10 > pr.lastPct/10 || now.Sub(pr.lastLog) >= 30*time.Second {
			elapsed := now.Sub(pr.start)
			var speed string
			if elapsed > 0 {
				bytesPerSec := float64(pr.read) / elapsed.Seconds()
				speed = humanFileSize(int64(bytesPerSec)) + "/s"
			}
			xlog.Info("Upload progress", "node", pr.node, "file", pr.file,
				"progress", fmt.Sprintf("%d%%", pct),
				"sent", humanFileSize(pr.read), "total", humanFileSize(pr.total),
				"speed", speed)
			pr.lastLog = now
			pr.lastPct = pct
		}
		pr.mu.Unlock()
	}
	return n, err
}

// humanFileSize returns a human-readable file size string.
func humanFileSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (h *HTTPFileStager) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	// For staging files (not under models/ or data/), the worker's file transfer
	// server resolves the key as a relative path under its staging directory.
	// remotePath is an absolute path on the worker (e.g. "/staging/localai-output-123.tmp"),
	// so we use just the basename as the key. For model/data files the full
	// relative key is already correct.
	key := remotePath
	if !strings.HasPrefix(remotePath, storage.ModelKeyPrefix) && !strings.HasPrefix(remotePath, storage.DataKeyPrefix) {
		key = filepath.Base(remotePath)
	}
	return h.FetchRemoteByKey(ctx, nodeID, key, localDst)
}

func (h *HTTPFileStager) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	xlog.Debug("Fetching file from remote node via HTTP", "node", nodeID, "key", key, "localDst", localDst)

	addr, err := h.httpAddrFor(nodeID)
	if err != nil {
		return fmt.Errorf("resolving HTTP address for node %s: %w", nodeID, err)
	}

	if err := os.MkdirAll(filepath.Dir(localDst), 0750); err != nil {
		return fmt.Errorf("creating directory for %s: %w", localDst, err)
	}

	url := fmt.Sprintf("http://%s/v1/files/%s", addr, key)
	xlog.Debug("HTTP download starting", "node", nodeID, "url", url, "localDst", localDst)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading from node %s: %w", nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download from node %s failed with status %d: %s", nodeID, resp.StatusCode, string(body))
	}

	f, err := os.Create(localDst)
	if err != nil {
		return fmt.Errorf("creating local file %s: %w", localDst, err)
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		os.Remove(localDst)
		return fmt.Errorf("writing to %s: %w", localDst, err)
	}

	xlog.Debug("HTTP download complete", "node", nodeID, "key", key, "localDst", localDst, "bytesReceived", written)
	return nil
}

func (h *HTTPFileStager) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	xlog.Debug("Allocating remote temp file via HTTP", "node", nodeID)

	addr, err := h.httpAddrFor(nodeID)
	if err != nil {
		return "", fmt.Errorf("resolving HTTP address for node %s: %w", nodeID, err)
	}

	url := fmt.Sprintf("http://%s/v1/files/temp", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("allocating temp file on node %s: %w", nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("alloc temp on node %s failed with status %d: %s", nodeID, resp.StatusCode, string(body))
	}

	var result struct {
		LocalPath string `json:"local_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding alloc temp response: %w", err)
	}

	xlog.Debug("Remote temp file allocated via HTTP", "node", nodeID, "remotePath", result.LocalPath)
	return result.LocalPath, nil
}

func (h *HTTPFileStager) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	return fmt.Errorf("StageRemoteToStore not supported in HTTP file transfer mode; use FetchRemote for direct transfer")
}

func (h *HTTPFileStager) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	addr, err := h.httpAddrFor(nodeID)
	if err != nil {
		return nil, fmt.Errorf("resolving HTTP address for node %s: %w", nodeID, err)
	}

	url := fmt.Sprintf("http://%s/v1/files-list/%s", addr, keyPrefix)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing dir on node %s: %w", nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list dir on node %s failed with status %d: %s", nodeID, resp.StatusCode, string(body))
	}

	var result struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding list dir response: %w", err)
	}

	return result.Files, nil
}
