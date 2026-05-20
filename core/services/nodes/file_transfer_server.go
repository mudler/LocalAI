package nodes

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// Headers used by the HEAD probe / skip-if-exists protocol.
const (
	HeaderContentSHA256 = "X-Content-SHA256"
	HeaderLocalPath     = "X-Local-Path"
	HeaderFileSize      = "X-File-Size"
	hashSidecarSuffix   = ".sha256"
)

// StartFileTransferServer starts a small HTTP server for file transfer in distributed mode.
// It provides PUT/GET/POST endpoints for uploading, downloading, and allocating temp files,
// as well as backend log REST and WebSocket endpoints when logStore is non-nil.
// Auth is via Bearer token (registration token), using constant-time comparison.
func StartFileTransferServer(addr, stagingDir, modelsDir, dataDir, token string, maxUploadSize int64, logStore ...*model.BackendLogStore) (*http.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return StartFileTransferServerWithListener(listener, stagingDir, modelsDir, dataDir, token, maxUploadSize, logStore...)
}

// StartFileTransferServerWithListener starts the server on an existing listener.
// This avoids the TOCTOU race of closing a listener and re-binding to the same port.
func StartFileTransferServerWithListener(lis net.Listener, stagingDir, modelsDir, dataDir, token string, maxUploadSize int64, logStore ...*model.BackendLogStore) (*http.Server, error) {
	if err := os.MkdirAll(stagingDir, 0750); err != nil {
		return nil, fmt.Errorf("creating staging dir %s: %w", stagingDir, err)
	}

	mux := http.NewServeMux()

	// PUT /v1/files/{key} — upload file
	// GET /v1/files/{key} — download file
	// POST /v1/files/temp — allocate temp file
	// GET /v1/files-list/{key} — list files in a directory
	mux.HandleFunc("/v1/files-list/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearerToken(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/v1/files-list/")
		handleListDir(w, r, stagingDir, modelsDir, dataDir, key)
	})

	mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearerToken(r, token) {
			xlog.Debug("HTTP file transfer: unauthorized request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Extract key from path: /v1/files/{key}
		key := strings.TrimPrefix(r.URL.Path, "/v1/files/")
		xlog.Debug("HTTP file transfer request", "method", r.Method, "key", key, "remote", r.RemoteAddr)

		switch r.Method {
		case http.MethodHead:
			handleHead(w, r, stagingDir, modelsDir, dataDir, key)
		case http.MethodPut:
			handleUpload(w, r, stagingDir, modelsDir, dataDir, key, maxUploadSize)
		case http.MethodGet:
			handleDownload(w, r, stagingDir, modelsDir, dataDir, key)
		case http.MethodPost:
			if key == "temp" {
				handleAllocTemp(w, r, stagingDir)
			} else {
				http.Error(w, "not found", http.StatusNotFound)
			}
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Backend log endpoints (only registered when a log store is provided)
	var ls *model.BackendLogStore
	if len(logStore) > 0 && logStore[0] != nil {
		ls = logStore[0]
	}
	if ls != nil {
		registerBackendLogHandlers(mux, token, ls)
	}

	// Liveness/readiness probes — unauthenticated so container orchestrators
	// (Docker HEALTHCHECK, k8s probes) can hit them without the bearer token.
	// Reaching this point means the listener is bound and the mux is serving.
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/readyz", healthHandler)
	mux.HandleFunc("/healthz", healthHandler)

	addr := lis.Addr().String()
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second, // prevent slowloris; does not affect body reads
	}

	go func() {
		xlog.Info("HTTP file transfer server started", "addr", addr, "stagingDir", stagingDir, "modelsDir", modelsDir, "dataDir", dataDir)
		if err := server.Serve(lis); err != nil && err != http.ErrServerClosed {
			xlog.Error("HTTP file transfer server error", "error", err)
		}
	}()

	return server, nil
}

func handleHead(w http.ResponseWriter, r *http.Request, stagingDir, modelsDir, dataDir, key string) {
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	targetDir, relName := resolveKeyToDir(key, stagingDir, modelsDir, dataDir)
	filePath := filepath.Join(targetDir, relName)

	if err := validatePathInDir(filePath, targetDir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("stat error: %v", err), http.StatusInternalServerError)
		}
		return
	}
	if info.IsDir() {
		http.Error(w, "is a directory", http.StatusBadRequest)
		return
	}

	w.Header().Set(HeaderFileSize, strconv.FormatInt(info.Size(), 10))
	w.Header().Set(HeaderLocalPath, filePath)

	hashHex, err := computeAndCacheHash(filePath)
	if err != nil {
		xlog.Warn("Failed to compute hash for HEAD", "path", filePath, "error", err)
	} else {
		w.Header().Set(HeaderContentSHA256, hashHex)
	}

	w.WriteHeader(http.StatusOK)
}

func handleUpload(w http.ResponseWriter, r *http.Request, stagingDir, modelsDir, dataDir, key string, maxUploadSize int64) {
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	if maxUploadSize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	}

	xlog.Info("Receiving file upload", "key", key, "contentLength", r.ContentLength, "remote", r.RemoteAddr)

	// Route keyed files to the appropriate directory
	targetDir, relName := resolveKeyToDir(key, stagingDir, modelsDir, dataDir)

	dstPath := filepath.Join(targetDir, relName)

	if err := validatePathInDir(dstPath, targetDir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0750); err != nil {
		http.Error(w, fmt.Sprintf("creating parent dir: %v", err), http.StatusInternalServerError)
		return
	}

	f, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("creating file: %v", err), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	hasher := sha256.New()
	n, err := io.Copy(f, io.TeeReader(r.Body, hasher))
	if err != nil {
		os.Remove(dstPath)
		os.Remove(dstPath + hashSidecarSuffix)
		xlog.Error("File upload failed", "key", key, "bytesReceived", n, "contentLength", r.ContentLength, "remote", r.RemoteAddr, "error", err)
		http.Error(w, fmt.Sprintf("writing file: %v", err), http.StatusInternalServerError)
		return
	}

	hashHex := hex.EncodeToString(hasher.Sum(nil))
	if err := os.WriteFile(dstPath+hashSidecarSuffix, []byte(hashHex), 0640); err != nil {
		xlog.Warn("Failed to write hash sidecar", "path", dstPath+hashSidecarSuffix, "error", err)
	}

	xlog.Info("File upload complete", "key", key, "path", dstPath, "size", n, "sha256", hashHex)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"local_path": dstPath}); err != nil {
		xlog.Warn("Failed to encode upload response", "error", err)
	}
}

// computeAndCacheHash returns the SHA-256 hex digest for filePath.
// It reads a cached sidecar when available and still fresh (sidecar mtime >=
// file mtime), otherwise computes the hash and writes/updates the sidecar.
func computeAndCacheHash(filePath string) (string, error) {
	sidecar := filePath + hashSidecarSuffix

	fileStat, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}

	if sidecarStat, err := os.Stat(sidecar); err == nil && !sidecarStat.ModTime().Before(fileStat.ModTime()) {
		if data, err := os.ReadFile(sidecar); err == nil {
			h := strings.TrimSpace(string(data))
			if len(h) == 64 { // valid hex-encoded SHA-256
				return h, nil
			}
		}
	}

	hashHex, err := downloader.CalculateSHA(filePath)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(sidecar, []byte(hashHex), 0640); err != nil {
		xlog.Warn("Failed to write hash sidecar", "path", sidecar, "error", err)
	}
	return hashHex, nil
}

func handleDownload(w http.ResponseWriter, r *http.Request, stagingDir, modelsDir, dataDir, key string) {
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	// Route keyed files to the appropriate directory
	targetDir, relName := resolveKeyToDir(key, stagingDir, modelsDir, dataDir)

	srcPath := filepath.Join(targetDir, relName)

	if err := validatePathInDir(srcPath, targetDir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("opening file: %v", err), http.StatusInternalServerError)
		}
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	var size int64
	if err != nil {
		xlog.Warn("Failed to stat file for transfer", "path", srcPath, "error", err)
	} else {
		size = fi.Size()
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	written, err := io.Copy(w, f)
	if err != nil {
		xlog.Warn("Error during file transfer", "path", srcPath, "error", err)
	}

	xlog.Debug("HTTP file download complete", "key", key, "path", srcPath, "fileSize", size, "bytesSent", written)
}

func handleAllocTemp(w http.ResponseWriter, r *http.Request, stagingDir string) {
	if err := os.MkdirAll(stagingDir, 0750); err != nil {
		http.Error(w, fmt.Sprintf("creating staging dir: %v", err), http.StatusInternalServerError)
		return
	}

	f, err := os.CreateTemp(stagingDir, "localai-output-*.tmp")
	if err != nil {
		http.Error(w, fmt.Sprintf("creating temp file: %v", err), http.StatusInternalServerError)
		return
	}
	localPath := f.Name()
	f.Close()

	xlog.Debug("HTTP allocated temp file", "path", localPath)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"local_path": localPath}); err != nil {
		xlog.Warn("Failed to encode alloc-temp response", "error", err)
	}
}

func handleListDir(w http.ResponseWriter, r *http.Request, stagingDir, modelsDir, dataDir, key string) {
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	targetDir, relName := resolveKeyToDir(key, stagingDir, modelsDir, dataDir)

	dirPath := filepath.Join(targetDir, relName)

	if err := validatePathInDir(dirPath, targetDir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(dirPath)
	if err != nil || !info.IsDir() {
		http.Error(w, "directory not found", http.StatusNotFound)
		return
	}

	var files []string
	filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			rel, err := filepath.Rel(dirPath, path)
			if err == nil {
				files = append(files, rel)
			}
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"files": files}); err != nil {
		xlog.Warn("Failed to encode list-files response", "error", err)
	}
}

// resolveKeyToDir maps a storage key to the appropriate local directory and
// relative path. Keys prefixed with "models/" route to modelsDir, "data/" to
// dataDir, and everything else to stagingDir.
func resolveKeyToDir(key, stagingDir, modelsDir, dataDir string) (targetDir, relName string) {
	targetDir = stagingDir
	relName = key
	if rel, ok := strings.CutPrefix(key, storage.ModelKeyPrefix); ok && modelsDir != "" {
		return modelsDir, rel
	}
	if rel, ok := strings.CutPrefix(key, storage.DataKeyPrefix); ok && dataDir != "" {
		return dataDir, rel
	}
	return
}

// checkBearerToken validates a Bearer token from the Authorization header
// using constant-time comparison. Returns true if valid or if expectedToken is empty.
func checkBearerToken(r *http.Request, expectedToken string) bool {
	if expectedToken == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || auth[:7] != "Bearer " {
		return false
	}
	provided := auth[7:]
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expectedToken)) == 1
}

// validatePathInDir checks that targetPath is within the given base directory.
func validatePathInDir(targetPath, baseDir string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolving base dir: %w", err)
	}
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return fmt.Errorf("resolving base dir symlinks: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolving target path: %w", err)
	}
	realTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		// File may not exist yet (e.g. upload). Walk up to the first
		// existing ancestor so platform symlinks (e.g. /tmp → /private/tmp
		// on macOS) are resolved even for deeply nested new paths.
		remaining := filepath.Base(absTarget)
		dir := filepath.Dir(absTarget)
		for {
			resolved, resolveErr := filepath.EvalSymlinks(dir)
			if resolveErr == nil {
				realTarget = filepath.Join(resolved, remaining)
				break
			}
			remaining = filepath.Join(filepath.Base(dir), remaining)
			parent := filepath.Dir(dir)
			if parent == dir {
				// Reached filesystem root without resolving
				realTarget = filepath.Clean(absTarget)
				break
			}
			dir = parent
		}
	}

	if !strings.HasPrefix(realTarget, realBase+string(filepath.Separator)) && realTarget != realBase {
		return fmt.Errorf("path %q is outside allowed directory", targetPath)
	}
	return nil
}

// ShutdownFileTransferServer gracefully shuts down the HTTP file transfer server.
func ShutdownFileTransferServer(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 5 seconds
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		xlog.Error("HTTP file transfer server shutdown error", "error", err)
	}
}

// registerBackendLogHandlers adds REST and WebSocket endpoints for streaming
// backend process logs from the worker's BackendLogStore.
func registerBackendLogHandlers(mux *http.ServeMux, token string, logStore *model.BackendLogStore) {
	wsUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // no origin header = same-origin or non-browser
			}
			// Parse origin URL and compare host with request host
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}

	// GET /v1/backend-logs — list model IDs with logs
	// GET /v1/backend-logs/{modelId} — get buffered log lines
	// GET /v1/backend-logs/{modelId}/ws — WebSocket real-time streaming
	mux.HandleFunc("/v1/backend-logs", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearerToken(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(logStore.ListModels())
	})

	mux.HandleFunc("/v1/backend-logs/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearerToken(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse: /v1/backend-logs/{modelId} or /v1/backend-logs/{modelId}/ws
		rest := strings.TrimPrefix(r.URL.Path, "/v1/backend-logs/")
		if rest == "" {
			http.Error(w, "model ID required", http.StatusBadRequest)
			return
		}

		// Check for /ws suffix (WebSocket upgrade)
		if strings.HasSuffix(rest, "/ws") {
			modelID := strings.TrimSuffix(rest, "/ws")
			modelID, _ = url.PathUnescape(modelID)
			handleBackendLogsWS(w, r, logStore, modelID, &wsUpgrader)
			return
		}

		// REST: return buffered lines
		modelID, _ := url.PathUnescape(rest)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(logStore.GetLines(modelID))
	})
}

// handleBackendLogsWS serves a WebSocket connection that streams backend log lines
// for a specific model in real time. Follows the same protocol as the standalone
// /ws/backend-logs/:modelId endpoint: sends an initial batch, then streams new lines.
func handleBackendLogsWS(w http.ResponseWriter, r *http.Request, logStore *model.BackendLogStore, modelID string, upgrader *websocket.Upgrader) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		xlog.Debug("WebSocket upgrade failed for backend-logs", "error", err)
		return
	}
	defer ws.Close()

	ws.SetReadLimit(4096)
	ws.SetReadDeadline(time.Now().Add(90 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	conn := &backendLogsWSConn{Conn: ws}

	// Send existing lines as initial batch
	existingLines := logStore.GetLines(modelID)
	initialMsg := map[string]any{
		"type":  "initial",
		"lines": existingLines,
	}
	if err := conn.writeJSON(initialMsg); err != nil {
		xlog.Debug("WebSocket backend-logs initial write failed", "error", err)
		return
	}

	// Subscribe to new lines
	lineCh, unsubscribe := logStore.Subscribe(modelID)
	defer unsubscribe()

	// Handle close from client side
	closeCh := make(chan struct{})
	go func() {
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				close(closeCh)
				return
			}
		}
	}()

	// Ping ticker for keepalive
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			lineMsg := map[string]any{
				"type": "line",
				"line": line,
			}
			if err := conn.writeJSON(lineMsg); err != nil {
				xlog.Debug("WebSocket backend-logs write error", "error", err)
				return
			}
		case <-pingTicker.C:
			if err := conn.writePing(); err != nil {
				return
			}
		case <-closeCh:
			return
		}
	}
}

// backendLogsWSConn wraps a websocket connection with a mutex for safe concurrent writes.
type backendLogsWSConn struct {
	*websocket.Conn
	mu sync.Mutex
}

func (c *backendLogsWSConn) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	return c.Conn.WriteMessage(websocket.TextMessage, data)
}

func (c *backendLogsWSConn) writePing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return c.Conn.WriteMessage(websocket.PingMessage, nil)
}
