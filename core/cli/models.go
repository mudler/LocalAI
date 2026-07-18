package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"github.com/schollz/progressbar/v3"
)

type ModelsCMDFlags struct {
	Galleries        string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
	BackendGalleries string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	ModelsPath       string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	BackendsPath     string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"storage"`
	Color            string `env:"COLOR" hidden:""`
	NoColor          string `env:"NO_COLOR" hidden:""`
	HFToken          string `env:"HF_TOKEN" hidden:""`
}

type ModelsList struct {
	ModelsCMDFlags `embed:""`
}

type ModelsInstall struct {
	DisablePredownloadScan   bool     `env:"LOCALAI_DISABLE_PREDOWNLOAD_SCAN" help:"If true, disables the best-effort security scanner before downloading any files." group:"hardening" default:"false"`
	RequireBackendIntegrity  bool     `env:"LOCALAI_REQUIRE_BACKEND_INTEGRITY,REQUIRE_BACKEND_INTEGRITY" help:"If true, reject backend installs without a configured signature verification policy (OCI URIs) or SHA256 (tarball/HTTP URIs)." group:"hardening" default:"false"`
	AutoloadBackendGalleries bool     `env:"LOCALAI_AUTOLOAD_BACKEND_GALLERIES" help:"If true, automatically loads backend galleries" group:"backends" default:"true"`
	ModelArgs                []string `arg:"" optional:"" name:"models" help:"Model configuration URLs to load"`
	Variant                  string   `name:"variant" help:"Install a specific variant of a gallery entry that declares them, by the variant's model name. Leave unset to let LocalAI auto-select the largest build this machine can run." group:"models"`

	ModelsCMDFlags `embed:""`
}

type ModelsCMD struct {
	List    ModelsList    `cmd:"" help:"List the models available in your galleries" default:"withargs"`
	Install ModelsInstall `cmd:"" help:"Install a model from the gallery"`
}

func (ml *ModelsList) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(ml.Galleries), &galleries); err != nil {
		xlog.Error("unable to load galleries", "error", err)
	}

	systemState, err := system.GetSystemState(
		system.WithModelPath(ml.ModelsPath),
		system.WithBackendPath(ml.BackendsPath),
	)
	if err != nil {
		return err
	}
	models, err := gallery.AvailableGalleryModels(galleries, systemState)
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.Installed {
			fmt.Printf(" * %s@%s (installed)\n", model.Gallery.Name, model.Name)
		} else {
			fmt.Printf(" - %s@%s\n", model.Gallery.Name, model.Name)
		}
	}
	return nil
}

func (mi *ModelsInstall) Run(ctx *cliContext.Context) error {
	systemState, err := system.GetSystemState(
		system.WithModelPath(mi.ModelsPath),
		system.WithBackendPath(mi.BackendsPath),
	)
	if err != nil {
		return err
	}

	artifactMaterializer := modelartifacts.NewDefaultManager(
		modelartifacts.WithHuggingFaceToken(mi.HFToken),
	)
	galleryService := galleryop.NewGalleryService(&config.ApplicationConfig{
		SystemState:               systemState,
		ModelArtifactMaterializer: artifactMaterializer,
		ModelPreloadRenderMode:    mi.Color,
		DisableModelPreloadColor:  mi.NoColor != "",
	}, model.NewModelLoader(systemState))
	err = galleryService.Start(context.Background(), config.NewModelConfigLoader(mi.ModelsPath,
		config.WithArtifactMaterializer(artifactMaterializer),
		config.WithPreloadDisplay(mi.Color, mi.NoColor != "")), systemState)
	if err != nil {
		return err
	}

	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(mi.Galleries), &galleries); err != nil {
		xlog.Error("unable to load galleries", "error", err)
	}

	var backendGalleries []config.Gallery
	if err := json.Unmarshal([]byte(mi.BackendGalleries), &backendGalleries); err != nil {
		xlog.Error("unable to load backend galleries", "error", err)
	}

	for _, modelName := range mi.ModelArgs {

		progressBar := progressbar.NewOptions(
			1000,
			progressbar.OptionSetDescription(fmt.Sprintf("downloading model %s", modelName)),
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
		//startup.InstallModels()
		models, err := gallery.AvailableGalleryModels(galleries, systemState)
		if err != nil {
			return err
		}

		modelURI := downloader.URI(modelName)

		if !modelURI.LooksLikeOCI() {
			model := gallery.FindGalleryElement(models, modelName)
			if model == nil {
				xlog.Error("model not found", "model", modelName)
				return err
			}

			err = gallery.SafetyScanGalleryModel(model)
			if err != nil && !errors.Is(err, downloader.ErrNonHuggingFaceFile) {
				return err
			}
		}

		modelLoader := model.NewModelLoader(systemState)
		var installOptions []gallery.InstallOption
		if mi.Variant != "" {
			installOptions = append(installOptions, gallery.WithVariant(mi.Variant))
		}
		err = startup.InstallModelsWithOptions(context.Background(), galleryService, galleries, backendGalleries, systemState, modelLoader, !mi.DisablePredownloadScan, mi.AutoloadBackendGalleries, mi.RequireBackendIntegrity, progressCallback, installOptions, modelName)
		if err != nil {
			return err
		}
	}
	return nil
}
