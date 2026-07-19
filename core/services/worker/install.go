package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
)

// installProgressDebounce is the leading-edge window the worker uses when
// streaming download progress to the master. 250ms caps wire chatter at
// ~4 events/sec per in-flight install while still surfacing every
// meaningful percentage jump.
const installProgressDebounce = 250 * time.Millisecond

// buildProcessKey is the supervisor's stable identifier for a backend gRPC
// process. It includes the replica index so the same model can run multiple
// processes on a worker simultaneously without colliding on the same map slot
// or port. The "#N" suffix is purely internal — the controller never reads it.
func buildProcessKey(modelID, backend string, replicaIndex int) string {
	base := modelID
	if base == "" {
		base = backend
	}
	return fmt.Sprintf("%s#%d", base, replicaIndex)
}

// installBackend handles the backend.install flow. force=true is the
// upgrade path; force=false is the routine load path.
//
// The caller is responsible for holding s.lockBackend(req.Backend) for
// the duration of the call so the gallery directory isn't raced.
//
//  1. If already running for this (model, replica) slot AND force is false,
//     return existing address (the fast path used by routine load events that
//     just want to know which port a backend already serves on).
//  2. If force is true, stop any process(es) currently using this backend
//     so the gallery install can replace the on-disk artifact and the freshly
//     started process picks up the new binary. This is the upgrade path —
//     without it, every backend.install we receive after the first hits the
//     fast path and silently no-ops, leaving the cluster on a stale build.
//  3. Install backend from gallery (force passed through so existing artifacts
//     get overwritten on upgrade).
//  4. Find backend binary
//  5. Start gRPC process on a new port
//
// Returns the gRPC address of the backend process.
//
// ProcessKey includes the replica index so a worker with MaxReplicasPerModel>1
// can host multiple processes for the same model on distinct ports. Old
// controllers (no replica_index in the request) implicitly target replica 0,
// which preserves single-replica behavior.
func (s *backendSupervisor) installBackend(req messaging.BackendInstallRequest, force bool) (string, error) {
	processKey := buildProcessKey(req.ModelID, req.Backend, int(req.ReplicaIndex))

	if !force {
		// Fast path: already running for this model+replica → return existing
		// address. Verify liveness before trusting the cached entry: a process
		// that died without the supervisor noticing leaves a stale (key, addr)
		// pair, and getAddr would otherwise hand the controller an address
		// that immediately ECONNREFUSEDs. The reconciler then marks the
		// replica failed, retries the install, the supervisor says "already
		// running" again, and the cluster loops on a dead replica forever.
		if addr := s.getAddr(processKey); addr != "" {
			switch {
			case !s.processMatchesBackend(processKey, req.Backend):
				// The slot is held by a process started from a DIFFERENT
				// backend — typically this model's previous backend was
				// deleted (or superseded by a -development variant) while its
				// process stayed up. Reusing that address would serve the load
				// from a backend directory that may no longer exist on disk.
				xlog.Warn("Process for this model replica belongs to another backend; restarting it",
					"backend", req.Backend, "model", req.ModelID, "replica", req.ReplicaIndex, "addr", addr)
				if err := s.stopBackendExact(processKey, false); err != nil {
					return "", fmt.Errorf("stopping mismatched backend process before reinstall: %w", err)
				}
			case s.isRunning(processKey):
				xlog.Info("Backend already running for model replica", "backend", req.Backend, "model", req.ModelID, "replica", req.ReplicaIndex, "addr", addr)
				return addr, nil
			default:
				xlog.Warn("Stale process entry for backend (dead process); cleaning up before reinstall",
					"backend", req.Backend, "model", req.ModelID, "replica", req.ReplicaIndex, "addr", addr)
				if err := s.stopBackendExact(processKey, false); err != nil {
					xlog.Warn("Failed to clean up stale process entry", "processKey", processKey, "error", err)
				}
			}
		}
	} else {
		// Upgrade path: stop every live process that shares this backend so the
		// gallery install can overwrite the on-disk artifact and the restarted
		// process picks up the new binary. resolveProcessKeysForBackend finds
		// them by recorded backend name (and alias), which also covers peer
		// replicas (whisper#0, whisper#1, ...) on workers configured with
		// MaxReplicasPerModel>1. We also stop the exact processKey from the
		// request tuple, whose recorded name may be absent on legacy entries.
		// Both are no-ops on missing keys.
		toStop := s.resolveProcessKeysForBackend(s.backendIdentity(req.Backend))
		toStop = append(toStop, processKey)
		for _, key := range toStop {
			xlog.Info("Force install: stopping running backend before reinstall",
				"backend", req.Backend, "processKey", key)
			if err := s.stopBackendExact(key, true); err != nil {
				return "", fmt.Errorf("stopping running backend before reinstall: %w", err)
			}
		}
	}

	// Parse galleries from request (override local config if provided)
	galleries := s.galleries
	if req.BackendGalleries != "" {
		var reqGalleries []config.Gallery
		if err := json.Unmarshal([]byte(req.BackendGalleries), &reqGalleries); err != nil {
			return "", fmt.Errorf("decoding backend galleries: %w", err)
		}
		galleries = reqGalleries
	}

	// When the master tagged this install with an OpID, stream the
	// gallery download progress back to it on the per-op NATS subject.
	// Old masters that omit OpID stay on the silent path so they keep
	// working without changes. The publisher releases its mutex before
	// every Publish so a slow link never stalls the download loop, and
	// the deferred Flush guarantees a terminal-percentage event reaches
	// the master even when the install errors out.
	var downloadCb func(file, current, total string, percentage float64)
	if req.OpID != "" && s.nats != nil {
		publisher := nodes.NewDebouncedInstallProgressPublisher(s.nats, s.nodeID, req.OpID, req.Backend, installProgressDebounce)
		downloadCb = publisher.OnDownload
		defer publisher.Flush()
	}

	// On upgrade, run the gallery install path even if the binary already
	// exists on disk: findBackend would otherwise short-circuit and we'd
	// restart the same stale binary. The force flag passed to
	// InstallBackendFromGallery makes it overwrite the existing artifact.
	backendPath := ""
	if !force {
		backendPath = s.findBackend(req.Backend)
	}
	if backendPath == "" {
		if req.URI != "" {
			xlog.Info("Installing backend from external URI", "backend", req.Backend, "uri", req.URI, "force", force)
			if err := galleryop.InstallExternalBackend(
				context.Background(), galleries, s.systemState, s.ml, downloadCb, req.URI, req.Name, req.Alias, force, s.cfg.RequireBackendIntegrity,
			); err != nil {
				return "", fmt.Errorf("installing backend from gallery: %w", err)
			}
		} else {
			xlog.Info("Installing backend from gallery", "backend", req.Backend, "force", force)
			if err := gallery.InstallBackendFromGallery(
				context.Background(), galleries, s.systemState, s.ml, req.Backend, downloadCb, force, s.cfg.RequireBackendIntegrity,
			); err != nil {
				return "", fmt.Errorf("installing backend from gallery: %w", err)
			}
		}
		// Re-register after install and retry
		if err := gallery.RegisterBackends(s.systemState, s.ml); err != nil {
			return "", fmt.Errorf("refreshing registered backends after install: %w", err)
		}
		backendPath = s.findBackend(req.Backend)
	}

	if backendPath == "" {
		return "", fmt.Errorf("backend %q not found after install attempt", req.Backend)
	}

	xlog.Info("Found backend binary", "path", backendPath, "processKey", processKey)

	// Start the gRPC process on a new port (keyed by model, not just backend).
	// req.Backend is recorded on the process so a later backend.delete/stop can
	// find it by backend name.
	return s.startBackend(processKey, req.Backend, backendPath)
}

// upgradeBackend stops every running process for `backend`, force-reinstalls
// from gallery (overwriting the on-disk artifact), and re-registers backends.
// It does NOT start any new gRPC process — the next routine model load via
// backend.install will spawn a fresh process picking up the new binary.
//
// The caller is responsible for holding s.lockBackend(req.Backend).
//
// It returns the process keys it terminated so the controller can drop the
// NodeModel rows addressing them: an upgrade stops every process using the
// binary and starts none back up, recycling their gRPC ports while the rows
// still point at those addresses.
func (s *backendSupervisor) upgradeBackend(req messaging.BackendUpgradeRequest) ([]string, error) {
	// Stop every live process for this backend (peer replicas + the bare
	// processKey). Same logic as the force branch in installBackend.
	toStop := s.resolveProcessKeysForBackend(s.backendIdentity(req.Backend))
	toStop = append(toStop, buildProcessKey("", req.Backend, int(req.ReplicaIndex)))
	stopped := make([]string, 0, len(toStop))
	for _, key := range toStop {
		// The bare-backend key appended above is speculative — it exists only
		// on legacy entries. Reporting a key we never had would ask the
		// controller to drop a row that a different, still-running replica may
		// own, so only keys backed by a tracked process are reported.
		tracked := s.getAddr(key) != ""
		xlog.Info("Upgrade: stopping running backend before reinstall",
			"backend", req.Backend, "processKey", key)
		if err := s.stopBackendExact(key, true); err != nil {
			return stopped, fmt.Errorf("stopping running backend before upgrade: %w", err)
		}
		if tracked {
			stopped = append(stopped, key)
		}
	}

	galleries := s.galleries
	if req.BackendGalleries != "" {
		var reqGalleries []config.Gallery
		if err := json.Unmarshal([]byte(req.BackendGalleries), &reqGalleries); err != nil {
			return stopped, fmt.Errorf("decoding backend galleries: %w", err)
		}
		galleries = reqGalleries
	}

	// When the master tagged this upgrade with an OpID, stream gallery download
	// progress back on the per-op subject (reused from install — an upgrade is a
	// force-reinstall). Old masters omit OpID and stay on the silent path. The
	// deferred Flush guarantees a terminal-percentage event even if the upgrade
	// errors out, so the master's per-node bar never hangs mid-download.
	var downloadCb func(file, current, total string, percentage float64)
	if req.OpID != "" && s.nats != nil {
		publisher := nodes.NewDebouncedInstallProgressPublisher(s.nats, s.nodeID, req.OpID, req.Backend, installProgressDebounce)
		downloadCb = publisher.OnDownload
		defer publisher.Flush()
	}

	if req.URI != "" {
		xlog.Info("Upgrading backend from external URI", "backend", req.Backend, "uri", req.URI)
		if err := galleryop.InstallExternalBackend(
			context.Background(), galleries, s.systemState, s.ml, downloadCb, req.URI, req.Name, req.Alias, true, s.cfg.RequireBackendIntegrity,
		); err != nil {
			return stopped, fmt.Errorf("upgrading backend from external URI: %w", err)
		}
	} else {
		xlog.Info("Upgrading backend from gallery", "backend", req.Backend)
		if err := gallery.InstallBackendFromGallery(
			context.Background(), galleries, s.systemState, s.ml, req.Backend, downloadCb, true, /* force */
			s.cfg.RequireBackendIntegrity,
		); err != nil {
			return stopped, fmt.Errorf("upgrading backend from gallery: %w", err)
		}
	}

	if err := gallery.RegisterBackends(s.systemState, s.ml); err != nil {
		return stopped, fmt.Errorf("refreshing registered backends after upgrade: %w", err)
	}
	return stopped, nil
}

// findBackend looks for the backend binary in the backends path and system path.
func (s *backendSupervisor) findBackend(backend string) string {
	candidates := []string{
		filepath.Join(s.cfg.BackendsPath, backend),
		filepath.Join(s.cfg.BackendsPath, backend, backend),
		filepath.Join(s.cfg.BackendsSystemPath, backend),
		filepath.Join(s.cfg.BackendsSystemPath, backend, backend),
	}
	if uri := s.ml.GetExternalBackend(backend); uri != "" {
		if fi, err := os.Stat(uri); err == nil && !fi.IsDir() {
			return uri
		}
	}
	for _, path := range candidates {
		fi, err := os.Stat(path)
		if err == nil && !fi.IsDir() {
			return path
		}
	}
	return ""
}

// lockBackend returns a release function for a per-backend mutex. Different
// backend names lock independently. The first caller for a name allocates
// the mutex under s.mu; subsequent callers for the same name reuse it.
func (s *backendSupervisor) lockBackend(name string) func() {
	s.mu.Lock()
	if s.backendLocks == nil {
		s.backendLocks = make(map[string]*sync.Mutex)
	}
	m, ok := s.backendLocks[name]
	if !ok {
		m = &sync.Mutex{}
		s.backendLocks[name] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}
