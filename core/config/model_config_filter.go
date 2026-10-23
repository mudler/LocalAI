package config

import "regexp"

type ModelConfigFilterFn func(string, *ModelConfig) bool

func NoFilterFn(_ string, _ *ModelConfig) bool { return true }

func BuildNameFilterFn(filter string) (ModelConfigFilterFn, error) {
	if filter == "" {
		return NoFilterFn, nil
	}
	rxp, err := regexp.Compile(filter)
	if err != nil {
		return nil, err
	}
	return func(name string, config *ModelConfig) bool {
		if config != nil {
			return rxp.MatchString(config.Name)
		}
		return rxp.MatchString(name)
	}, nil
}

func BuildUsecaseFilterFn(usecases ModelConfigUsecase) ModelConfigFilterFn {
	if usecases == FLAG_ANY {
		return NoFilterFn
	}
	return func(name string, config *ModelConfig) bool {
		if config == nil {
			return false // TODO: Potentially make this a param, for now, no known usecase to include
		}
		return config.HasUsecases(usecases)
	}
}

// FilterMayMatchLooseFiles reports whether filter can accept a model file that
// has no configuration. A nil filter means "no filtering" and accepts them.
// Usecase filters need a configuration, so they never match loose files and a
// directory scan cannot add results for them. Arbitrary filters cannot be
// inspected, so the answer comes from probing with typical file names; any
// accepted probe keeps the directory scan.
func FilterMayMatchLooseFiles(filter ModelConfigFilterFn) bool {
	if filter == nil {
		return true
	}
	for _, name := range []string{"", "model", "model.gguf", "model.bin", "model.safetensors", "a/b"} {
		if filter(name, nil) {
			return true
		}
	}
	return false
}
