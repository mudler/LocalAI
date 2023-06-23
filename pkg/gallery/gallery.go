package gallery

import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/imdario/mergo"
	"gopkg.in/yaml.v2"
)

type Gallery struct {
	URL  string `json:"url" yaml:"url"`
	Name string `json:"name" yaml:"name"`
}

// Installs a model from the gallery (galleryname@modelname)
func InstallModelFromGallery(galleries []Gallery, name string, basePath string, req GalleryModel, downloadStatus func(string, string, string, float64)) error {
	models, err := AvailableGalleryModels(galleries)
	if err != nil {
		return err
	}

	applyModel := func(model *GalleryModel) error {
		var config Config

		err := model.Get(&config)
		if err != nil {
			return err
		}

		if req.Name != "" {
			model.Name = req.Name
		}

		config.Files = append(config.Files, req.AdditionalFiles...)
		config.Files = append(config.Files, model.AdditionalFiles...)

		// TODO model.Overrides could be merged with user overrides (not defined yet)
		if err := mergo.Merge(&model.Overrides, req.Overrides, mergo.WithOverride); err != nil {
			return err
		}

		if err := InstallModel(basePath, model.Name, &config, model.Overrides, downloadStatus); err != nil {
			return err
		}

		return nil
	}

	for _, model := range models {
		if name == fmt.Sprintf("%s@%s", model.Gallery.Name, model.Name) {
			return applyModel(model)
		}
	}

	return fmt.Errorf("no model found with name %q", name)
}

// List available models
// Models galleries are a list of json files that are hosted on a remote server (for example github).
// Each json file contains a list of models that can be downloaded and optionally overrides to define a new model setting.
func AvailableGalleryModels(galleries []Gallery) ([]*GalleryModel, error) {
	var models []*GalleryModel

	// Get models from galleries
	for _, gallery := range galleries {
		galleryModels, err := getGalleryModels(gallery)
		if err != nil {
			return nil, err
		}
		models = append(models, galleryModels...)
	}

	return models, nil
}

func getGalleryModels(gallery Gallery) ([]*GalleryModel, error) {
	var models []*GalleryModel = []*GalleryModel{}

	err := utils.GetURI(gallery.URL, func(d []byte) error {
		return yaml.Unmarshal(d, &models)
	})
	if err != nil {
		return models, err
	}

	// Add gallery to models
	for _, model := range models {
		model.Gallery = gallery
	}
	return models, nil
}
