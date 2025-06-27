package services

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v2"
)

func (g *GalleryService) modelHandler(op *GalleryOp[gallery.GalleryModel], cl *config.BackendConfigLoader) error {
	utils.ResetDownloadTimers()

	g.UpdateStatus(op.ID, &GalleryOpStatus{Message: "processing", Progress: 0})

	// displayDownload displays the download progress
	progressCallback := func(fileName string, current string, total string, percentage float64) {
		g.UpdateStatus(op.ID, &GalleryOpStatus{Message: "processing", FileName: fileName, Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
		utils.DisplayDownloadFunction(fileName, current, total, percentage)
	}

	err := processModelOperation(op, g.appConfig.ModelPath, g.appConfig.BackendsPath, g.appConfig.EnforcePredownloadScans, g.appConfig.AutoloadBackendGalleries, progressCallback)
	if err != nil {
		return err
	}

	// Reload models
	err = cl.LoadBackendConfigsFromPath(g.appConfig.ModelPath)
	if err != nil {
		return err
	}

	err = cl.Preload(g.appConfig.ModelPath)
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

func installModelFromRemoteConfig(modelPath string, req gallery.GalleryModel, downloadStatus func(string, string, string, float64), enforceScan, automaticallyInstallBackend bool, backendGalleries []config.Gallery, backendBasePath string) error {
	config, err := gallery.GetGalleryConfigFromURL[gallery.ModelConfig](req.URL, modelPath)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	installedModel, err := gallery.InstallModel(modelPath, req.Name, &config, req.Overrides, downloadStatus, enforceScan)
	if err != nil {
		return err
	}

	if automaticallyInstallBackend && installedModel.Backend != "" {
		systemState, err := system.GetSystemState()
		if err != nil {
			return err
		}

		if err := gallery.InstallBackendFromGallery(backendGalleries, systemState, installedModel.Backend, backendBasePath, downloadStatus, false); err != nil {
			return err
		}
	}

	return nil
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(modelPath, backendBasePath string, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, requests []galleryModel) error {
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = installModelFromRemoteConfig(modelPath, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend, backendGalleries, backendBasePath)

		} else {
			err = gallery.InstallModelFromGallery(
				galleries, backendGalleries, r.ID, modelPath, backendBasePath, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend)
		}
	}
	return err
}

func ApplyGalleryFromFile(modelPath, backendBasePath string, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(modelPath, backendBasePath, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests)
}

func ApplyGalleryFromString(modelPath, backendBasePath string, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(modelPath, backendBasePath, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests)
}

// processModelOperation handles the installation or deletion of a model
func processModelOperation(
	op *GalleryOp[gallery.GalleryModel],
	modelPath string,
	backendBasePath string,
	enforcePredownloadScans bool,
	automaticallyInstallBackend bool,
	progressCallback func(string, string, string, float64),
) error {
	// delete a model
	if op.Delete {
		modelConfig := &config.BackendConfig{}

		// Galleryname is the name of the model in this case
		dat, err := os.ReadFile(filepath.Join(modelPath, op.GalleryElementName+".yaml"))
		if err != nil {
			return err
		}
		err = yaml.Unmarshal(dat, modelConfig)
		if err != nil {
			return err
		}

		files := []string{}
		// Remove the model from the config
		if modelConfig.Model != "" {
			files = append(files, modelConfig.ModelFileName())
		}

		if modelConfig.MMProj != "" {
			files = append(files, modelConfig.MMProjFileName())
		}

		return gallery.DeleteModelFromSystem(modelPath, op.GalleryElementName, files)
	}

	// if the request contains a gallery name, we apply the gallery from the gallery list
	if op.GalleryElementName != "" {
		return gallery.InstallModelFromGallery(op.Galleries, op.BackendGalleries, op.GalleryElementName, modelPath, backendBasePath, op.Req, progressCallback, enforcePredownloadScans, automaticallyInstallBackend)
		// } else if op.ConfigURL != "" {
		// 	err := startup.InstallModels(op.Galleries, modelPath, enforcePredownloadScans, progressCallback, op.ConfigURL)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	return cl.Preload(modelPath)
	} else {
		return installModelFromRemoteConfig(modelPath, op.Req, progressCallback, enforcePredownloadScans, automaticallyInstallBackend, op.BackendGalleries, backendBasePath)
	}
}
