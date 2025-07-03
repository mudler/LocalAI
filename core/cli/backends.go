package cli

import (
	"encoding/json"
	"fmt"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/startup"
	"github.com/rs/zerolog/log"
	"github.com/schollz/progressbar/v3"
)

type BackendsCMDFlags struct {
	BackendGalleries string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	BackendsPath     string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"storage"`
}

type BackendsList struct {
	BackendsCMDFlags `embed:""`
}

type BackendsInstallSingle struct {
	InstallArgs []string `arg:"" optional:"" name:"backend" help:"Backend images to install"`

	BackendsCMDFlags `embed:""`
}

type BackendsInstall struct {
	BackendArgs []string `arg:"" optional:"" name:"backends" help:"Backend configuration URLs to load"`

	BackendsCMDFlags `embed:""`
}

type BackendsUninstall struct {
	BackendArgs []string `arg:"" name:"backends" help:"Backend names to uninstall"`

	BackendsCMDFlags `embed:""`
}

type BackendsCMD struct {
	List          BackendsList          `cmd:"" help:"List the backends available in your galleries" default:"withargs"`
	Install       BackendsInstall       `cmd:"" help:"Install a backend from the gallery"`
	InstallSingle BackendsInstallSingle `cmd:"" help:"Install a single backend from the gallery"`
	Uninstall     BackendsUninstall     `cmd:"" help:"Uninstall a backend"`
}

func (bi *BackendsInstallSingle) Run(ctx *cliContext.Context) error {
	for _, backend := range bi.InstallArgs {
		progressBar := progressbar.NewOptions(
			1000,
			progressbar.OptionSetDescription(fmt.Sprintf("downloading backend %s", backend)),
			progressbar.OptionShowBytes(false),
			progressbar.OptionClearOnFinish(),
		)
		progressCallback := func(fileName string, current string, total string, percentage float64) {
			v := int(percentage * 10)
			err := progressBar.Set(v)
			if err != nil {
				log.Error().Err(err).Str("filename", fileName).Int("value", v).Msg("error while updating progress bar")
			}
		}

		if err := gallery.InstallBackend(bi.BackendsPath, &gallery.GalleryBackend{
			URI: backend,
		}, progressCallback); err != nil {
			return err
		}
	}

	return nil
}

func (bl *BackendsList) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(bl.BackendGalleries), &galleries); err != nil {
		log.Error().Err(err).Msg("unable to load galleries")
	}

	backends, err := gallery.AvailableBackends(galleries, bl.BackendsPath)
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
		log.Error().Err(err).Msg("unable to load galleries")
	}

	for _, backendName := range bi.BackendArgs {

		progressBar := progressbar.NewOptions(
			1000,
			progressbar.OptionSetDescription(fmt.Sprintf("downloading backend %s", backendName)),
			progressbar.OptionShowBytes(false),
			progressbar.OptionClearOnFinish(),
		)
		progressCallback := func(fileName string, current string, total string, percentage float64) {
			v := int(percentage * 10)
			err := progressBar.Set(v)
			if err != nil {
				log.Error().Err(err).Str("filename", fileName).Int("value", v).Msg("error while updating progress bar")
			}
		}

		backendURI := downloader.URI(backendName)

		if !backendURI.LooksLikeOCI() {
			backends, err := gallery.AvailableBackends(galleries, bi.BackendsPath)
			if err != nil {
				return err
			}

			backend := gallery.FindGalleryElement(backends, backendName, bi.BackendsPath)
			if backend == nil {
				log.Error().Str("backend", backendName).Msg("backend not found")
				return fmt.Errorf("backend not found: %s", backendName)
			}

			log.Info().Str("backend", backendName).Str("license", backend.License).Msg("installing backend")
		}

		err := startup.InstallExternalBackends(galleries, bi.BackendsPath, progressCallback, backendName)
		if err != nil {
			return err
		}
	}
	return nil
}

func (bu *BackendsUninstall) Run(ctx *cliContext.Context) error {
	for _, backendName := range bu.BackendArgs {
		log.Info().Str("backend", backendName).Msg("uninstalling backend")

		err := gallery.DeleteBackendFromSystem(bu.BackendsPath, backendName)
		if err != nil {
			return err
		}

		fmt.Printf("Backend %s uninstalled successfully\n", backendName)
	}
	return nil
}
