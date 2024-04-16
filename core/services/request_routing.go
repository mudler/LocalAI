package services

import (
	"fmt"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type RequestRoutingService struct {
	configLoader            *config.BackendConfigLoader
	modelLoader             *model.ModelLoader
	appConfig               *config.ApplicationConfig
	ruleBasedBackendService *RuleBasedBackendService
	continueIfLoaded        bool
}

func NewRequestRoutingService(configLoader *config.BackendConfigLoader, modelLoader *model.ModelLoader, appConfig *config.ApplicationConfig) *RequestRoutingService {
	rrs := &RequestRoutingService{
		configLoader: configLoader,
		modelLoader:  modelLoader,
		appConfig:    appConfig,
	}

	// TODO: do simple non rule modes require individual handling? Collapse these if not
	switch appConfig.BackendLoaderStrategy {
	case "":
		// TODO: Should this set continueIfLoaded, or be a distinct case from "always"
		rrs.ruleBasedBackendService = nil
	case "always":
		rrs.continueIfLoaded = true
		rrs.ruleBasedBackendService = nil
	case "never":
		rrs.continueIfLoaded = true
		rrs.ruleBasedBackendService = nil
	default:
		rbbs := NewRuleBasedBackendService(configLoader, modelLoader, appConfig)
		rrs.ruleBasedBackendService = &rbbs
	}

	return rrs
}

func (rrs *RequestRoutingService) ExtractModelName(request interface{}) (string, error) {
	switch request.(type) {
	case *schema.OpenAIRequest:
		return request.(*schema.OpenAIRequest).Model, nil
	case schema.OpenAIRequest: // TODO: Do we need both variants for each of these?
		return request.(schema.OpenAIRequest).Model, nil
	case *schema.TTSRequest:
		return request.(*schema.TTSRequest).Model, nil
	case schema.TTSRequest:
		return request.(schema.TTSRequest).Model, nil
	case *schema.ElevenLabsTTSRequest:
		return request.(*schema.ElevenLabsTTSRequest).ModelID, nil
	case schema.ElevenLabsTTSRequest:
		return request.(schema.ElevenLabsTTSRequest).ModelID, nil
	case *schema.BackendMonitorRequest:
		return request.(*schema.BackendMonitorRequest).Model, nil
	case schema.BackendMonitorRequest:
		return request.(schema.BackendMonitorRequest).Model, nil
	// TODO: should these be here, I think to start with, yes.
	case *schema.StoresSet:
		return request.(*schema.StoresSet).Store, nil
	case schema.StoresSet:
		return request.(schema.StoresSet).Store, nil
	case *schema.StoresGet:
		return request.(*schema.StoresGet).Store, nil
	case schema.StoresGet:
		return request.(schema.StoresGet).Store, nil
	case *schema.StoresFind:
		return request.(*schema.StoresFind).Store, nil
	case schema.StoresFind:
		return request.(schema.StoresFind).Store, nil
	case *schema.StoresDelete:
		return request.(*schema.StoresDelete).Store, nil
	case schema.StoresDelete:
		return request.(schema.StoresDelete).Store, nil
	default:
		return "", fmt.Errorf("unrecognized request type %T", request)
	}
}

func (rrs *RequestRoutingService) RouteRequest(source string, endpoint string, request interface{}) (string, string, interface{}, error) {
	log.Debug().Msgf("[RRS] RouteRequest top source: %q endpoint: %q request %+v", source, endpoint, request)
	if rrs.ruleBasedBackendService == nil {
		log.Debug().Msg("[RRS] early exit")
		return source, endpoint, request, nil
	}
	modelName, err := rrs.ExtractModelName(request)
	log.Debug().Msgf("[RRS] ExtractModelName %q // %q", modelName, err)
	if err != nil {
		return source, endpoint, request, err
	}
	ruleResult, err := rrs.ruleBasedBackendService.RuleBasedLoad(modelName, rrs.continueIfLoaded, source, request)
	if err != nil {
		log.Error().Msgf("[RRS] RULE ERROR: %q", err)
		if ruleResult != nil {
			// Partial Success of Rule Evaluation
			source = ruleResult.Destination
			endpoint = ruleResult.Endpoint
		}
		return source, endpoint, request, err
	}
	log.Debug().Msgf("[RRS] result: %+v", ruleResult)
	switch ruleResult.Action {
	case ruleBasedBackendResultActionDefinitions.Continue:
		return ruleResult.Destination, ruleResult.Endpoint, request, nil
	case ruleBasedBackendResultActionDefinitions.Error:
		return ruleResult.Destination, ruleResult.Endpoint, request, fmt.Errorf("rule error: %w", ruleResult.Error)
	default:
		return ruleResult.Destination, ruleResult.Endpoint, request, fmt.Errorf("unknown action type %q: %w", ruleResult.Action, ruleResult.Error)
	}

}
