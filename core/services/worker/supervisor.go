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
}

const workerBackendFreeTimeout = 5 * time.Second

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
	freePorts []int                      // ports freed by stopBackend, reused before nextPort

	// backendLocks serializes gallery operations against the same on-disk
	// artifact. Two installs of different backends on the same worker run
	// concurrently (their handlers are each in a goroutine); two operations
	// on the same backend (install vs upgrade, or two parallel installs of
	// the same not-yet-cached backend) are serialized here so the gallery
	// download path doesn't race itself on the same directory.
	backendLocks map[string]*sync.Mutex
}

// startBackend starts a gRPC backend process on a dynamically allocated port.
// Returns the gRPC address.
func (s *backendSupervisor) startBackend(backend, backendPath string) (string, error) {
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

	// Allocate port — recycle freed ports first, then grow upward from basePort
	var port int
	if len(s.freePorts) > 0 {
		port = s.freePorts[len(s.freePorts)-1]
		s.freePorts = s.freePorts[:len(s.freePorts)-1]
	} else {
		port = s.nextPort
		s.nextPort++
	}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	clientAddr := fmt.Sprintf("127.0.0.1:%d", port)

	proc, err := s.ml.StartProcess(backendPath, backend, bindAddr)
	if err != nil {
		s.freePorts = append(s.freePorts, port)
		s.mu.Unlock()
		return "", fmt.Errorf("starting backend process: %w", err)
	}

	s.processes[backend] = &backendProcess{
		proc: proc,
		addr: clientAddr,
		port: port,
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
	s.freePorts = append(s.freePorts, bp.port)
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

// stopBackend stops the backend process(es) matching the given identifier.
// Accepts a bare modelID (stops every replica) or a full processKey
// (stops just that replica).
func (s *backendSupervisor) stopBackend(id string, force bool) {
	for _, key := range s.resolveProcessKeys(id) {
		s.stopBackendExact(key, force)
	}
}

// stopBackendExact stops the process under exactly this key. It marks the
// entry as stopping under the lock, then runs Free() and proc.Stop() after
// release so network I/O cannot block other supervisor operations. The entry
// and port remain reserved until process termination completes.
func (s *backendSupervisor) stopBackendExact(key string, force bool) {
	bp := s.beginBackendStop(key)
	if bp == nil {
		return
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

	xlog.Info("Stopping backend process", "backend", key, "addr", bp.addr, "force", force)
	stopErr := bp.proc.Stop()
	if stopErr != nil {
		xlog.Error("Error stopping backend process", "backend", key, "error", stopErr)
	}
	s.finishBackendStop(key, bp, stopErr)
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

func (s *backendSupervisor) finishBackendStop(key string, bp *backendProcess, stopErr error) {
	// Keep the process and port reserved until termination completes. Recycling
	// the port before this point can start a second backend on an address still
	// owned by the stuck process.
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, exists := s.processes[key]; !exists || current != bp {
		return
	}
	if stopErr != nil && bp.proc.IsAlive() {
		bp.stopping = false
		return
	}
	delete(s.processes, key)
	if bp.port <= 0 {
		xlog.Error("Cannot recycle backend port: process has invalid recorded port", "backend", key, "addr", bp.addr, "port", bp.port)
		return
	}
	s.freePorts = append(s.freePorts, bp.port)
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
// replica) or a full processKey (exact match). Callers like the
// backend.delete pre-check rely on the bare-name path.
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
