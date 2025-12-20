package cli

import (
	"context"
	"encoding/json"
	"fmt"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	"github.com/mudler/xlog"
	"github.com/schollz/progressbar/v3"
)

type BackendsCMDFlags struct {
	BackendGalleries   string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	BackendsPath       string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"storage"`
	BackendsSystemPath string `env:"LOCALAI_BACKENDS_SYSTEM_PATH,BACKEND_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends used for inferencing" group:"backends"`
}

type BackendsList struct {
	BackendsCMDFlags `embed:""`
}

type BackendsInstall struct {
	BackendArgs string `arg:"" optional:"" name:"backend" help:"Backend configuration URL to load"`
	Name        string `arg:"" optional:"" name:"name" help:"Name of the backend"`
	Alias       string `arg:"" optional:"" name:"alias" help:"Alias of the backend"`

	BackendsCMDFlags `embed:""`
}

type BackendsUninstall struct {
	BackendArgs []string `arg:"" name:"backends" help:"Backend names to uninstall"`

	BackendsCMDFlags `embed:""`
}

type BackendsCMD struct {
	List      BackendsList      `cmd:"" help:"List the backends available in your galleries" default:"withargs"`
	Install   BackendsInstall   `cmd:"" help:"Install a backend from the gallery"`
	Uninstall BackendsUninstall `cmd:"" help:"Uninstall a backend"`
}

func (bl *BackendsList) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(bl.BackendGalleries), &galleries); err != nil {
		xlog.Error("unable to load galleries", "error", err)
	}

	systemState, err := system.GetSystemState(
		system.WithBackendSystemPath(bl.BackendsSystemPath),
		system.WithBackendPath(bl.BackendsPath),
	)
	if err != nil {
		return err
	}

	backends, err := gallery.AvailableBackends(galleries, systemState)
	if err != nil {
		return err
	}
	for _, backend := range backends {
		if backend.Installed {
			fmt.Printf(" * %s@%s (installed)\n", backend.Gallery.Name, backend.Name)
		} else {
			fmt.Printf(" - %s@%s\n", backend.Gallery.Name, backend.Name)
		}
	}
	return nil
}

func (bi *BackendsInstall) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(bi.BackendGalleries), &galleries); err != nil {
		xlog.Error("unable to load galleries", "error", err)
	}

	systemState, err := system.GetSystemState(
		system.WithBackendSystemPath(bi.BackendsSystemPath),
		system.WithBackendPath(bi.BackendsPath),
	)
	if err != nil {
		return err
	}

	progressBar := progressbar.NewOptions(
		1000,
		progressbar.OptionSetDescription(fmt.Sprintf("downloading backend %s", bi.BackendArgs)),
		progressbar.OptionShowBytes(false),
		progressbar.OptionClearOnFinish(),
	)
	progressCallback := func(fileName string, current string, total string, percentage float64) {
		v := int(percentage * 10)
		err := progressBar.Set(v)
		if err != nil {
			xlog.Error("error while updating progress bar", "error", err, "filename", fileName, "value", v)
		}
	}

	modelLoader := model.NewModelLoader(systemState)
	err = services.InstallExternalBackend(context.Background(), galleries, systemState, modelLoader, progressCallback, bi.BackendArgs, bi.Name, bi.Alias)
	if err != nil {
		return err
	}

	return nil
}

func (bu *BackendsUninstall) Run(ctx *cliContext.Context) error {
	for _, backendName := range bu.BackendArgs {
		xlog.Info("uninstalling backend", "backend", backendName)

		systemState, err := system.GetSystemState(
			system.WithBackendSystemPath(bu.BackendsSystemPath),
			system.WithBackendPath(bu.BackendsPath),
		)
		if err != nil {
			return err
		}

		err = gallery.DeleteBackendFromSystem(systemState, backendName)
		if err != nil {
			return err
		}

		fmt.Printf("Backend %s uninstalled successfully\n", backendName)
	}
	return nil
}
