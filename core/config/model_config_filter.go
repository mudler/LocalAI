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

func BuildUsecaseFilterFn(usecases ModelConfigUsecases) ModelConfigFilterFn {
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
