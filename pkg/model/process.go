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
	modelNotFoundErr = errors.New("model not found")
)

func (ml *ModelLoader) deleteProcess(s string) error {
	model, ok := ml.store.Get(s)
	if !ok {
		xlog.Debug("Model not found", "model", s)
		return modelNotFoundErr
	}

	retries := 1
	for model.GRPC(false, ml.wd).IsBusy() {
		xlog.Debug("Model busy. Waiting.", "model", s)
		dur := time.Duration(retries*2) * time.Second
		if dur > retryTimeout {
			dur = retryTimeout
		}
		time.Sleep(dur)
		retries++

		if retries > 10 && forceBackendShutdown {
			xlog.Warn("Model is still busy after retries. Forcing shutdown.", "model", s, "retries", retries)
			break
		}
	}

	xlog.Debug("Deleting process", "model", s)

	// Run unload hooks (e.g. close MCP sessions)
	for _, hook := range ml.onUnloadHooks {
		hook(s)
	}

	// Free GPU resources before stopping the process to ensure VRAM is released.
	// Free is optional: backends that don't override it (the generated stub, many
	// Python/external backends, or a federation proxy in distributed mode) return
	// gRPC Unimplemented. That is expected, not a failure — VRAM is reclaimed when
	// the process is stopped below, or by the remote unloader for remote backends —
	// so don't surface it as an error.
	xlog.Debug("Calling Free() to release GPU resources", "model", s)
	if err := model.GRPC(false, ml.wd).Free(context.Background()); err != nil {
		if grpcerrors.IsUnimplemented(err) {
			xlog.Debug("Backend does not implement Free(); GPU release handled on process stop", "model", s)
		} else {
			// Now that the expected Unimplemented case is filtered out above, a
			// remaining error is a genuine failure to release VRAM — surface it.
			xlog.Error("Error freeing GPU resources", "error", err, "model", s)
		}
	}

	process := model.Process()
	if process == nil {
		// No local process — this is a remote/external backend.
		// In distributed mode, delegate to the remote unloader to tell
		// the backend node to free the model (GPU resources, etc.).
		if ml.remoteUnloader != nil {
			xlog.Debug("Delegating model unload to remote unloader", "model", s)
			if err := ml.remoteUnloader.UnloadRemoteModel(s); err != nil {
				xlog.Warn("Remote model unload failed", "model", s, "error", err)
			}
		} else {
			xlog.Debug("No local process and no remote unloader", "model", s)
		}
		ml.store.Delete(s)
		return nil
	}

	// Mark the stop as intentional so the exit-watcher logs it as an
	// expected stop, not a crash (signal-terminated children report -1).
	ml.stoppingProcs.Store(process, struct{}{})
	err := process.Stop()
	if err != nil {
		xlog.Error("(deleteProcess) error while deleting process", "error", err, "model", s)
	}

	if err == nil {
		ml.store.Delete(s)
	}

	return err
}
func (ml *ModelLoader) StopGRPC(filter GRPCProcessFilter) error {
	var err error = nil
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Collect matching keys first — can't mutate store during Range
	var toDelete []string
	ml.store.Range(func(k string, m *Model) bool {
		if filter(k, m.Process()) {
			toDelete = append(toDelete, k)
		}
		return true
	})
	for _, k := range toDelete {
		e := ml.deleteProcess(k)
		err = errors.Join(err, e)
	}
	return err
}

func (ml *ModelLoader) StopAllGRPC() error {
	return ml.StopGRPC(all)
}

func (ml *ModelLoader) GetGRPCPID(id string) (int, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	p, exists := ml.store.Get(id)
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
