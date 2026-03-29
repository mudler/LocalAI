package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/xlog"
)

// HTTPFileStager implements FileStager using HTTP for environments without S3.
// Files are transferred between the frontend and backend nodes over a small
// HTTP server running alongside the gRPC backend process.
type HTTPFileStager struct {
	httpAddrFor func(nodeID string) (string, error)
	token       string
	client      *http.Client
}

// NewHTTPFileStager creates a new HTTP file stager.
// httpAddrFor should return the HTTP address (host:port) for the given node ID.
// token is the registration token used for authentication.
func NewHTTPFileStager(httpAddrFor func(nodeID string) (string, error), token string) *HTTPFileStager {
	timeout := 30 * time.Minute
	if v := os.Getenv("LOCALAI_FILE_TRANSFER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	return &HTTPFileStager{
		httpAddrFor: httpAddrFor,
		token:       token,
		client:      &http.Client{Timeout: timeout},
	}
}

func (h *HTTPFileStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	xlog.Debug("Staging file to remote node via HTTP", "node", nodeID, "localPath", localPath, "key", key)

	addr, err := h.httpAddrFor(nodeID)
	if err != nil {
		return "", fmt.Errorf("resolving HTTP address for node %s: %w", nodeID, err)
	}

	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("opening local file %s: %w", localPath, err)
	}
	defer f.Close()

	fi, _ := f.Stat()
	var fileSize int64
	if fi != nil {
		fileSize = fi.Size()
	}

	url := fmt.Sprintf("http://%s/v1/files/%s", addr, key)
	xlog.Debug("HTTP upload starting", "node", nodeID, "url", url, "localPath", localPath, "fileSize", fileSize)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading %s to node %s: %w", localPath, nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload to node %s failed with status %d: %s", nodeID, resp.StatusCode, string(body))
	}

	var result struct {
		LocalPath string `json:"local_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding upload response: %w", err)
	}

	xlog.Debug("HTTP upload complete", "node", nodeID, "remotePath", result.LocalPath, "fileSize", fileSize)
	return result.LocalPath, nil
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
