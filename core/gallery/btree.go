package gallery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/btree"
	"gopkg.in/yaml.v3"
)

type Gallery[T GalleryElement] struct {
	*btree.BTreeG[T]
}

func less[T GalleryElement](a, b T) bool {
	return a.GetName() < b.GetName()
}

func (g *Gallery[T]) Search(term string) GalleryElements[T] {
	var filteredModels GalleryElements[T]
	g.Ascend(func(item T) bool {
		if strings.Contains(item.GetName(), term) ||
			strings.Contains(item.GetDescription(), term) ||
			strings.Contains(item.GetGallery().Name, term) ||
			strings.Contains(strings.Join(item.GetTags(), ","), term) {
			filteredModels = append(filteredModels, item)
		}
		return true
	})

	return filteredModels
}

// processYAMLFile takes a single file path and adds its models to the existing tree.
func processYAMLFile[T GalleryElement](filePath string, tree *Gallery[T]) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("could not open %s: %w", filePath, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)

	// Stream documents from the file
	for {
		var model T
		err := decoder.Decode(&model)
		if err == io.EOF {
			break // End of file
		}
		if err != nil {
			return fmt.Errorf("error decoding %s: %w", filePath, err)
		}

		tree.ReplaceOrInsert(model)
	}
	return nil
}

// loadModelsFromDirectory scans a directory and processes all .yaml/.yml files.
func LoadGalleryFromDirectory[T GalleryElement](dirPath string) (*Gallery[T], error) {
	tree := &Gallery[T]{btree.NewG[T](2, less[T])}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("could not read directory: %w", err)
	}

	for _, entry := range entries {
		// Skip subdirectories and non-YAML files.
		if entry.IsDir() {
			continue
		}
		fileName := entry.Name()
		if !strings.HasSuffix(fileName, ".yaml") && !strings.HasSuffix(fileName, ".yml") {
			continue
		}

		fullPath := filepath.Join(dirPath, fileName)

		err := processYAMLFile(fullPath, tree)
		if err != nil {
			return nil, err
		}
	}

	return tree, nil
}
