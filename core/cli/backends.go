package cli

import (
	"context"
	"encoding/json"
	"fmt"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
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

type BackendsUpgrade struct {
	BackendArgs []string `arg:"" optional:"" name:"backends" help:"Backend names to upgrade (empty = upgrade all)"`

	BackendsCMDFlags `embed:""`
}

type BackendsCMD struct {
	List      BackendsList      `cmd:"" help:"List the backends available in your galleries" default:"withargs"`
	Install   BackendsInstall   `cmd:"" help:"Install a backend from the gallery"`
	Uninstall BackendsUninstall `cmd:"" help:"Uninstall a backend"`
	Upgrade   BackendsUpgrade   `cmd:"" help:"Upgrade backends to latest versions"`
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

	// Check for upgrades
	upgrades, _ := gallery.CheckBackendUpgrades(context.Background(), galleries, systemState)

	for _, backend := range backends {
		versionStr := ""
		if backend.Version != "" {
			versionStr = " v" + backend.Version
		}
		if backend.Installed {
			if info, ok := upgrades[backend.Name]; ok {
				upgradeStr := info.AvailableVersion
				if upgradeStr == "" {
					upgradeStr = "new build"
				}
				fmt.Printf(" * %s@%s%s (installed, upgrade available: %s)\n", backend.Gallery.Name, backend.Name, versionStr, upgradeStr)
			} else {
				fmt.Printf(" * %s@%s%s (installed)\n", backend.Gallery.Name, backend.Name, versionStr)
			}
		} else {
			fmt.Printf(" - %s@%s%s\n", backend.Gallery.Name, backend.Name, versionStr)
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
	err = galleryop.InstallExternalBackend(context.Background(), galleries, systemState, modelLoader, progressCallback, bi.BackendArgs, bi.Name, bi.Alias)
	if err != nil {
		return err
	}

	return nil
}

func (bu *BackendsUpgrade) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(bu.BackendGalleries), &galleries); err != nil {
		xlog.Error("unable to load galleries", "error", err)
	}

	systemState, err := system.GetSystemState(
		system.WithBackendSystemPath(bu.BackendsSystemPath),
		system.WithBackendPath(bu.BackendsPath),
	)
	if err != nil {
		return err
	}

	upgrades, err := gallery.CheckBackendUpgrades(context.Background(), galleries, systemState)
	if err != nil {
		return fmt.Errorf("failed to check for upgrades: %w", err)
	}

	if len(upgrades) == 0 {
		fmt.Println("All backends are up to date.")
		return nil
	}

	// Filter to specified backends if args given
	toUpgrade := upgrades
	if len(bu.BackendArgs) > 0 {
		toUpgrade = make(map[string]gallery.UpgradeInfo)
		for _, name := range bu.BackendArgs {
			if info, ok := upgrades[name]; ok {
				toUpgrade[name] = info
			} else {
				fmt.Printf("Backend %s: no upgrade available\n", name)
			}
		}
	}

	if len(toUpgrade) == 0 {
		fmt.Println("No upgrades to apply.")
		return nil
	}

	modelLoader := model.NewModelLoader(systemState)
	for name, info := range toUpgrade {
		versionStr := ""
		if info.AvailableVersion != "" {
			versionStr = " to v" + info.AvailableVersion
		}
		fmt.Printf("Upgrading %s%s...\n", name, versionStr)

		progressBar := progressbar.NewOptions(
			1000,
			progressbar.OptionSetDescription(fmt.Sprintf("downloading %s", name)),
			progressbar.OptionShowBytes(false),
			progressbar.OptionClearOnFinish(),
		)
		progressCallback := func(fileName string, current string, total string, percentage float64) {
			v := int(percentage * 10)
			if err := progressBar.Set(v); err != nil {
				xlog.Error("error updating progress bar", "error", err)
			}
		}

		if err := gallery.UpgradeBackend(context.Background(), systemState, modelLoader, galleries, name, progressCallback); err != nil {
			fmt.Printf("Failed to upgrade %s: %v\n", name, err)
		} else {
			fmt.Printf("Backend %s upgraded successfully\n", name)
		}
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
