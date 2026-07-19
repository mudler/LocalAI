package worker

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
)

// backendProcess represents a single gRPC backend process.
type backendProcess struct {
	proc     *process.Process
	addr     string // gRPC address (host:port)
	port     int
	stopping bool
	// backendName is the gallery backend this process was started for (e.g.
	// "cuda13-nvidia-l4t-arm64-longcat-video"). It is NOT derivable from the
	// map key: keys are `modelID#replicaIndex`, so without it a backend.delete
	// keyed on the backend name resolves to nothing and the process is
	// orphaned while its files are removed from disk.
	backendName string
}

const workerBackendFreeTimeout = 5 * time.Second

// backendIdentitySet returns every name that refers to the same on-disk
// backend as `name`: the concrete directory name plus its alias. Deletes
// arrive with the concrete name while installs arrive with whatever the model
// config declared (often the alias), so both must land on one identity.
//
// An unknown name yields just itself. A delete must never over-reach (or fail)
// because the gallery listing is unavailable or the entry is already gone.
func backendIdentitySet(backends gallery.SystemBackends, name string) map[string]struct{} {
	set := map[string]struct{}{name: {}}
	b, ok := backends[name]
	if !ok || b.Metadata == nil {
		return set
	}
	// Only this entry's own metadata is consulted. Two concrete backends can
	// share one alias (foo and foo-development); walking every candidate of
	// that alias would let deleting one reap the other's process.
	if b.Metadata.Name != "" {
		set[b.Metadata.Name] = struct{}{}
	}
	if b.Metadata.Alias != "" {
		set[b.Metadata.Alias] = struct{}{}
	}
	return set
}

// backendIdentity resolves `name` against this worker's installed backends.
// Callers must invoke it BEFORE deleting files: DeleteBackendFromSystem
// removes the metadata.json that carries the alias mapping.
func (s *backendSupervisor) backendIdentity(name string) map[string]struct{} {
	if s.systemState == nil {
		return map[string]struct{}{name: {}}
	}
	backends, err := gallery.ListSystemBackends(s.systemState)
	if err != nil {
		// Degrade to name-only matching rather than failing the operation.
		xlog.Warn("Could not list backends for alias resolution; matching on name only",
			"backend", name, "error", err)
		return map[string]struct{}{name: {}}
	}
	return backendIdentitySet(backends, name)
}

// backendSupervisor manages multiple backend gRPC processes on different ports.
// Each backend type (e.g., llama-cpp, bert-embeddings) gets its own process and port.
type backendSupervisor struct {
	cfg         *Config
	ml          *model.ModelLoader
	systemState *system.SystemState
	galleries   []config.Gallery
	nodeID      string
	nats        messaging.MessagingClient
	sigCh       chan<- os.Signal // send shutdown signal instead of os.Exit

	mu        sync.Mutex
	processes map[string]*backendProcess // key: backend name
	nextPort  int                        // next available port for new backends
	freePorts []int                      // ports out of quarantine, reused before nextPort

	// quarantinedPorts holds ports whose process has terminated but which are
	// not yet safe to re-bind, and portQuarantine is how long they wait.
	// Overridden only by tests; zero means defaultPortQuarantine.
	quarantinedPorts []quarantinedPort
	portQuarantine   time.Duration

	// backendLocks serializes gallery operations against the same on-disk
	// artifact. Two installs of different backends on the same worker run
	// concurrently (their handlers are each in a goroutine); two operations
	// on the same backend (install vs upgrade, or two parallel installs of
	// the same not-yet-cached backend) are serialized here so the gallery
	// download path doesn't race itself on the same directory.
	backendLocks map[string]*sync.Mutex
}

// defaultPortQuarantine is how long a released gRPC port waits before it can be
// re-bound by another backend.
//
// This is an interlock for one specific window and nothing more: between the
// moment this worker frees a port and the moment the controller processes our
// reply and drops the NodeModel rows naming that address, a row still resolves
// to a live listener. probeHealth verifies liveness, not identity, so a port
// re-bound inside that window is dispatched to as if it were the original
// backend. The window is a NATS round-trip plus a row delete, so seconds of
// slack are ample.
//
// Deliberately NOT derived from the controller's HealthCheckInterval or from
// the per-model miss threshold. Tying a worker-local constant to a
// controller-side cadence would be unsound: that cadence is operator-tunable
// and the per-model reaper can be switched off entirely with
// DisablePerModelHealthCheck, so the coupling would be silently wrong on some
// clusters. Eager row removal, not this delay, is what actually fixes stale
// rows; raising this value is not a substitute for it.
const defaultPortQuarantine = 15 * time.Second

// quarantinedPort is a released port that must not be re-bound until `until`.
type quarantinedPort struct {
	port  int
	until time.Time
}

// quarantineWindow returns the configured quarantine, defaulting when unset so
// the zero-value supervisor never degrades to immediate reuse.
func (s *backendSupervisor) quarantineWindow() time.Duration {
	if s.portQuarantine <= 0 {
		return defaultPortQuarantine
	}
	return s.portQuarantine
}

// allocatePort returns a gRPC port for a new backend process, preferring a
// previously released port over growing the range. Callers must hold s.mu.
func (s *backendSupervisor) allocatePort() int {
	s.sweepQuarantine()
	if len(s.freePorts) > 0 {
		port := s.freePorts[len(s.freePorts)-1]
		s.freePorts = s.freePorts[:len(s.freePorts)-1]
		return port
	}
	port := s.nextPort
	s.nextPort++
	return port
}

// sweepQuarantine moves ports whose quarantine has elapsed into the free pool.
// Sweeping lazily on allocation avoids a timer goroutine per stopped backend;
// the only observer of the free pool is allocation itself. Callers must hold
// s.mu.
func (s *backendSupervisor) sweepQuarantine() {
	if len(s.quarantinedPorts) == 0 {
		return
	}
	now := time.Now()
	held := s.quarantinedPorts[:0]
	for _, q := range s.quarantinedPorts {
		if now.Before(q.until) {
			held = append(held, q)
			continue
		}
		s.freePorts = append(s.freePorts, q.port)
	}
	s.quarantinedPorts = held
}

// releasePort returns a port to the allocator after a quarantine period.
// Callers must hold s.mu. See defaultPortQuarantine for why the port is not
// immediately reusable.
func (s *backendSupervisor) releasePort(port int) {
	s.quarantinedPorts = append(s.quarantinedPorts, quarantinedPort{
		port:  port,
		until: time.Now().Add(s.quarantineWindow()),
	})
}

// startBackend starts a gRPC backend process on a dynamically allocated port.
// Returns the gRPC address.
//
// `backend` is the process key (modelID#replica); `backendName` is the gallery
// backend it was started from, recorded so delete/stop can find this process
// by backend name later.
func (s *backendSupervisor) startBackend(backend, backendName, backendPath string) (string, error) {
	s.mu.Lock()

	// Already running?
	if bp, ok := s.processes[backend]; ok {
		if bp.stopping {
			s.mu.Unlock()
			return "", fmt.Errorf("backend %s is stopping", backend)
		}
		if bp.proc != nil && bp.proc.IsAlive() {
			s.mu.Unlock()
			return bp.addr, nil
		}
		// Process died — clean up and restart
		xlog.Warn("Backend process died unexpectedly, restarting", "backend", backend)
		delete(s.processes, backend)
	}

	port := s.allocatePort()
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	clientAddr := fmt.Sprintf("127.0.0.1:%d", port)

	proc, err := s.ml.StartProcess(backendPath, backend, bindAddr)
	if err != nil {
		s.releasePort(port)
		s.mu.Unlock()
		return "", fmt.Errorf("starting backend process: %w", err)
	}

	s.processes[backend] = &backendProcess{
		proc:        proc,
		addr:        clientAddr,
		port:        port,
		backendName: backendName,
	}
	xlog.Info("Backend process started", "backend", backend, "addr", clientAddr)

	// Capture reference before unlocking for race-safe health check.
	// Another goroutine could stopBackend and recycle the port while we poll.
	bp := s.processes[backend]
	s.mu.Unlock()

	// Wait for the gRPC server to be ready before reporting success.
	// Slow nodes (Jetson Orin doing first-boot CUDA init, large CGO libs)
	// can take 10-15s before the gRPC port accepts connections; the previous
	// 4s window made the worker reply Success on a not-yet-listening port,
	// which manifested upstream as "connect: connection refused" on the
	// frontend's first LoadModel dial.
	client := grpc.NewClientWithToken(clientAddr, false, nil, false, s.cfg.RegistrationToken)
	const (
		readinessPollInterval = 200 * time.Millisecond
		readinessTimeout      = 30 * time.Second
	)
	deadline := time.Now().Add(readinessTimeout)
	var lastHealthErr error
	for time.Now().Before(deadline) {
		time.Sleep(readinessPollInterval)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ok, healthErr := client.HealthCheck(ctx)
		if ok {
			cancel()
			// Verify the process wasn't stopped/replaced while health-checking.
			// A stopping entry remains in the map until process termination so its
			// port stays reserved, but it must not be advertised as ready.
			if !s.backendStartStillValid(backend, bp) {
				return "", fmt.Errorf("backend %s was stopped during startup", backend)
			}
			xlog.Debug("Backend gRPC server is ready", "backend", backend, "addr", clientAddr)
			return clientAddr, nil
		}
		if healthErr != nil {
			lastHealthErr = healthErr
		}
		cancel()

		// Check if the process died (e.g. OOM, CUDA error, missing libs)
		if !proc.IsAlive() {
			stderrTail := readLastLinesFromFile(proc.StderrPath(), 20)
			xlog.Warn("Backend process died during startup", "backend", backend, "healthError", lastHealthErr, "stderr", stderrTail)
			s.releaseBackendStart(backend, bp)
			return "", fmt.Errorf("backend process %s died during startup. Last stderr:\n%s", backend, stderrTail)
		}
	}

	// Readiness deadline exceeded. Returning success here would leave the
	// frontend with an unbound address (it dials, gets ECONNREFUSED, and
	// the operator sees a misleading "connection refused" instead of the
	// real cause). Stop the half-started process, recycle the port, and
	// surface the failure to the caller with the backend's stderr tail.
	stderrTail := readLastLinesFromFile(proc.StderrPath(), 20)
	xlog.Error("Backend gRPC server not ready before deadline; aborting install", "backend", backend, "addr", clientAddr, "timeout", readinessTimeout, "healthError", lastHealthErr, "stderr", stderrTail)
	if killErr := proc.Stop(); killErr != nil {
		xlog.Warn("Failed to stop unready backend process", "backend", backend, "error", killErr)
	}
	s.releaseBackendStart(backend, bp)
	return "", fmt.Errorf("backend %s did not become ready within %s. Last stderr:\n%s", backend, readinessTimeout, stderrTail)
}

// backendStartStillValid verifies that a successful readiness probe still
// belongs to the active startup attempt. Stop keeps an entry tracked while it
// terminates, so pointer identity alone is not enough.
func (s *backendSupervisor) backendStartStillValid(key string, bp *backendProcess) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.processes[key]
	return exists && current == bp && !current.stopping
}

// releaseBackendStart removes a failed startup and recycles its port only when
// the map still owns that exact attempt. A concurrent stop or replacement may
// already have removed it and recycled the port.
func (s *backendSupervisor) releaseBackendStart(key string, bp *backendProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, exists := s.processes[key]; !exists || current != bp {
		return
	}
	delete(s.processes, key)
	if bp.port <= 0 {
		xlog.Error("Cannot recycle backend port: startup has invalid recorded port", "backend", key, "addr", bp.addr, "port", bp.port)
		return
	}
	s.releasePort(bp.port)
}

// resolveProcessKeys turns a caller-supplied identifier into the set of
// process map keys it refers to. PR #9583 changed s.processes to be keyed by
// `modelID#replicaIndex`, but external NATS handlers still pass the bare
// model ID — without this resolver, those lookups silently no-op'd, so
// admin "Unload model" / "Delete backend" left the worker process alive.
//
//   - Exact match wins. Callers that already know the full processKey
//     (stopAllBackends iterating its own map) get exactly that entry.
//   - Else, an identifier without `#` is treated as a model prefix and
//     every `id#N` replica is returned.
//   - An identifier that contains `#` but doesn't match anything returns
//     nothing — no spurious prefix fallback when the caller was explicit.
func (s *backendSupervisor) resolveProcessKeys(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.processes[id]; ok {
		return []string{id}
	}
	if strings.Contains(id, "#") {
		return nil
	}
	prefix := id + "#"
	var keys []string
	for k := range s.processes {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys
}

// resolveProcessKeysForBackend returns the process keys of every process
// started for any name in `names` (the backend's identity set).
//
// resolveProcessKeys alone is not enough here: it resolves a bare *modelID*
// via the `id#N` prefix, but backend.delete/backend.stop carry a *backend*
// name, which never prefixes a `modelID#replica` key. That mismatch is what
// let a deleted backend's process survive with its files removed. Matching on
// the recorded backendName is the primary path; the prefix resolution is
// unioned in so legacy entries keyed by the backend name itself (installs with
// an empty modelID) keep resolving.
func (s *backendSupervisor) resolveProcessKeysForBackend(names map[string]struct{}) []string {
	seen := make(map[string]struct{})
	var keys []string
	add := func(k string) {
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}

	s.mu.Lock()
	for key, bp := range s.processes {
		if bp.backendName == "" {
			continue
		}
		if _, ok := names[bp.backendName]; ok {
			add(key)
		}
	}
	s.mu.Unlock()

	// Legacy fallback for entries predating backendName: an install with an
	// empty modelID keys the map by the backend name itself. Restricted to
	// entries with no recorded name so it cannot reap a process that belongs
	// to a different backend but whose modelID happens to equal this backend's
	// name or alias.
	for name := range names {
		for _, k := range s.resolveProcessKeys(name) {
			s.mu.Lock()
			bp, ok := s.processes[k]
			unnamed := ok && bp.backendName == ""
			s.mu.Unlock()
			if unnamed {
				add(k)
			}
		}
	}
	return keys
}

// resolveStopTargets resolves the ambiguous identifier carried on backend.stop.
//
// The payload field is named "backend", but it is published with both
// meanings: the admin backends UI sends a BACKEND name, while
// UnloadRemoteModel and the router's abandoned-load reap send a MODEL name (or
// an exact modelID#replica key). Resolving only one meaning silently strands
// the other — matching on backend name alone stops nothing for a model unload,
// and matching on the modelID prefix alone is the bug that orphaned deleted
// backends' processes. Stop is best-effort and idempotent, so accepting both is
// safe here; delete stays strict because its identifier is unambiguously a
// backend name.
func (s *backendSupervisor) resolveStopTargets(id string) []string {
	keys := s.resolveProcessKeysForBackend(s.backendIdentity(id))
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		seen[k] = struct{}{}
	}
	for _, k := range s.resolveProcessKeys(id) {
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	return keys
}

// processMatchesBackend reports whether the process under `key` may be reused
// to serve `backend`. The install fast path returns any live process for a
// (model, replica) slot; without this check a slot whose backend was deleted
// and replaced handed the new install the deleted backend's port, so the load
// ran against a directory that no longer exists.
//
// A process with no recorded backendName predates this field and is accepted:
// treating it as a mismatch would restart every running backend once on
// rollout.
func (s *backendSupervisor) processMatchesBackend(key, backend string) bool {
	s.mu.Lock()
	bp, ok := s.processes[key]
	if !ok {
		s.mu.Unlock()
		return false
	}
	recorded := bp.backendName
	s.mu.Unlock()

	if recorded == "" || recorded == backend {
		return true
	}
	// Names differ textually — they may still be alias and concrete for the
	// same on-disk backend, which is a legitimate reuse.
	_, match := s.backendIdentity(backend)[recorded]
	return match
}

// stopBackend stops the backend process(es) matching the given identifier.
// Accepts a bare modelID (stops every replica) or a full processKey
// (stops just that replica).
func (s *backendSupervisor) stopBackend(id string, force bool) {
	for _, key := range s.resolveProcessKeys(id) {
		if err := s.stopBackendExact(key, force); err != nil {
			xlog.Error("Failed to stop backend process", "processKey", key, "error", err)
		}
	}
}

// stopBackendExact stops the process under exactly this key. It marks the
// entry as stopping under the lock, then runs Free() and proc.Stop() after
// release so network I/O cannot block other supervisor operations. The entry
// and port remain reserved until process termination completes.
//
// Returns an error only when a process was found and is still alive after the
// stop attempt — a missing key is a no-op, not a failure. Callers that report
// success to an operator (backend.delete) must not claim success while the
// process keeps serving requests from a directory they just removed.
func (s *backendSupervisor) stopBackendExact(key string, force bool) error {
	bp := s.beginBackendStop(key)
	if bp == nil {
		return nil
	}

	if !force {
		client := grpc.NewClientWithToken(bp.addr, false, nil, false, s.cfg.RegistrationToken)
		freeCtx, cancel := context.WithTimeout(context.Background(), workerBackendFreeTimeout)
		xlog.Debug("Calling bounded Free() before stopping backend", "backend", key, "timeout", workerBackendFreeTimeout)
		if err := client.Free(freeCtx); err != nil {
			xlog.Warn("Free() failed (best-effort)", "backend", key, "error", err)
		}
		cancel()
	}

	xlog.Info("Stopping backend process", "backend", key, "addr", bp.addr, "force", force, "backendName", bp.backendName)
	stopErr := bp.proc.Stop()
	if stopErr != nil {
		xlog.Error("Error stopping backend process", "backend", key, "error", stopErr)
	}
	return s.finishBackendStop(key, bp, stopErr)
}

// beginBackendStop reserves both the process entry and its port while network
// cleanup and process termination run without the supervisor mutex.
func (s *backendSupervisor) beginBackendStop(key string) *backendProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, ok := s.processes[key]
	if !ok || bp.proc == nil || bp.stopping {
		return nil
	}
	bp.stopping = true
	return bp
}

// finishBackendStop returns a non-nil error when the process survived the stop
// attempt, so callers can distinguish a real reap from a failed one.
func (s *backendSupervisor) finishBackendStop(key string, bp *backendProcess, stopErr error) error {
	// Keep the process and port reserved until termination completes. Recycling
	// the port before this point can start a second backend on an address still
	// owned by the stuck process.
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, exists := s.processes[key]; !exists || current != bp {
		return nil
	}
	if stopErr != nil && bp.proc.IsAlive() {
		bp.stopping = false
		return fmt.Errorf("stopping backend process %s: %w", key, stopErr)
	}
	delete(s.processes, key)
	if bp.port <= 0 {
		xlog.Error("Cannot recycle backend port: process has invalid recorded port", "backend", key, "addr", bp.addr, "port", bp.port)
		return nil
	}
	s.releasePort(bp.port)
	return nil
}

// stopAllBackends stops all running backend processes.
func (s *backendSupervisor) stopAllBackends(force bool) {
	s.mu.Lock()
	backends := slices.Collect(maps.Keys(s.processes))
	s.mu.Unlock()

	for _, b := range backends {
		s.stopBackend(b, force)
	}
}

// isRunning returns whether at least one backend process matching the given
// identifier is currently running. Accepts a bare modelID (matches any
// replica) or a full processKey (exact match).
//
// It does NOT accept a backend name: backend names never prefix a
// modelID#replica key. Callers holding a backend name must go through
// resolveProcessKeysForBackend — assuming otherwise here is what left deleted
// backends' processes running.
func (s *backendSupervisor) isRunning(id string) bool {
	keys := s.resolveProcessKeys(id)
	if len(keys) == 0 {
		// Same lock-free zero-process check the caller would have done.
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		if bp, ok := s.processes[key]; ok && bp.proc != nil && bp.proc.IsAlive() {
			return true
		}
	}
	return false
}

// getAddr returns the gRPC address for a running backend, or empty string.
func (s *backendSupervisor) getAddr(backend string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bp, ok := s.processes[backend]; ok {
		return bp.addr
	}
	return ""
}
