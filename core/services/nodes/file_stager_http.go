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

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/httpclient"
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
		// No Timeout set — for large uploads, http.Client.Timeout covers the
		// entire request lifecycle including the body upload. If it fires
		// mid-write, Go closes the connection causing "connection reset by peer"
		// on the server. Instead we use ResponseHeaderTimeout on the transport
		// to cover only the wait-for-server-response phase.
		client:          httpclient.New(httpclient.WithTransport(transport)),
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

	// Compute the SHA-256 of the local file once and bind it to every PUT
	// attempt — the server uses it to detect mid-flight content drift and
	// reject (409) if a partial upload claims a new identity, forcing a clean
	// restart.
	localHash, err := downloader.CalculateSHA(localPath)
	if err != nil {
		// Hash failure isn't fatal — we can still upload; we just lose
		// resume-safety and end-of-transfer integrity checks.
		xlog.Warn("Failed to hash local file for upload integrity check", "localPath", localPath, "error", err)
		localHash = ""
	}

	xlog.Info("Uploading file to remote node", "node", nodeID, "file", filepath.Base(localPath), "size", humanFileSize(fileSize), "url", url)

	// Outer time budget: bound the total resumable-upload duration so a
	// permanently-unreachable worker doesn't hold the request forever.
	resumeCtx, cancel, outerBudget := h.resumeContext(ctx)
	defer cancel()

	var lastErr error
	attempt := 0
	for {
		attempt++
		if attempt > 1 {
			backoff := nextBackoff(attempt)
			xlog.Warn("Retrying file upload", "node", nodeID, "file", filepath.Base(localPath),
				"attempt", attempt, "backoff", backoff, "lastError", lastErr)
			select {
			case <-resumeCtx.Done():
				return "", fmt.Errorf("upload cancelled during retry backoff (after %d attempts): %w (last: %v)", attempt-1, resumeCtx.Err(), lastErr)
			case <-time.After(backoff):
			}
		}

		// Determine resume offset from the server before each attempt. A
		// HEAD response that reports an in-progress upload (X-Target-SHA256)
		// matching ours unlocks resume from the reported size; any other
		// outcome (missing file, hash mismatch, partial-of-different-file)
		// resets to 0 and uploads the entire file.
		startOffset := h.resumeOffset(resumeCtx, addr, key, localHash, fileSize)

		result, err := h.doUpload(ctx, resumeCtx, addr, nodeID, localPath, key, url, fileSize, startOffset, localHash)
		if err == nil {
			if attempt > 1 {
				xlog.Info("File upload succeeded after retry", "node", nodeID, "file", filepath.Base(localPath), "attempt", attempt)
			}
			return result, nil
		}
		lastErr = err

		// Non-transient failures (4xx other than 416, hard auth, etc.) abort
		// immediately — retrying won't help.
		if !isTransientError(err) {
			xlog.Error("File upload failed with non-transient error", "node", nodeID, "file", filepath.Base(localPath), "error", err)
			return "", err
		}

		// Caller-cancelled (not deadline) — give up.
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", fmt.Errorf("upload cancelled by caller after %d attempts: %w", attempt, lastErr)
		}

		// Outer budget exhausted.
		if errors.Is(resumeCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("uploading %s to node %s failed after %d attempts within %s budget: %w",
				localPath, nodeID, attempt, outerBudget, lastErr)
		}

		xlog.Warn("File upload failed with transient error", "node", nodeID, "file", filepath.Base(localPath),
			"attempt", attempt, "error", err)
	}
}

// resumeContext bounds the resumable upload loop so it can't spin forever, and
// returns the budget it ended up with for error reporting.
//
// When the caller already imposed a deadline, that deadline IS the budget:
// nesting a second one under it was actively misleading. In production a 1h
// resume budget sat inside a 25m cold-load ceiling, so the inner budget was
// unreachable and the failure still reported "failed after 1 attempts within
// 1h0m0s budget" while the real killer was the 25m parent. Worse, the parent's
// cold-load hold is now progress-extended (see load_deadline.go) precisely so a
// 600 GB transfer can run for hours — a fixed 1h nested inside it would
// reintroduce the very size cliff that change removes.
//
// The fixed fallback only applies when nobody above bounded the transfer.
func (h *HTTPFileStager) resumeContext(ctx context.Context) (context.Context, context.CancelFunc, time.Duration) {
	if deadline, ok := ctx.Deadline(); ok {
		resumeCtx, cancel := context.WithCancel(ctx)
		return resumeCtx, cancel, time.Until(deadline)
	}
	resumeCtx, cancel := context.WithTimeout(ctx, defaultResumeBudget)
	return resumeCtx, cancel, defaultResumeBudget
}

// defaultResumeBudget bounds an unparented resumable upload. 1h covers multi-GB
// transfers on pathological links without letting a wedged server jam the
// master. Callers that need longer (a cold load staging a 600 GB checkpoint)
// impose their own, progress-extended deadline instead.
const defaultResumeBudget = 1 * time.Hour

// nextBackoff returns the sleep before retry #attempt: 1s, 2s, 4s, ..., capped
// at 30s, with the first sleep (attempt=2) being 1s.
func nextBackoff(attempt int) time.Duration {
	if attempt < 2 {
		return 0
	}
	const (
		base    = 1 * time.Second
		ceiling = 30 * time.Second
	)
	shift := uint(attempt - 2)
	if shift > 30 {
		shift = 30 // saturate before time.Duration overflows
	}
	b := base << shift
	if b > ceiling || b < 0 {
		b = ceiling
	}
	return b
}

// resumeOffset asks the server (via HEAD) how many bytes of the current upload
// are already on disk. It returns 0 if the server has no usable partial state
// (no file, finished file with a different hash, or a partial under a
// different target hash). It returns the server-reported size when the
// server's X-Target-SHA256 matches our expected final hash AND the size is
// strictly less than the local file size.
func (h *HTTPFileStager) resumeOffset(ctx context.Context, addr, key, localHash string, fileSize int64) int64 {
	if localHash == "" || fileSize <= 0 {
		return 0
	}
	url := fmt.Sprintf("http://%s/v1/files/%s", addr, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0
	}

	sizeStr := resp.Header.Get(HeaderFileSize)
	if sizeStr == "" {
		return 0
	}
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil || size <= 0 || size >= fileSize {
		return 0
	}

	target := resp.Header.Get(HeaderTargetSHA256)
	if target == "" || !strings.EqualFold(target, localHash) {
		// No partial-upload metadata, or it's for a different target.
		return 0
	}

	xlog.Info("Resuming upload from server-reported offset", "key", key, "offset", size, "total", fileSize)
	return size
}

// doUpload performs a single upload attempt. When startOffset > 0 the request
// is sent as a resumable PUT with a Content-Range header, transferring only
// the bytes from startOffset to fileSize-1. The outerCtx is the long-lived
// resume budget; reqCtx is what's bound to the request (currently the same as
// the parent ctx, since http.Client doesn't expose a per-request timeout).
func (h *HTTPFileStager) doUpload(ctx, outerCtx context.Context, addr, nodeID, localPath, key, url string, fileSize, startOffset int64, expectedHash string) (string, error) {
	if startOffset < 0 || startOffset > fileSize {
		startOffset = 0
	}

	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("opening local file %s: %w", localPath, err)
	}
	defer f.Close()

	if startOffset > 0 {
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return "", fmt.Errorf("seeking to offset %d in %s: %w", startOffset, localPath, err)
		}
	}

	chunkLen := fileSize - startOffset

	var body io.Reader = f
	cb := StagingProgressFromContext(ctx)
	// For files > 100MB or when a progress callback is set, wrap with progress reporting.
	// We report against the FULL fileSize (not the chunkLen) so a resumed upload's
	// progress bar starts from the actual completed fraction rather than at 0%.
	const progressThreshold = 100 << 20
	if fileSize > progressThreshold || cb != nil {
		pr := newProgressReader(f, fileSize, filepath.Base(localPath), nodeID, cb)
		pr.read = startOffset // seed prior progress
		body = pr
	}

	// The body length we actually send.
	limitedBody := io.LimitReader(body, chunkLen)

	req, err := http.NewRequestWithContext(outerCtx, http.MethodPut, url, limitedBody)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.ContentLength = chunkLen
	req.Header.Set("Content-Type", "application/octet-stream")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	if expectedHash != "" {
		// Lets the server detect cross-attempt content drift and reject
		// resume with 409 if the local file changed identity.
		req.Header.Set(HeaderContentSHA256, expectedHash)
	}
	if startOffset > 0 || (expectedHash != "" && fileSize > 0) {
		// Send Content-Range even on the first chunk (0-...) when we have an
		// expected hash, so the server's range-aware branch records the
		// target-hash sidecar for future resume attempts.
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", startOffset, fileSize-1, fileSize))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		xlog.Error("File upload failed", "node", nodeID, "file", filepath.Base(localPath),
			"size", humanFileSize(fileSize), "offset", startOffset, "error", err)
		return "", fmt.Errorf("uploading %s to node %s: %w", localPath, nodeID, err)
	}
	defer resp.Body.Close()

	// 308 Permanent Redirect ("Resume Incomplete") means the chunk landed but
	// the upload as a whole hasn't completed. From our perspective the
	// connection survived and the server has more bytes than before — but
	// since we always send the whole remainder, hitting 308 means the server
	// truncated us. Treat as transient so the retry loop re-HEADs and tries
	// again from the new offset.
	if resp.StatusCode == http.StatusPermanentRedirect {
		body, _ := io.ReadAll(resp.Body)
		return "", &transientStatusError{status: resp.StatusCode, msg: fmt.Sprintf("server reports resume-incomplete: %s", string(body))}
	}

	// 416 Range Not Satisfiable: client/server disagree on offset. Treat as
	// transient — the next iteration re-HEADs to learn the correct offset.
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		body, _ := io.ReadAll(resp.Body)
		return "", &transientStatusError{status: resp.StatusCode, msg: fmt.Sprintf("range not satisfiable: %s", string(body))}
	}

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

// transientStatusError wraps an HTTP status that should be treated as
// transient by the upload retry loop.
type transientStatusError struct {
	status int
	msg    string
}

func (e *transientStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, e.msg)
}

func (e *transientStatusError) Transient() bool { return true }

// isTransientError returns true if the error is likely transient and worth retrying.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Errors that explicitly opt into transient semantics (e.g. 308/416 from
	// the resumable-upload protocol).
	var transient interface{ Transient() bool }
	if errors.As(err, &transient) && transient.Transient() {
		return true
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
// If a StagingProgressCallback is present in the context, it also calls it
// for UI-visible progress updates.
type progressReader struct {
	reader     io.Reader
	total      int64
	read       int64
	file       string
	node       string
	lastLog    time.Time
	lastPct    int
	start      time.Time
	mu         sync.Mutex
	progressCb StagingProgressCallback
}

func newProgressReader(r io.Reader, total int64, file, node string, cb StagingProgressCallback) *progressReader {
	return &progressReader{
		reader:     r,
		total:      total,
		file:       file,
		node:       node,
		start:      time.Now(),
		lastLog:    time.Now(),
		progressCb: cb,
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
		// Call external progress callback for UI visibility
		if pr.progressCb != nil {
			pr.progressCb(pr.file, pr.read, pr.total)
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

	// Wrap response body with progress reporting if callback is set or file is large
	var src io.Reader = resp.Body
	cb := StagingProgressFromContext(ctx)
	totalSize := resp.ContentLength
	const progressThreshold = 100 << 20
	if totalSize > progressThreshold || cb != nil {
		if totalSize <= 0 {
			totalSize = 0 // unknown size — progress reader will still report bytes
		}
		src = newProgressReader(resp.Body, totalSize, filepath.Base(key), nodeID, cb)
	}

	written, err := io.Copy(f, src)
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
