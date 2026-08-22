package modeladmin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/safefile"
)

type savedMutationFile struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

func (s *ConfigService) withMutationRollback(paths []string, mutate func() error) error {
	configs := s.Loader.GetAllModelsConfigs()
	files := make([]savedMutationFile, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		name, err := directMutationEntry(s.modelsPath(), path)
		if err != nil {
			return fmt.Errorf("snapshot config mutation: %w", err)
		}
		file := savedMutationFile{path: path}
		file.data, file.mode, err = safefile.ReadRegularAt(s.modelsPath(), name)
		if err == nil {
			file.exists = true
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("snapshot config mutation: %w", err)
		}
		files = append(files, file)
	}

	if err := mutate(); err != nil {
		var restoreErr error
		for _, file := range files {
			if file.exists {
				restoreErr = errors.Join(restoreErr, writeFileAtomic(file.path, file.data, file.mode))
			} else if removeErr := os.Remove(file.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				restoreErr = errors.Join(restoreErr, removeErr)
			}
		}
		s.Loader.ReplaceModelConfigs(configs)
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore prior model configuration: %w", restoreErr))
		}
		return err
	}
	return nil
}

func directMutationEntry(modelsPath, path string) (string, error) {
	root, err := filepath.Abs(modelsPath)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Dir(candidate) != root {
		return "", fmt.Errorf("config path %q is not a direct entry of the configured models directory", path)
	}
	return filepath.Base(candidate), nil
}
