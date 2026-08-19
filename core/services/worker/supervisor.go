package worker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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
	// backendDir is the directory the process was started out of — its
	// working directory, and the directory a reinstall replaces. backendDirID
	// is that directory's identity at spawn time, compared with os.SameFile so
	// a rebuilt directory at the same path is recognised as a different one.
	//
	// A working directory follows the inode across a rename, so a process that
	// outlives gallery.InstallBackend's rename-install-delete swap ends up with
	// a deleted inode as its CWD and every getcwd(2) in it fails with ENOENT.
	// Recording identity (not just the path) is what lets the reuse gate spot
	// such a survivor; matching on backendName alone cannot, because the name
	// is exactly what stays the same across a reinstall.
	backendDir   string
	backendDirID os.FileInfo
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
	nextPort  int                        // next unhanded-out port; grows within [minPort, maxPort]
	minPort   int                        // first port of the allocatable range
	maxPort   int                        // last port of the allocatable range (inclusive)
	freePorts []int                      // ports out of quarantine, reused before nextPort

	// portAffinity remembers which process key last held each port, so a
	// released port is offered back to that key before any other, and how long
	// that claim outlives the release. See allocatePort for why this is a
	// correctness property and not a nice-to-have.
	// portAffinityWindow is overridden only by tests; zero means
	// defaultPortAffinityWindow.
	portAffinity       map[string]portOwnership
	portAffinityWindow time.Duration

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

// ErrNoFreePort reports that every port in this worker's configured gRPC range
// is either bound by a live backend or still in quarantine.
var ErrNoFreePort = errors.New("no free gRPC port in range")

// defaultPortAffinityWindow is how long a released port stays reserved for the
// process key that last held it.
//
// It must comfortably outlive any controller NodeModel row that could still
// name the port, because that row is the whole reason affinity exists. The
// slowest such row is reaped by the per-model health check after
// perModelMissThreshold consecutive misses (~45s at the default cadence), and
// operators can widen that cadence, so this is set well above it rather than
// derived from it — the same reasoning as defaultPortQuarantine.
//
// It must also expire. Ownership held forever would make every distinct model
// a worker has ever served consume a port permanently, so the allocator would
// climb to the end of its range on distinct-key count rather than concurrency,
// then steal on every allocation while telling the operator to raise a ceiling
// that is not the constraint.
const defaultPortAffinityWindow = 5 * time.Minute

// portOwnership records the key that last held a port and, once released, when
// that claim lapses. A zero `until` means the port is currently held: it cannot
// be reallocated to anyone, so the claim does not need to age.
type portOwnership struct {
	port  int
	until time.Time
}

// expired reports whether this claim has lapsed and the port may now be handed
// to an unrelated key.
func (o portOwnership) expired(now time.Time) bool {
	return !o.until.IsZero() && now.After(o.until)
}

// affinityWindow returns the configured affinity lifetime, defaulting when
// unset so a zero-value supervisor never degrades to no affinity at all.
func (s *backendSupervisor) affinityWindow() time.Duration {
	if s.portAffinityWindow <= 0 {
		return defaultPortAffinityWindow
	}
	return s.portAffinityWindow
}

// defaultMaxPort bounds the allocator when the operator sets no explicit
// ceiling. The allocator used to increment without any bound at all, so past
// 65535 it handed out integers that cannot be bound and the operator saw an
// opaque "backend won't start" with nothing implicating the allocator.
const defaultMaxPort = 65535

// portBounds resolves the allocatable range, tolerating a zero-value
// supervisor. minPort defaults to wherever the range was seeded so that a
// supervisor built without an explicit floor never scans below its base port.
func (s *backendSupervisor) portBounds() (int, int) {
	minPort := s.minPort
	if minPort <= 0 {
		minPort = s.nextPort
	}
	maxPort := s.maxPort
	if maxPort <= 0 {
		maxPort = defaultMaxPort
	}
	return minPort, maxPort
}

// allocatePort returns a gRPC port for the process `key` will run under.
//
// Ports are handed back to the key that last held them before they are offered
// to anyone else. That preference is a correctness property, not a tidiness
// one. A process key is `modelID#replica` and a controller NodeModel row is
// keyed (nodeID, modelName, replicaIndex) — the two are isomorphic. So if a
// port can only ever be re-bound by the key that last held it, the only row
// that can name that port belongs to that same key, and that key's
// re-registration overwrites it. A stale row can therefore never resolve to a
// *different* model's backend, which is the silent misroute #10952 describes.
// The quarantine narrows the window for that misroute; affinity removes it.
//
// The claim lapses after defaultPortAffinityWindow, once no controller row can
// still name the port. Holding it forever would make every distinct model the
// worker has ever served consume a port permanently, so the allocator would
// climb to the end of its range on distinct-key count rather than on
// concurrency. Expiry keeps the guarantee for exactly as long as it buys
// anything.
//
// Nor is the preference a reservation: when the range would otherwise be
// exhausted a still-claimed port is stolen rather than failing the start. A
// rare misroute window is a better trade than a guaranteed outage, and by then
// the port is long out of quarantine. Because claims expire, reaching that
// branch means the worker is genuinely out of concurrent capacity, so the
// warning's advice to widen the range is the right advice.
//
// Callers must hold s.mu.
func (s *backendSupervisor) allocatePort(key string) (int, error) {
	s.sweepQuarantine()
	s.sweepAffinity()
	minPort, maxPort := s.portBounds()

	// 1. This key's own port, if it is back out of quarantine.
	if own, ok := s.portAffinity[key]; ok {
		if s.takeFreePort(own.port) {
			return s.claimPort(key, own.port), nil
		}
	}

	// 2. Any released port no live claim covers — either never owned, or owned
	// by a key whose window has lapsed, so no controller row can still name it.
	owners := s.portOwners()
	for i := len(s.freePorts) - 1; i >= 0; i-- {
		port := s.freePorts[i]
		if _, owned := owners[port]; owned {
			continue
		}
		s.freePorts = slices.Delete(s.freePorts, i, i+1)
		return s.claimPort(key, port), nil
	}

	// 3. Grow into ports never handed out, staying inside the range.
	if s.nextPort >= minPort && s.nextPort <= maxPort {
		port := s.nextPort
		s.nextPort++
		return s.claimPort(key, port), nil
	}

	// 4. Steal another key's port rather than refuse to start a backend.
	if len(s.freePorts) > 0 {
		port := s.freePorts[len(s.freePorts)-1]
		s.freePorts = s.freePorts[:len(s.freePorts)-1]
		xlog.Warn("gRPC port range is exhausted; reusing a port that belonged to another backend. A stale controller row for the previous owner could briefly misroute to this backend — raise LOCALAI_GRPC_MAX_PORT to restore headroom",
			"backend", key, "port", port, "previousOwner", owners[port], "min", minPort, "max", maxPort)
		return s.claimPort(key, port), nil
	}

	return 0, fmt.Errorf("%w: %d-%d is fully consumed by %d running backend(s) and %d port(s) still in quarantine; raise LOCALAI_GRPC_MAX_PORT to widen the range",
		ErrNoFreePort, minPort, maxPort, len(s.processes), len(s.quarantinedPorts))
}

// sweepAffinity drops claims whose window has lapsed, so their ports become
// ordinary free ports again. Swept lazily on allocation for the same reason as
// sweepQuarantine: the only observer is allocation itself, so a timer goroutine
// per released port would buy nothing. Callers must hold s.mu.
func (s *backendSupervisor) sweepAffinity() {
	if len(s.portAffinity) == 0 {
		return
	}
	now := time.Now()
	for key, own := range s.portAffinity {
		if own.expired(now) {
			delete(s.portAffinity, key)
		}
	}
}

// portOwners inverts the affinity map. Building it per allocation keeps a
// single source of truth: a second always-in-sync reverse map would be more
// state to get wrong, and allocation happens once per backend start.
func (s *backendSupervisor) portOwners() map[int]string {
	owners := make(map[int]string, len(s.portAffinity))
	for key, own := range s.portAffinity {
		owners[own.port] = key
	}
	return owners
}

// takeFreePort removes `port` from the free pool, reporting whether it was
// there to take.
func (s *backendSupervisor) takeFreePort(port int) bool {
	if i := slices.Index(s.freePorts, port); i >= 0 {
		s.freePorts = slices.Delete(s.freePorts, i, i+1)
		return true
	}
	return false
}

// claimPort records `key` as the owner of `port` and evicts whichever key owned
// it before.
//
// That eviction is what bounds the affinity map. Ownership is kept injective
// over ports, so the map can never hold more entries than the range has ports,
// no matter how many distinct model keys the worker sees over its lifetime.
// Without it the map would grow once per distinct key forever — the same
// unbounded-growth shape as the port leak this change fixes. Callers must hold
// s.mu.
// The claim is recorded without an expiry: the port is in use, so it cannot be
// reallocated to anyone and the claim has nothing to age against. The window
// starts when the port is released.
func (s *backendSupervisor) claimPort(key string, port int) int {
	if s.portAffinity == nil {
		s.portAffinity = make(map[string]portOwnership)
	}
	for owner, own := range s.portAffinity {
		if own.port == port && owner != key {
			delete(s.portAffinity, owner)
		}
	}
	s.portAffinity[key] = portOwnership{port: port}
	return port
}

// releasePortForKey returns `port` to the allocator, reserving it for `key` for
// the affinity window so that no unrelated model can bind it while a controller
// row for this key could still name it. Callers must hold s.mu.
func (s *backendSupervisor) releasePortForKey(key string, port int) {
	s.claimPort(key, port)
	own := s.portAffinity[key]
	own.until = time.Now().Add(s.affinityWindow())
	s.portAffinity[key] = own
	s.releasePort(port)
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
	// Mirrors the working directory pkg/model.startProcess hands the child.
	backendDir := filepath.Dir(backendPath)

	// A process that outlived a reinstall of this backend is still alive and
	// still recorded under this key, but its working directory is now a
	// deleted inode. The reuse branch below would hand that process straight
	// back to the caller; stop it first so the fresh spawn chdirs into the
	// newly installed directory. Forced, because a graceful Free() into a
	// process whose CWD no longer resolves buys nothing and can hang.
	if s.getAddr(backend) != "" && !s.backendDirIntact(backend) {
		if err := s.stopBackendExact(backend, true); err != nil {
			return "", fmt.Errorf("stopping backend process running from a replaced directory %s: %w", backend, err)
		}
	}

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
		s.reapDeadProcess(backend, bp)
	}

	port, err := s.allocatePort(backend)
	if err != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("allocating gRPC port for backend %s: %w", backend, err)
	}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	clientAddr := fmt.Sprintf("127.0.0.1:%d", port)

	proc, err := s.ml.StartProcess(backendPath, backend, bindAddr)
	if err != nil {
		s.releasePortForKey(backend, port)
		s.mu.Unlock()
		return "", fmt.Errorf("starting backend process: %w", err)
	}

	// Record the directory this process runs out of, and its identity, while
	// it is still the live one. pkg/model.startProcess passes exactly this
	// directory as the child's working directory, so it is what a later
	// reinstall unlinks out from under this process. A stat failure is not
	// fatal: leaving backendDirID nil degrades to today's name-only reuse
	// gate rather than refusing to start the backend.
	dirInfo, dirErr := os.Stat(backendDir)
	if dirErr != nil {
		xlog.Warn("Could not record backend directory identity; reuse gate degrades to name matching",
			"backend", backend, "dir", backendDir, "error", dirErr)
	}

	s.processes[backend] = &backendProcess{
		proc:         proc,
		addr:         clientAddr,
		port:         port,
		backendName:  backendName,
		backendDir:   backendDir,
		backendDirID: dirInfo,
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

// reapDeadProcess drops the bookkeeping for a process that exited without
// anyone asking it to (OOM kill, CUDA fault, a segfaulting shared library).
// Unlike every other teardown path there is no request/reply in flight here:
// nothing on the controller side is waiting to be told, and the death is only
// noticed lazily, when something tries to start this key again.
//
// This path used to drop the map entry without releasing the port, so every
// unexpected exit consumed one port permanently. A crash-looping backend leaks
// one per restart, which is what actually walks a long-lived worker to the end
// of its range — not the concurrent-peak growth #10961 assumed.
//
// Releasing it through the affinity-preserving path is what makes the recycle
// safe: nothing here can tell the controller its row is stale (there is no
// reply to carry the key, and the death is noticed only lazily), so the port
// must come back only to this same key. See allocatePort.
//
// Callers must hold s.mu.
func (s *backendSupervisor) reapDeadProcess(key string, bp *backendProcess) {
	xlog.Warn("Backend process died unexpectedly, restarting", "backend", key)
	delete(s.processes, key)
	if bp == nil {
		return
	}
	if bp.port <= 0 {
		xlog.Error("Cannot recycle backend port: dead process has invalid recorded port", "backend", key, "addr", bp.addr, "port", bp.port)
		return
	}
	s.releasePortForKey(key, bp.port)
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
	s.releasePortForKey(key, bp.port)
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
//
// The name check alone is not sufficient. A reinstall keeps the name and
// replaces the directory, which is the one case name matching cannot see, so
// backendDirIntact gates reuse first.
func (s *backendSupervisor) processMatchesBackend(key, backend string) bool {
	s.mu.Lock()
	bp, ok := s.processes[key]
	if !ok {
		s.mu.Unlock()
		return false
	}
	recorded := bp.backendName
	s.mu.Unlock()

	if !s.backendDirIntact(key) {
		return false
	}

	if recorded == "" || recorded == backend {
		return true
	}
	// Names differ textually — they may still be alias and concrete for the
	// same on-disk backend, which is a legitimate reuse.
	_, match := s.backendIdentity(backend)[recorded]
	return match
}

// backendDirIntact reports whether the process under `key` is still running
// out of the installed backend directory.
//
// gallery.InstallBackend replaces a backend by renaming the live directory to
// `<name>.install-backup`, moving the staged directory into place, then
// deleting the backup. A working directory follows the inode across a rename,
// so any process that outlived that swap now has a deleted inode as its CWD
// and every getcwd(2) in it fails with ENOENT. Python backends import torch
// lazily inside LoadModel, so the survivor still answers HealthCheck and only
// detonates when a model is loaded through it — surfacing as a bare
// `[Errno 2] No such file or directory` from deep inside torch's custom-op
// registration, with nothing pointing at the reinstall that caused it.
//
// The install paths already stop processes by name before replacing the
// directory. This is the backstop for when that bookkeeping misses one — a
// legacy entry with no recorded name, backendIdentity degraded to name-only
// matching because ListSystemBackends failed, or an earlier reinstall having
// rewritten the metadata.json that carries the alias. Comparing the recorded
// directory identity with what is on disk needs none of that bookkeeping to
// have been correct.
//
// A process with no recorded directory predates this field and is accepted, so
// a rollout does not restart every running backend once.
func (s *backendSupervisor) backendDirIntact(key string) bool {
	s.mu.Lock()
	bp, ok := s.processes[key]
	if !ok {
		s.mu.Unlock()
		return false
	}
	dir, recorded := bp.backendDir, bp.backendDirID
	s.mu.Unlock()

	if dir == "" || recorded == nil {
		return true
	}

	current, err := os.Stat(dir)
	if err != nil {
		xlog.Warn("Backend process is running from a directory that no longer exists; it must not be reused",
			"processKey", key, "dir", dir, "error", err)
		return false
	}
	// Same path, different inode: the directory was replaced underneath the
	// process, which is exactly what a reinstall does.
	if !os.SameFile(recorded, current) {
		xlog.Warn("Backend directory was replaced under a running process (reinstall); it must not be reused",
			"processKey", key, "dir", dir)
		return false
	}
	return true
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
	s.releasePortForKey(key, bp.port)
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
