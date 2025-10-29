package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"

	"gopkg.in/yaml.v2"
)

func GetGalleryConfigFromURL[T any](url string, basePath string) (T, error) {
	var config T
	uri := downloader.URI(url)
	err := uri.DownloadWithCallback(basePath, func(url string, d []byte) error {
		return yaml.Unmarshal(d, &config)
	})
	if err != nil {
		log.Error().Err(err).Str("url", url).Msg("failed to get gallery config for url")
		return config, err
	}
	return config, nil
}

func ReadConfigFile[T any](filePath string) (*T, error) {
	// Read the YAML file
	yamlFile, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %v", err)
	}

	// Unmarshal YAML data into a Config struct
	var config T
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %v", err)
	}

	return &config, nil
}

type GalleryElement interface {
	SetGallery(gallery config.Gallery)
	SetInstalled(installed bool)
	GetName() string
	GetDescription() string
	GetTags() []string
	GetGallery() config.Gallery
}

type GalleryElements[T GalleryElement] []T

func (gm GalleryElements[T]) Search(term string) GalleryElements[T] {
	var filteredModels GalleryElements[T]
	term = strings.ToLower(term)
	for _, m := range gm {
		if fuzzy.Match(term, strings.ToLower(m.GetName())) ||
			fuzzy.Match(term, strings.ToLower(m.GetGallery().Name)) ||
			strings.Contains(strings.ToLower(m.GetName()), term) ||
			strings.Contains(strings.ToLower(m.GetDescription()), term) ||
			strings.Contains(strings.ToLower(m.GetGallery().Name), term) ||
			strings.Contains(strings.ToLower(strings.Join(m.GetTags(), ",")), term) {
			filteredModels = append(filteredModels, m)
		}
	}

	return filteredModels
}

func (gm GalleryElements[T]) FindByName(name string) T {
	for _, m := range gm {
		if strings.EqualFold(m.GetName(), name) {
			return m
		}
	}
	var zero T
	return zero
}

func (gm GalleryElements[T]) Paginate(pageNum int, itemsNum int) GalleryElements[T] {
	start := (pageNum - 1) * itemsNum
	end := start + itemsNum
	if start > len(gm) {
		start = len(gm)
	}
	if end > len(gm) {
		end = len(gm)
	}
	return gm[start:end]
}

func FindGalleryElement[T GalleryElement](models []T, name string) T {
	var model T
	name = strings.ReplaceAll(name, string(os.PathSeparator), "__")

	if !strings.Contains(name, "@") {
		for _, m := range models {
			if strings.EqualFold(strings.ToLower(m.GetName()), strings.ToLower(name)) {
				model = m
				break
			}
		}

	} else {
		for _, m := range models {
			if strings.EqualFold(strings.ToLower(name), strings.ToLower(fmt.Sprintf("%s@%s", m.GetGallery().Name, m.GetName()))) {
				model = m
				break
			}
		}
	}

	return model
}

// List available models
// Models galleries are a list of yaml files that are hosted on a remote server (for example github).
// Each yaml file contains a list of models that can be downloaded and optionally overrides to define a new model setting.
func AvailableGalleryModels(galleries []config.Gallery, systemState *system.SystemState) (GalleryElements[*GalleryModel], error) {
	var models []*GalleryModel

	// Get models from galleries
	for _, gallery := range galleries {
		galleryModels, err := getGalleryElements[*GalleryModel](gallery, systemState.Model.ModelsPath, func(model *GalleryModel) bool {
			if _, err := os.Stat(filepath.Join(systemState.Model.ModelsPath, fmt.Sprintf("%s.yaml", model.GetName()))); err == nil {
				return true
			}
			return false
		})
		if err != nil {
			return nil, err
		}
		models = append(models, galleryModels...)
	}

	return models, nil
}

// List available backends
func AvailableBackends(galleries []config.Gallery, systemState *system.SystemState) (GalleryElements[*GalleryBackend], error) {
	var backends []*GalleryBackend

	systemBackends, err := ListSystemBackends(systemState)
	if err != nil {
		return nil, err
	}

	// Get backends from galleries
	for _, gallery := range galleries {
		galleryBackends, err := getGalleryElements(gallery, systemState.Backend.BackendsPath, func(backend *GalleryBackend) bool {
			return systemBackends.Exists(backend.GetName())
		})
		if err != nil {
			return nil, err
		}
		backends = append(backends, galleryBackends...)
	}

	return backends, nil
}

func findGalleryURLFromReferenceURL(url string, basePath string) (string, error) {
	var refFile string
	uri := downloader.URI(url)
	err := uri.DownloadWithCallback(basePath, func(url string, d []byte) error {
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

func getGalleryElements[T GalleryElement](gallery config.Gallery, basePath string, isInstalledCallback func(T) bool) ([]T, error) {
	var models []T = []T{}

	if strings.HasSuffix(gallery.URL, ".ref") {
		var err error
		gallery.URL, err = findGalleryURLFromReferenceURL(gallery.URL, basePath)
		if err != nil {
			return models, err
		}
	}
	uri := downloader.URI(gallery.URL)

	err := uri.DownloadWithCallback(basePath, func(url string, d []byte) error {
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
		model.SetGallery(gallery)
		model.SetInstalled(isInstalledCallback(model))
	}
	return models, nil
}
