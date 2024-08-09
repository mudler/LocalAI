package config

import "regexp"

type BackendConfigFilterFn func(string, *BackendConfig) bool

func NoFilterFn(_ string, _ *BackendConfig) bool { return true }

func BuildNameFilterFn(filter string) (BackendConfigFilterFn, error) {
	if filter == "" {
		return NoFilterFn, nil
	}
	rxp, err := regexp.Compile(filter)
	if err != nil {
		return nil, err
	}
	return func(name string, config *BackendConfig) bool {
		if config != nil {
			return rxp.MatchString(config.Name)
		}
		return rxp.MatchString(name)
	}, nil
}

func BuildUsecaseFilterFn(usecases BackendConfigUsecases) BackendConfigFilterFn {
	if usecases == FLAG_ANY {
		return NoFilterFn
	}
	return func(name string, config *BackendConfig) bool {
		if config == nil {
			return false // TODO: Potentially make this a param, for now, no known usecase to include
		}
		return config.HasUsecases(usecases)
	}
}
