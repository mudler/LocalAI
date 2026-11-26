package gallery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// StartCheck starts the backend whose entrypoint is runFile, waits until it
// answers, and stops it again. It returns nil when the build starts. A
// non-nil error means the build must not stay in place.
//
// The check runs from the final path of the new build, after the swap, so it
// tests what a model load will run. It never loads a model: a check that
// passes proves the process starts and listens, not that a model loads on a
// GPU.
type StartCheck func(ctx context.Context, runFile string) error

// LoaderStartCheck is the StartCheck of a host that runs backends itself: it
// starts the build with the loader's own process start on a free local port
// and probes its gRPC health check (model.ModelLoader.CheckBackendStart). A
// nil loader gives a check that always fails, so an upgrade never skips the
// check by accident.
func LoaderStartCheck(ml *model.ModelLoader, backendName string) StartCheck {
	return func(ctx context.Context, runFile string) error {
		if ml == nil {
			return errors.New("no model loader to run the start check")
		}
		return ml.CheckBackendStart(ctx, "upgrade-check:"+backendName, runFile, startCheckTimeout)
	}
}

// startCheckTimeout is the readiness window of LoaderStartCheck. A var so a
// spec can shorten it.
var startCheckTimeout = model.DefaultStartCheckTimeout

// BackendInstallOption changes how InstallBackend and
// InstallBackendFromGallery replace a backend.
type BackendInstallOption func(*backendInstallOptions)

type backendInstallOptions struct {
	startCheck StartCheck
}

// WithStartCheck makes an install that replaces an installed build keep the
// replaced build until check passes on the new one, and put it back when the
// check fails. A fresh install (nothing to replace) runs no check.
func WithStartCheck(check StartCheck) BackendInstallOption {
	return func(o *backendInstallOptions) { o.startCheck = check }
}

func applyBackendInstallOptions(opts []BackendInstallOption) backendInstallOptions {
	var o backendInstallOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// failedSuffix names the directory a build that failed after the swap is
// moved to while the previous build is put back. It is removed once the
// restore succeeded and kept, for diagnosis, when the restore failed.
const failedSuffix = ".failed"

// restoreBackup puts the previous build, at backupPath, back at backendPath.
// The new build is moved aside to backendPath+".failed" first, so a restore
// never deletes the only copy of either build before the other is in place.
func restoreBackup(backendPath, backupPath string) error {
	// Nothing is moved or removed unless both paths are a backend directory
	// and its swap directory. rename and RemoveAll act on a symbolic link
	// itself, never on what it points to.
	if err := checkSwapPaths(backendPath, backupPath); err != nil {
		return err
	}
	failedPath := backendPath + failedSuffix
	_ = os.RemoveAll(failedPath)
	if err := renameBackend(backendPath, failedPath); err != nil {
		return fmt.Errorf("moving the failed build aside: %w", err)
	}
	if err := renameBackend(backupPath, backendPath); err != nil {
		// Put the failed build back so the backend is not left missing. It is
		// not runnable, but its directory is what the operator inspects, and
		// the previous build stays at backupPath.
		if backErr := renameBackend(failedPath, backendPath); backErr != nil {
			xlog.Error("Could not put the failed build back after a failed restore", "path", backendPath, "error", backErr)
		}
		return fmt.Errorf("restoring the previous build from %s: %w", filepath.Base(backupPath), err)
	}
	if err := os.RemoveAll(failedPath); err != nil {
		xlog.Warn("Could not remove the failed build after the restore", "path", failedPath, "error", err)
	}
	return nil
}

// rollbackSwap puts the previous build back after a step that followed the
// swap failed, registers the backends again and returns the error of the
// step (see rollbackError).
func rollbackSwap(ctx context.Context, systemState *system.SystemState, modelLoader *model.ModelLoader, code UpgradeFailureCode, cause error, backendPath, backupPath string) error {
	xlog.Error("Backend build failed after the swap, restoring the previous build", "path", backendPath, "error", cause)
	restoreErr := restoreBackup(backendPath, backupPath)
	if restoreErr != nil {
		xlog.Error("Could not restore the previous build", "path", backendPath, "error", restoreErr)
	}
	if err := registerBackends(systemState, modelLoader); err != nil && restoreErr == nil {
		restoreErr = fmt.Errorf("registering the restored build: %w", err)
	}
	return rollbackError(ctx, code, cause, restoreErr, backendPath)
}

// rollbackError is the error of a step that failed after the swap. It carries
// code, says whether the previous build is back, and keeps the cause. A
// restore that failed wins over a cancel: the error carries ErrUpgradeRestore
// with the cause still in the chain, so a caller that reads a cancel first
// does not report the operation as cancelled. A cancel whose restore
// succeeded keeps its own error (callers read it as a stop). The message
// names the backend directory by its base name only.
func rollbackError(ctx context.Context, code UpgradeFailureCode, cause, restoreErr error, backendPath string) error {
	if restoreErr != nil {
		restore := fmt.Errorf("%w (%s): %w", ErrUpgradeRestore, filepath.Base(backendPath), restoreErr)
		return &UpgradeError{Code: code, Err: errors.Join(cause, restore)}
	}
	if ctx.Err() != nil && errors.Is(cause, ctx.Err()) {
		return cause
	}
	return &UpgradeError{Code: code, RolledBack: true, Err: fmt.Errorf("%w; the previous build was restored", cause)}
}
