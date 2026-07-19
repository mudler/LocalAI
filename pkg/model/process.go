package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hpcloud/tail"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	"github.com/mudler/LocalAI/pkg/signals"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
)

var forceBackendShutdown bool = os.Getenv("LOCALAI_FORCE_BACKEND_SHUTDOWN") == "true"

var (
	// ErrModelNotFound reports that a model is not loaded. Exported so HTTP
	// handlers can map it to 404 instead of a blanket 500.
	ErrModelNotFound = errors.New("model not found")
	// ErrModelBusy indicates that a graceful shutdown context ended while
	// requests were still in flight.
	ErrModelBusy = errors.New("model is still busy")

	// ErrRemoteModelNotLoaded is returned by a RemoteModelUnloader when no
	// node in the cluster has the model loaded. It exists so the local store
	// miss and the cluster-wide miss stay distinguishable: only when BOTH are
	// empty may we tell the operator the model is not loaded.
	ErrRemoteModelNotLoaded = errors.New("model not loaded on any node")
)

const (
	gracefulShutdownTimeout = 30 * time.Second
	forcedShutdownTimeout   = 30 * time.Second
	backendFreeTimeout      = 5 * time.Second
	busyPollInterval        = 100 * time.Millisecond
)

// unloadRemote asks the remote unloader to stop `s` on whichever node holds
// it, preferring the context-aware extension so a forced shutdown and the
// caller's deadline both survive the distributed boundary.
func unloadRemote(ctx context.Context, u RemoteModelUnloader, s string, force bool) error {
	if contextUnloader, ok := u.(RemoteModelContextUnloader); ok {
		return contextUnloader.UnloadRemoteModelContext(ctx, s, force)
	}
	return u.UnloadRemoteModel(s)
}

// deleteProcess stops and removes a backend. The force flag trades a graceful
// shutdown for a prompt one and is meant for the watchdog's busy-killer: a
// backend that has been busy past the watchdog timeout may be stuck in an
// in-flight gRPC call. The graceful path waits only until ctx expires; the
// force path skips that wait and Free(), stops the process, then cleans up.
// Callers serialize this operation per model, never with the global loader
// mutex, so a faulty backend cannot stall unrelated model lifecycle work.
func (ml *ModelLoader) deleteProcess(ctx context.Context, s string, force bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Snapshot mutable loader configuration while holding ml.mu, then perform
	// every wait, callback, RPC, and process operation without the global lock.
	// Same-model ordering is provided by modelOperationLocks at the public
	// lifecycle boundary.
	ml.mu.Lock()
	store := ml.store
	wd := ml.wd
	hooks := append([]ModelUnloadHook(nil), ml.onUnloadHooks...)
	remoteUnloader := ml.remoteUnloader
	ml.mu.Unlock()

	model, ok := store.Get(s)
	if !ok {
		// A local-store miss is not proof the model is unloaded. In
		// distributed mode the model runs on a worker and the authoritative
		// record is the shared node registry: any frontend replica that did
		// not itself serve the model (load balancer picked a peer, or this
		// replica restarted) has no local entry. Returning "model not found"
		// here reported a model that was demonstrably running as absent, and
		// left its backend process untouched.
		if remoteUnloader != nil {
			xlog.Debug("Model not in local store; asking the remote unloader", "model", s)
			if err := unloadRemote(ctx, remoteUnloader, s, force); err != nil {
				if errors.Is(err, ErrRemoteModelNotLoaded) {
					// Absent locally AND cluster-wide: genuinely not loaded.
					return ErrModelNotFound
				}
				return err
			}
			return nil
		}
		xlog.Debug("Model not found", "model", s)
		return ErrModelNotFound
	}

	if !force {
		client := model.GRPC(false, wd)
		for client.IsBusy() {
			xlog.Debug("Model busy. Waiting.", "model", s)
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: %s: %w", ErrModelBusy, s, ctx.Err())
			case <-time.After(busyPollInterval):
			}
		}
	}

	xlog.Debug("Deleting process", "model", s, "force", force)

	// Run unload hooks (e.g. close MCP sessions)
	for _, hook := range hooks {
		hook(s)
	}

	// Free GPU resources before stopping the process to ensure VRAM is
	// released. Skipped on force-shutdown: a stuck-busy backend won't answer
	// a Free RPC (it's hung on the same stuck call), and stopping the
	// process releases its VRAM anyway. Free is optional: backends that
	// don't override it (the generated stub, many Python/external backends,
	// or a federation proxy in distributed mode) return gRPC Unimplemented.
	// That is expected, not a failure — VRAM is reclaimed when the process
	// is stopped below, or by the remote unloader for remote backends — so
	// don't surface it as an error.
	if !force {
		xlog.Debug("Calling Free() to release GPU resources", "model", s)
		freeCtx, cancel := context.WithTimeout(ctx, backendFreeTimeout)
		err := model.GRPC(false, wd).Free(freeCtx)
		cancel()
		if err != nil {
			if grpcerrors.IsUnimplemented(err) {
				xlog.Debug("Backend does not implement Free(); GPU release handled on process stop", "model", s)
			} else {
				// Now that the expected Unimplemented case is filtered out above, a
				// remaining error is a genuine failure to release VRAM — surface it.
				xlog.Error("Error freeing GPU resources", "error", err, "model", s)
			}
		}
	}

	process := model.Process()
	if process == nil {
		// No local process — this is a remote/external backend.
		// In distributed mode, delegate to the remote unloader to tell
		// the backend node to free the model (GPU resources, etc.).
		var unloadErr error
		if remoteUnloader != nil {
			xlog.Debug("Delegating model unload to remote unloader", "model", s)
			unloadErr = unloadRemote(ctx, remoteUnloader, s, force)
			if unloadErr != nil {
				xlog.Warn("Remote model unload failed", "model", s, "error", unloadErr)
			}
		} else {
			xlog.Debug("No local process and no remote unloader", "model", s)
		}
		// The store is only the frontend's local representative of a remote
		// model. Never retain it after an unload attempt: on failure it may point
		// at a known-unreachable worker, while the distributed registry remains
		// the source of truth for anything that is still running remotely.
		store.Delete(s)
		return unloadErr
	}

	// Mark the stop as intentional so the exit-watcher logs it as an
	// expected stop, not a crash (signal-terminated children report -1).
	ml.stoppingProcs.Store(process, struct{}{})
	err := process.Stop()
	if err != nil {
		xlog.Error("(deleteProcess) error while deleting process", "error", err, "model", s)
		if !process.IsAlive() {
			// A concurrently crashed/already-reaped process can no longer own
			// resources even if Stop could not read or signal its PID.
			store.Delete(s)
			return nil
		}
		return err
	}

	store.Delete(s)
	return nil
}
func (ml *ModelLoader) StopGRPC(filter GRPCProcessFilter) error {
	var err error = nil
	ml.mu.Lock()
	store := ml.store
	ml.mu.Unlock()

	// Collect matching keys first — can't mutate store during Range
	var toDelete []string
	store.Range(func(k string, m *Model) bool {
		if filter(k, m.Process()) {
			toDelete = append(toDelete, k)
		}
		return true
	})
	for _, k := range toDelete {
		e := ml.ShutdownModel(k)
		err = errors.Join(err, e)
	}
	return err
}

func (ml *ModelLoader) StopAllGRPC() error {
	return ml.StopGRPC(all)
}

func (ml *ModelLoader) GetGRPCPID(id string) (int, error) {
	ml.mu.Lock()
	store := ml.store
	ml.mu.Unlock()
	p, exists := store.Get(id)
	if !exists {
		return -1, fmt.Errorf("no grpc backend found for %s", id)
	}
	if p.Process() == nil {
		return -1, fmt.Errorf("no grpc backend found for %s", id)
	}
	return strconv.Atoi(p.Process().PID)
}

// StartProcess starts a gRPC backend process and returns its process handle.
// This is the public wrapper for the internal startProcess method, used by
// the serve-backend CLI subcommand to start a backend on a specified address.
func (ml *ModelLoader) StartProcess(grpcProcess, id string, serverAddress string, args ...string) (*process.Process, error) {
	return ml.startProcess(grpcProcess, id, serverAddress, args...)
}

func (ml *ModelLoader) startProcess(grpcProcess, id string, serverAddress string, args ...string) (*process.Process, error) {
	// Make sure the process is executable
	// Check first if it has executable permissions
	if fi, err := os.Stat(grpcProcess); err == nil {
		if fi.Mode()&0111 == 0 {
			xlog.Debug("Process is not executable. Making it executable.", "process", grpcProcess)
			if err := os.Chmod(grpcProcess, 0700); err != nil {
				return nil, err
			}
		}
	}

	xlog.Debug("Loading GRPC Process", "process", grpcProcess)

	xlog.Debug("GRPC Service will be running", "id", id, "address", serverAddress)

	workDir, err := filepath.Abs(filepath.Dir(grpcProcess))
	if err != nil {
		return nil, err
	}

	env := os.Environ()
	// Vulkan backends are self-contained: they bundle their own loader and
	// Mesa driver .so files in lib/ plus the matching ICD manifests in
	// vulkan/icd.d/. Point the loader at those manifests so it doesn't rely on
	// the runtime base image shipping a Vulkan driver (it carries the
	// SYCL/Level-Zero stack instead, so the default ICD search path is empty
	// and the GPU would silently fall back to CPU). No-op for other backends.
	env = append(env, vulkanICDEnv(workDir)...)

	grpcControlProcess := process.New(
		process.WithTemporaryStateDir(),
		process.WithName(filepath.Base(grpcProcess)),
		process.WithArgs(append(args, []string{"--addr", serverAddress}...)...),
		process.WithEnvironment(env...),
		process.WithWorkDir(workDir),
	)

	if ml.wd != nil {
		ml.wd.Add(serverAddress, grpcControlProcess)
		ml.wd.AddAddressModelMap(serverAddress, id)
	}

	if err := grpcControlProcess.Run(); err != nil {
		return grpcControlProcess, err
	}

	xlog.Debug("GRPC Service state dir", "dir", grpcControlProcess.StateDir())

	signals.RegisterGracefulTerminationHandler(func() {
		// StopAllGRPC (the deleteProcess path) is registered earlier and runs
		// first for store-tracked backends, stopping this process and removing
		// its pidfile. Calling Stop again then fails with "failed to read PID".
		// Skip when it's already gone; this handler still covers processes that
		// StopAllGRPC doesn't track (e.g. worker-supervised backends).
		if !grpcControlProcess.IsAlive() {
			return
		}
		ml.stoppingProcs.Store(grpcControlProcess, struct{}{})
		if err := grpcControlProcess.Stop(); err != nil {
			xlog.Error("error while shutting down grpc process", "error", err)
		}
	})

	go func() {
		t, err := tail.TailFile(grpcControlProcess.StderrPath(), tail.Config{Follow: true})
		if err != nil {
			xlog.Error("Could not tail stderr", "process", grpcProcess)
			return
		}
		for line := range t.Lines {
			xlog.Debug("GRPC stderr", "id", strings.Join([]string{id, serverAddress}, "-"), "line", line.Text)
			if ml.backendLogs != nil && ml.backendLoggingEnabled.Load() {
				ml.backendLogs.AppendLine(id, "stderr", line.Text)
			}
		}
	}()
	go func() {
		t, err := tail.TailFile(grpcControlProcess.StdoutPath(), tail.Config{Follow: true})
		if err != nil {
			xlog.Error("Could not tail stdout", "process", grpcProcess)
			return
		}
		for line := range t.Lines {
			xlog.Debug("GRPC stdout", "id", strings.Join([]string{id, serverAddress}, "-"), "line", line.Text)
			if ml.backendLogs != nil && ml.backendLoggingEnabled.Load() {
				ml.backendLogs.AppendLine(id, "stdout", line.Text)
			}
		}
	}()

	// Surface backend exits in the log. Without this, a crash (SIGSEGV
	// from a missing shared library, a Python ImportError, etc.) is
	// invisible at every log level — the only signal is a delayed
	// "connection refused" from the gRPC dial, which doesn't say
	// whether the child is alive.
	go func() {
		<-grpcControlProcess.Done()
		// LoadAndDelete both reads the intentional-stop marker and frees the
		// map entry so it doesn't accumulate across the process's lifetime.
		_, intentional := ml.stoppingProcs.LoadAndDelete(grpcControlProcess)
		fields := []any{
			"id", id,
			"address", serverAddress,
			"process", filepath.Base(grpcProcess),
		}
		// Report the raw exit code without interpreting it: a child killed by
		// our own SIGTERM/SIGKILL surfaces as -1 (Go reports -1 for signal
		// termination, not the shell's 128+signal convention), so the code
		// alone can't tell an intended stop from a crash. The stoppingProcs
		// marker is the reliable signal for that, so it picks the log level.
		if code, codeErr := grpcControlProcess.ExitCode(); codeErr == nil {
			fields = append(fields, "exitCode", code)
		}
		if intentional {
			xlog.Info("Backend process stopped", fields...)
		} else {
			// A stop we didn't initiate — a SIGSEGV from a missing shared
			// library, a Python ImportError, an OOM kill, an unexpected self-exit.
			xlog.Warn("Backend process exited unexpectedly", fields...)
		}
	}()

	return grpcControlProcess, nil
}

// vulkanICDEnv returns environment overrides that point the Vulkan loader at
// the ICD manifests a backend bundles in <workDir>/vulkan/icd.d. Vulkan
// backends ship a self-contained stack — their own loader and Mesa driver .so
// files in lib/ (resolved via the LD_LIBRARY_PATH that run.sh sets) plus the
// matching ICD manifests — so the loader must be told where those manifests
// live; its default search path (/usr/share/vulkan/icd.d, /etc/vulkan/icd.d)
// is empty on the runtime base image. Returns nil when the directory holds no
// manifests (CPU/CUDA/SYCL builds), leaving the host's Vulkan setup untouched.
func vulkanICDEnv(workDir string) []string {
	icdDir := filepath.Join(workDir, "vulkan", "icd.d")
	entries, err := os.ReadDir(icdDir)
	if err != nil {
		return nil
	}

	manifests := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		manifests = append(manifests, filepath.Join(icdDir, e.Name()))
	}
	if len(manifests) == 0 {
		return nil
	}

	list := strings.Join(manifests, string(os.PathListSeparator))
	// VK_DRIVER_FILES is the current loader variable; VK_ICD_FILENAMES is its
	// deprecated alias, set too so older bundled loaders still pick it up.
	return []string{
		"VK_DRIVER_FILES=" + list,
		"VK_ICD_FILENAMES=" + list,
	}
}
