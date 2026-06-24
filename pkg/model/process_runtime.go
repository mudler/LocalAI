package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gofrs/flock"
	"github.com/mudler/xlog"
)

const (
	backendTempDirEnv       = "LOCALAI_BACKEND_TEMP_DIR"
	backendRuntimeDirPrefix = "process-"
	backendRuntimeMarker    = ".localai-backend-runtime"
	backendRuntimeMagic     = "localai-backend-runtime-v1\n"
)

// backendProcessRuntime owns both go-processmanager's state and all temporary
// files created by one backend process. The held lock distinguishes a live
// runtime from one abandoned when LocalAI was killed or crashed.
type backendProcessRuntime struct {
	dir     string
	tempDir string
	lock    *flock.Flock
	scratch sync.Once
	once    sync.Once
	// diagnosticsDone closes after the exit watcher has read the state files.
	diagnosticsDone chan struct{}
}

func backendRuntimeRoot() string {
	base := os.TempDir()
	if configured := os.Getenv(backendTempDirEnv); configured != "" {
		base = configured
	}
	// Always append a LocalAI- and user-specific namespace. Even if an operator
	// points the configurable base at /tmp, the sweeper never inspects unrelated
	// process-* directories in that shared parent.
	return filepath.Join(base, fmt.Sprintf("localai-%d", os.Getuid()), "backend-runtime")
}

func newBackendProcessRuntime() (*backendProcessRuntime, error) {
	root := backendRuntimeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("creating backend runtime root %s: %w", root, err)
	}

	// Serialize sweeping with creation. Otherwise a second LocalAI instance
	// could observe the new directory in the tiny window before its owner lock
	// is acquired and mistake it for an abandoned runtime.
	sweepLock := flock.New(filepath.Join(root, ".sweep.lock"))
	if err := sweepLock.Lock(); err != nil {
		return nil, fmt.Errorf("locking backend runtime root %s: %w", root, err)
	}
	defer func() {
		if err := sweepLock.Unlock(); err != nil {
			xlog.Warn("Failed to unlock backend runtime root", "root", root, "error", err)
		}
	}()

	sweepAbandonedBackendRuntimes(root)

	dir, err := os.MkdirTemp(root, backendRuntimeDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("creating backend process runtime under %s: %w", root, err)
	}
	if err := os.WriteFile(filepath.Join(dir, backendRuntimeMarker), []byte(backendRuntimeMagic), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("marking backend process runtime %s: %w", dir, err)
	}
	runtimeLock := flock.New(filepath.Join(dir, ".owner.lock"))
	if err := runtimeLock.Lock(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("locking backend process runtime %s: %w", dir, err)
	}
	tempDir := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		_ = runtimeLock.Unlock()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("creating backend scratch directory %s: %w", tempDir, err)
	}

	return &backendProcessRuntime{
		dir:             dir,
		tempDir:         tempDir,
		lock:            runtimeLock,
		diagnosticsDone: make(chan struct{}),
	}, nil
}

func sweepAbandonedBackendRuntimes(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		xlog.Warn("Failed to inspect backend runtime root", "root", root, "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), backendRuntimeDirPrefix) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		marker, err := os.ReadFile(filepath.Join(dir, backendRuntimeMarker))
		if err != nil || string(marker) != backendRuntimeMagic {
			continue
		}
		ownerLock := flock.New(filepath.Join(dir, ".owner.lock"))
		available, err := ownerLock.TryLock()
		if err != nil {
			xlog.Warn("Failed to inspect backend runtime ownership", "dir", dir, "error", err)
			continue
		}
		if !available {
			continue
		}
		if err := ownerLock.Unlock(); err != nil {
			xlog.Warn("Failed to release abandoned backend runtime lock", "dir", dir, "error", err)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			xlog.Warn("Failed to remove abandoned backend runtime", "dir", dir, "error", err)
		}
	}
}

func (r *backendProcessRuntime) cleanup() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.cleanupScratch()
		if err := r.lock.Unlock(); err != nil {
			xlog.Warn("Failed to unlock backend process runtime", "dir", r.dir, "error", err)
		}
		if err := os.RemoveAll(r.dir); err != nil {
			xlog.Warn("Failed to remove backend process runtime", "dir", r.dir, "error", err)
		}
	})
}

func (r *backendProcessRuntime) cleanupScratch() {
	if r == nil {
		return
	}
	r.scratch.Do(func() {
		if err := os.RemoveAll(r.tempDir); err != nil {
			xlog.Warn("Failed to remove backend scratch directory", "dir", r.tempDir, "error", err)
		}
	})
}

func backendTempEnvironment(env []string, tempDir string) []string {
	result := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, found := strings.Cut(entry, "=")
		if found && (key == "TMPDIR" || key == "TMP" || key == "TEMP") {
			continue
		}
		result = append(result, entry)
	}
	return append(result,
		"TMPDIR="+tempDir,
		"TMP="+tempDir,
		"TEMP="+tempDir,
	)
}
