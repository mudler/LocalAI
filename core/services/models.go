package services

import (
	"encoding/json"
	"os"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v2"
)

func (g *GalleryService) modelHandler(op *GalleryOp[gallery.GalleryModel], cl *config.ModelConfigLoader, systemState *system.SystemState) error {
	utils.ResetDownloadTimers()

	g.UpdateStatus(op.ID, &GalleryOpStatus{Message: "processing", Progress: 0})

	// displayDownload displays the download progress
	progressCallback := func(fileName string, current string, total string, percentage float64) {
		g.UpdateStatus(op.ID, &GalleryOpStatus{Message: "processing", FileName: fileName, Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
		utils.DisplayDownloadFunction(fileName, current, total, percentage)
	}

	err := processModelOperation(op, systemState, g.appConfig.EnforcePredownloadScans, g.appConfig.AutoloadBackendGalleries, progressCallback)
	if err != nil {
		return err
	}

	// Reload models
	err = cl.LoadModelConfigsFromPath(systemState.Model.ModelsPath)
	if err != nil {
		return err
	}

	err = cl.Preload(systemState.Model.ModelsPath)
	if err != nil {
		return err
	}

	g.UpdateStatus(op.ID,
		&GalleryOpStatus{
			Deletion:           op.Delete,
			Processed:          true,
			GalleryElementName: op.GalleryElementName,
			Message:            "completed",
			Progress:           100})

	return nil
}

func installModelFromRemoteConfig(systemState *system.SystemState, req gallery.GalleryModel, downloadStatus func(string, string, string, float64), enforceScan, automaticallyInstallBackend bool, backendGalleries []config.Gallery) error {
	config, err := gallery.GetGalleryConfigFromURL[gallery.ModelConfig](req.URL, systemState.Model.ModelsPath)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	installedModel, err := gallery.InstallModel(systemState, req.Name, &config, req.Overrides, downloadStatus, enforceScan)
	if err != nil {
		return err
	}

	if automaticallyInstallBackend && installedModel.Backend != "" {
		if err := gallery.InstallBackendFromGallery(backendGalleries, systemState, installedModel.Backend, downloadStatus, false); err != nil {
			return err
		}
	}

	return nil
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(systemState *system.SystemState, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, requests []galleryModel) error {
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = installModelFromRemoteConfig(systemState, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend, backendGalleries)

		} else {
			err = gallery.InstallModelFromGallery(
				galleries, backendGalleries, systemState, r.ID, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend)
		}
	}
	return err
}

func ApplyGalleryFromFile(systemState *system.SystemState, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(systemState, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests)
}

func ApplyGalleryFromString(systemState *system.SystemState, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(systemState, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests)
}

// processModelOperation handles the installation or deletion of a model
func processModelOperation(
	op *GalleryOp[gallery.GalleryModel],
	systemState *system.SystemState,
	enforcePredownloadScans bool,
	automaticallyInstallBackend bool,
	progressCallback func(string, string, string, float64),
) error {
	// delete a model
	if op.Delete {
		return gallery.DeleteModelFromSystem(systemState, op.GalleryElementName)
	}

	// if the request contains a gallery name, we apply the gallery from the gallery list
	if op.GalleryElementName != "" {
		return gallery.InstallModelFromGallery(op.Galleries, op.BackendGalleries, systemState, op.GalleryElementName, op.Req, progressCallback, enforcePredownloadScans, automaticallyInstallBackend)
		// } else if op.ConfigURL != "" {
		// 	err := startup.InstallModels(op.Galleries, modelPath, enforcePredownloadScans, progressCallback, op.ConfigURL)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	return cl.Preload(modelPath)
	} else {
		return installModelFromRemoteConfig(systemState, op.Req, progressCallback, enforcePredownloadScans, automaticallyInstallBackend, op.BackendGalleries)
	}
}
