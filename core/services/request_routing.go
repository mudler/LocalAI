package services

import (
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/pkg/model"
)

type RequestRoutingService struct {
	configLoader            *config.BackendConfigLoader
	modelLoader             *model.ModelLoader
	appConfig               *config.ApplicationConfig
	ruleBasedBackendService *RuleBasedBackendService
}

func NewRequestRoutingService(configLoader *config.BackendConfigLoader, modelLoader *model.ModelLoader, appConfig *config.ApplicationConfig) RequestRoutingService {
	bls := RequestRoutingService{
		configLoader: configLoader,
		modelLoader:  modelLoader,
		appConfig:    appConfig,
	}

	// TODO: do simple non rule modes require individual handling? Collapse these if not
	switch appConfig.BackendLoaderStrategy {
	case "":
		bls.ruleBasedBackendService = nil
	case "always":
		bls.ruleBasedBackendService = nil
	case "never":
		bls.ruleBasedBackendService = nil
	default:
		rbbs := NewRuleBasedBackendService(configLoader, modelLoader, appConfig)
		bls.ruleBasedBackendService = &rbbs
	}

	return bls
}

func (rrs *RequestRoutingService) RouteRequest(source string, endpoint string, request interface{}) error {

}
