package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/workerctl"
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
// the duration of the call so the gallery directory isn't raced. Production
// serveInstall also holds lockInstallSlot through readiness and attachment.
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
// Returns the gRPC address of the backend process. downloadCb receives the
// gallery download ticks; nil keeps the install silent.
//
// ProcessKey includes the replica index so a worker with MaxReplicasPerModel>1
// can host multiple processes for the same model on distinct ports. Old
// controllers (no replica_index in the request) implicitly target replica 0,
// which preserves single-replica behavior.
func (s *backendSupervisor) installBackend(req workerctl.BackendInstallRequest, force bool, downloadCb func(file, current, total string, percentage float64)) (string, error) {
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
			case !s.processMatchesBackend(processKey, req.Backend) || !s.processMatchesEnv(processKey, req.EnvVars):
				// The slot is held by a process that must not serve this
				// backend: either it was started from a DIFFERENT backend
				// (this model's previous backend was deleted, or superseded by
				// a -development variant, while its process stayed up), or a
				// reinstall replaced the directory it is running out of, which
				// leaves its working directory a deleted inode. Either way,
				// reusing that address serves the load from a backend
				// directory that no longer exists. processMatchesBackend
				// reports the specific reason.
				xlog.Warn("Process for this model replica must not be reused; restarting it",
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
			// The load context: a shutdown cancels the download.
			if err := galleryop.InstallExternalBackend(
				s.loadContext(), galleries, s.systemState, s.ml, downloadCb, req.URI, req.Name, req.Alias, force, s.cfg.RequireBackendIntegrity,
			); err != nil {
				return "", fmt.Errorf("installing backend from gallery: %w", err)
			}
		} else {
			xlog.Info("Installing backend from gallery", "backend", req.Backend, "force", force)
			if err := gallery.InstallBackendFromGallery(
				s.loadContext(), galleries, s.systemState, s.ml, req.Backend, downloadCb, force, s.cfg.RequireBackendIntegrity,
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
	return s.startBackend(processKey, req.Backend, backendPath, req.EnvVars)
}

// upgradeOutcome is what an upgrade did besides its error: the processes it
// stopped and started again, and whether the new build passed the candidate
// start.
type upgradeOutcome struct {
	// stopped lists the process keys the upgrade terminated. It is
	// meaningful on the error paths too: those ports are recycled whatever
	// the outcome, so the controller drops the rows addressing them.
	stopped []string
	// restarted lists the stopped processes that run again after the upgrade.
	restarted []string
	// restartErr says why some stopped processes could not be started again.
	restartErr error
	// verified is true when the new build passed the candidate start.
	verified bool
}

// stoppedProcess is what is needed to start a stopped process again.
type stoppedProcess struct {
	key         string
	backendName string
	env         map[string]string
}

// upgradeCheckKeySuffix names the process of a candidate start. It never
// collides with a model process key (`model#<replica index>`).
const upgradeCheckKeySuffix = "#upgrade-check"

// upgradeBackend stops every running process for `backend`, force-reinstalls
// it from the gallery and starts the stopped processes again.
//
// The installed build is kept until the new build starts from its final path:
// the install moves it aside, the candidate start (startBackend, with the
// readiness check of a model load, on a process key of its own) runs the new
// build, and only when it answers is the old build removed. When it does not,
// the old build and its metadata are put back and registered, and the error
// has the code start and says the build was rolled back. No step of the check
// or the restore talks to NATS, so it works the same on a worker that holds
// only a tunnel (websocket or P2P).
//
// The caller is responsible for holding s.lockBackend(req.Backend).
//
// The stopped processes are started again on the build in place at the end,
// on new ports and with no model loaded (only the frontend can load a model).
// Their keys are still reported as stopped so the controller drops the rows
// that address the old ports; the next load reuses the running process.
// downloadCb is as for installBackend.
func (s *backendSupervisor) upgradeBackend(req workerctl.BackendUpgradeRequest, downloadCb func(file, current, total string, percentage float64)) (out upgradeOutcome, err error) {
	// Stop every live process for this backend (peer replicas + the bare
	// processKey). Same logic as the force branch in installBackend.
	toStop := s.resolveProcessKeysForBackend(s.backendIdentity(req.Backend))
	toStop = append(toStop, buildProcessKey("", req.Backend, int(req.ReplicaIndex)))
	var restart []stoppedProcess
	for _, key := range toStop {
		// The bare-backend key appended above is speculative — it exists only
		// on legacy entries. Reporting a key we never had would ask the
		// controller to drop a row that a different, still-running replica may
		// own, so only keys backed by a tracked process are reported.
		snapshot, tracked := s.stoppedSnapshot(key, req.Backend)
		xlog.Info("Upgrade: stopping running backend before reinstall",
			"backend", req.Backend, "processKey", key)
		if err := s.stopBackendExact(key, true); err != nil {
			return out, fmt.Errorf("stopping running backend before upgrade: %w", err)
		}
		if tracked {
			out.stopped = append(out.stopped, key)
			restart = append(restart, snapshot)
		}
	}
	// Whatever happens below, the processes that ran before run again. out
	// is a named result so the restart is part of what the caller gets.
	defer func() { out.restarted, out.restartErr = s.restartStopped(restart) }()

	galleries := s.galleries
	if req.BackendGalleries != "" {
		var reqGalleries []config.Gallery
		if err := json.Unmarshal([]byte(req.BackendGalleries), &reqGalleries); err != nil {
			return out, fmt.Errorf("decoding backend galleries: %w", err)
		}
		galleries = reqGalleries
	}

	check := gallery.WithStartCheck(s.candidateStartCheck(req.Backend, &out.verified))
	if req.URI != "" {
		xlog.Info("Upgrading backend from external URI", "backend", req.Backend, "uri", req.URI)
		if err := galleryop.InstallExternalBackend(
			context.Background(), galleries, s.systemState, s.ml, downloadCb, req.URI, req.Name, req.Alias, true, s.cfg.RequireBackendIntegrity, check,
		); err != nil {
			return out, fmt.Errorf("upgrading backend from external URI: %w", err)
		}
	} else {
		xlog.Info("Upgrading backend from gallery", "backend", req.Backend)
		if err := gallery.InstallBackendFromGallery(
			context.Background(), galleries, s.systemState, s.ml, req.Backend, downloadCb, true, /* force */
			s.cfg.RequireBackendIntegrity, check,
		); err != nil {
			return out, fmt.Errorf("upgrading backend from gallery: %w", err)
		}
	}

	if err := gallery.RegisterBackends(s.systemState, s.ml); err != nil {
		return out, gallery.WithUpgradeCode(gallery.UpgradeFailureSwap, fmt.Errorf("refreshing registered backends after upgrade: %w", err))
	}
	return out, nil
}

// stoppedSnapshot records what restarting the process under key needs, and
// whether the supervisor tracks a process there at all.
func (s *backendSupervisor) stoppedSnapshot(key, backend string) (stoppedProcess, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, ok := s.processes[key]
	if !ok || bp.addr == "" {
		return stoppedProcess{}, false
	}
	name := bp.backendName
	if name == "" {
		name = backend
	}
	return stoppedProcess{key: key, backendName: name, env: maps.Clone(bp.envVars)}, true
}

// candidateStartCheck is the start check of a worker upgrade: it starts the
// new build under a process key of its own with startBackend, so it gets the
// worker's port range, bind host, data token and readiness check, and stops it
// again whatever the result. verified is set when the build answered.
func (s *backendSupervisor) candidateStartCheck(backend string, verified *bool) gallery.StartCheck {
	return func(_ context.Context, runFile string) error {
		key := backend + upgradeCheckKeySuffix
		_, err := s.startBackend(key, backend, runFile)
		if stopErr := s.stopBackendExact(key, true); stopErr != nil {
			xlog.Warn("Could not stop the candidate start of an upgrade", "backend", backend, "error", stopErr)
		}
		if err != nil {
			return err
		}
		*verified = true
		return nil
	}
}

// restartStopped starts the processes an upgrade stopped again, on the build
// that is in place now. A process that cannot start is reported, not fatal:
// the next load of its model starts it.
func (s *backendSupervisor) restartStopped(stopped []stoppedProcess) ([]string, error) {
	var restarted []string
	var errs []error
	for _, p := range stopped {
		path := s.findBackend(p.backendName)
		if path == "" {
			errs = append(errs, fmt.Errorf("%s: backend %q not found", p.key, p.backendName))
			continue
		}
		if _, err := s.startBackend(p.key, p.backendName, path, p.env); err != nil {
			xlog.Warn("Could not start a stopped backend process again after the upgrade", "processKey", p.key, "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", p.key, err))
			continue
		}
		restarted = append(restarted, p.key)
	}
	return restarted, errors.Join(errs...)
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

// lockInstallSlot protects one complete serveInstall transaction, including
// readiness and operation attachment. It is acquired after lockBackend. Stops
// (including upgrade's multi-slot stops) must not acquire it: they can interrupt
// startup, and must not wait on an install holding an artifact lock they need.
func (s *backendSupervisor) lockInstallSlot(key string) func() {
	s.mu.Lock()
	if s.installSlotLocks == nil {
		s.installSlotLocks = make(map[string]*sync.Mutex)
	}
	m := s.installSlotLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		s.installSlotLocks[key] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}
