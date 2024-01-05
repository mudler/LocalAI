package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/imdario/mergo"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

type Gallery struct {
	URL  string `json:"url" yaml:"url"`
	Name string `json:"name" yaml:"name"`
}

// Installs a model from the gallery (galleryname@modelname)
func InstallModelFromGallery(galleries []Gallery, name string, basePath string, req GalleryModel, downloadStatus func(string, string, string, float64)) error {
	applyModel := func(model *GalleryModel) error {
		name = strings.ReplaceAll(name, string(os.PathSeparator), "__")

		var config Config

		if len(model.URL) > 0 {
			var err error
			config, err = GetGalleryConfigFromURL(model.URL)
			if err != nil {
				return err
			}
		} else if len(model.ConfigFile) > 0 {
			// TODO: is this worse than using the override method with a blank cfg yaml?
			reYamlConfig, err := yaml.Marshal(model.ConfigFile)
			if err != nil {
				return err
			}
			config = Config{
				ConfigFile:  string(reYamlConfig),
				Description: model.Description,
				License:     model.License,
				URLs:        model.URLs,
				Name:        model.Name,
				Files:       make([]File, 0), // Real values get added below, must be blank
				// Prompt Template Skipped for now - I expect in this mode that they will be delivered as files.
			}
		} else {
			return fmt.Errorf("invalid gallery model %+v", model)
		}

		installName := model.Name
		if req.Name != "" {
			installName = req.Name
		}

		config.Files = append(config.Files, req.AdditionalFiles...)
		config.Files = append(config.Files, model.AdditionalFiles...)

		// TODO model.Overrides could be merged with user overrides (not defined yet)
		if err := mergo.Merge(&model.Overrides, req.Overrides, mergo.WithOverride); err != nil {
			return err
		}

		if err := InstallModel(basePath, installName, &config, model.Overrides, downloadStatus); err != nil {
			return err
		}

		return nil
	}

	models, err := AvailableGalleryModels(galleries, basePath)
	if err != nil {
		return err
	}

	model, err := FindGallery(models, name)
	if err != nil {
		var err2 error
		model, err2 = FindGallery(models, strings.ToLower(name))
		if err2 != nil {
			return err
		}
	}

	return applyModel(model)
}

func FindGallery(models []*GalleryModel, name string) (*GalleryModel, error) {
	// os.PathSeparator is not allowed in model names. Replace them with "__" to avoid conflicts with file paths.
	name = strings.ReplaceAll(name, string(os.PathSeparator), "__")

	for _, model := range models {
		if name == fmt.Sprintf("%s@%s", model.Gallery.Name, model.Name) {
			return model, nil
		}
	}
	return nil, fmt.Errorf("no gallery found with name %q", name)
}

// InstallModelFromGalleryByName loads a model from the gallery by specifying only the name (first match wins)
func InstallModelFromGalleryByName(galleries []Gallery, name string, basePath string, req GalleryModel, downloadStatus func(string, string, string, float64)) error {
	models, err := AvailableGalleryModels(galleries, basePath)
	if err != nil {
		return err
	}

	name = strings.ReplaceAll(name, string(os.PathSeparator), "__")
	var model *GalleryModel
	for _, m := range models {
		if name == m.Name || m.Name == strings.ToLower(name) {
			model = m
		}
	}

	if model == nil {
		return fmt.Errorf("no model found with name %q", name)
	}

	return InstallModelFromGallery(galleries, fmt.Sprintf("%s@%s", model.Gallery.Name, model.Name), basePath, req, downloadStatus)
}

// List available models
// Models galleries are a list of yaml files that are hosted on a remote server (for example github).
// Each yaml file contains a list of models that can be downloaded and optionally overrides to define a new model setting.
func AvailableGalleryModels(galleries []Gallery, basePath string) ([]*GalleryModel, error) {
	var models []*GalleryModel

	// Get models from galleries
	for _, gallery := range galleries {
		galleryModels, err := getGalleryModels(gallery, basePath)
		if err != nil {
			return nil, err
		}
		models = append(models, galleryModels...)
	}

	return models, nil
}

func findGalleryURLFromReferenceURL(url string) (string, error) {
	var refFile string
	err := downloader.GetURI(url, func(url string, d []byte) error {
		refFile = string(d)
		if len(refFile) == 0 {
			return fmt.Errorf("invalid reference file at url %s: %s", url, d)
		}
		cutPoint := strings.LastIndex(url, "/")
		refFile = url[:cutPoint+1] + refFile
		return nil
	})
	return refFile, err
}

func getGalleryModels(gallery Gallery, basePath string) ([]*GalleryModel, error) {
	var models []*GalleryModel = []*GalleryModel{}

	if strings.HasSuffix(gallery.URL, ".ref") {
		var err error
		gallery.URL, err = findGalleryURLFromReferenceURL(gallery.URL)
		if err != nil {
			return models, err
		}
	}

	err := downloader.GetURI(gallery.URL, func(url string, d []byte) error {
		return yaml.Unmarshal(d, &models)
	})
	if err != nil {
		if yamlErr, ok := err.(*yaml.TypeError); ok {
			log.Debug().Msgf("YAML errors: %s\n\nwreckage of models: %+v", strings.Join(yamlErr.Errors, "\n"), models)
		}
		return models, err
	}

	// Add gallery to models
	for _, model := range models {
		model.Gallery = gallery
		// we check if the model was already installed by checking if the config file exists
		// TODO: (what to do if the model doesn't install a config file?)
		if _, err := os.Stat(filepath.Join(basePath, fmt.Sprintf("%s.yaml", model.Name))); err == nil {
			model.Installed = true
		}
	}
	return models, nil
}
