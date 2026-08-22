package modeladmin

import (
	"errors"
	"fmt"
	"os"
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
		file := savedMutationFile{path: path}
		info, err := os.Stat(path)
		if err == nil {
			file.exists = true
			file.mode = info.Mode().Perm()
			file.data, err = os.ReadFile(path)
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
