package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

type DS4Distributed struct {
	WorkerFlags  `embed:""`
	ExtraDS4Args string `name:"ds4-args" env:"LOCALAI_EXTRA_DS4_ARGS,EXTRA_DS4_ARGS" help:"Arguments passed to ds4-worker (e.g. '--role worker --model m.gguf --layers 20:output --coordinator HOST PORT')"`
}

const (
	ds4WorkerBinaryName = "ds4-worker"
	ds4GalleryName      = "ds4"
)

// ds4WorkerArgs builds the argv for syscall.Exec when launching ds4-worker
// directly: the binary path followed by the space-split extra args. An empty
// extra string yields a bare invocation.
func ds4WorkerArgs(binary, extra string) []string {
	args := []string{binary}
	args = append(args, strings.Fields(extra)...)
	return args
}

func findDS4Backend(galleries string, systemState *system.SystemState, requireIntegrity bool) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		xlog.Warn("Failed listing system backends", "error", err)
		return "", err
	}

	backend, ok := backends.Get(ds4GalleryName)
	if !ok {
		ml := model.NewModelLoader(systemState)
		var gals []config.Gallery
		if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
			xlog.Error("failed loading galleries", "error", err)
			return "", err
		}
		if err := gallery.InstallBackendFromGallery(context.Background(), gals, systemState, ml, ds4GalleryName, nil, true, requireIntegrity); err != nil {
			xlog.Error("ds4 backend not found, failed to install it", "error", err)
			return "", err
		}
		backends, err = gallery.ListSystemBackends(systemState)
		if err != nil {
			return "", err
		}
		backend, ok = backends.Get(ds4GalleryName)
		if !ok {
			return "", errors.New("ds4 backend not found after install")
		}
	}

	backendPath := filepath.Dir(backend.RunFile)
	if backendPath == "" {
		return "", errors.New("ds4 backend not found, install it first")
	}
	return filepath.Join(backendPath, ds4WorkerBinaryName), nil
}

func (r *DS4Distributed) Run(ctx *cliContext.Context) error {
	if r.ExtraDS4Args == "" && len(os.Args) < 4 {
		return fmt.Errorf("usage: local-ai worker ds4-distributed -- --role worker --model <gguf> --layers <START:END|START:output> --coordinator <host> <port>")
	}

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}

	worker, err := findDS4Backend(r.BackendGalleries, systemState, r.RequireBackendIntegrity)
	if err != nil {
		return err
	}

	// ds4 bundles its own dynamic loader (lib/ld.so) for glibc compatibility,
	// like backend/cpp/ds4/run.sh does for grpc-server. Launch ds4-worker via
	// that loader when present; otherwise exec it directly. (This is a
	// deliberate divergence from worker_llamacpp.go, which has no bundled loader.)
	backendPath := filepath.Dir(worker)
	env := os.Environ()
	loader := filepath.Join(backendPath, "lib", "ld.so")
	if _, statErr := os.Stat(loader); statErr == nil {
		env = append(env, "LD_LIBRARY_PATH="+filepath.Join(backendPath, "lib")+":"+os.Getenv("LD_LIBRARY_PATH"))
		args := append([]string{loader}, ds4WorkerArgs(worker, r.ExtraDS4Args)...)
		return syscall.Exec(loader, args, env)
	}

	return syscall.Exec(worker, ds4WorkerArgs(worker, r.ExtraDS4Args), env)
}
