package services

import (
	"os"
	"path"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/hyperjumptech/grule-rule-engine/ast"
	"github.com/hyperjumptech/grule-rule-engine/builder"
	"github.com/hyperjumptech/grule-rule-engine/engine"
	"github.com/hyperjumptech/grule-rule-engine/pkg"
)

// I am not sure that I like the naming conventions in this file.
// It predates the request_routing concept, and just is here for now
// Open to suggestions, may get refactored to RequestRoutingRuleService
// Is seperate from the base RequestRouting service to avoid loading all the grules stuff for lightweight environments

const ruleBasedBackendServiceKLName = "RuleBasedBackendService"
const ruleBasedBackendServiceKLVersion = "0.0.1"

type RuleBasedBackendResult struct {
	Action      string
	Destination string
	Endpoint    string
	ModelName   string
	Error       error
	// TODO other?
}

// TODO: Is there a more enum-like way to accomplish this? It seems like in grule script land, it'll be easier to manage as string constants

type ruleBasedBackendResultDestinationDefinitionsStruct struct {
	Http   string
	Mqtt   string
	StdOut string
}

var ruleBasedBackendResultDestinationDefinitions = ruleBasedBackendResultDestinationDefinitionsStruct{
	Http:   "http",
	Mqtt:   "mqtt",
	StdOut: "stdout",
}

type ruleBasedBackendResultActionDefinitionsStruct struct {
	Blank    string
	Continue string
	Error    string
	Relay    string
}

var ruleBasedBackendResultActionDefinitions ruleBasedBackendResultActionDefinitionsStruct = ruleBasedBackendResultActionDefinitionsStruct{
	Blank:    "",
	Continue: "continue",
	Error:    "error",
	Relay:    "relay",
}

type RuleBasedBackendService struct {
	configLoader     *config.BackendConfigLoader
	modelLoader      *model.ModelLoader
	appConfig        *config.ApplicationConfig
	knowledgeLibrary *ast.KnowledgeLibrary
}

func NewRuleBasedBackendService(configLoader *config.BackendConfigLoader, modelLoader *model.ModelLoader, appConfig *config.ApplicationConfig) RuleBasedBackendService {
	rbbs := RuleBasedBackendService{
		configLoader: configLoader,
		modelLoader:  modelLoader,
		appConfig:    appConfig,
	}

	// TODO: Phase 2 is to have bundled rule sets for common scenarios, such as always allow, SINGLE_BACKEND, only allowing authorized requests to load new backends, etc
	// For now, no settings for that, always use a custom json file for testing.
	// Phase 2 also should involve having _multiple_ json files loaded into the KL, for mix and match
	res, err := rbbs.getExternalRuleFileResource()
	if err != nil {
		rbbs.ReloadRules(res)
	}

	return rbbs
}

func (rbbs RuleBasedBackendService) getExternalRuleFileResource() (pkg.Resource, error) {
	ruleFilePath := path.Join(rbbs.appConfig.ConfigsDir, "backend_rules.json")
	f, err := os.Open(ruleFilePath)
	if err != nil {
		return nil, err
	}
	underlying := pkg.NewReaderResource(f)
	resource, err := pkg.NewJSONResourceFromResource(underlying)
	if err != nil {
		return nil, err
	}
	return resource, nil
}

func (rbbs RuleBasedBackendService) ReloadRules(res pkg.Resource) error {
	rbbs.knowledgeLibrary = ast.NewKnowledgeLibrary()
	ruleBuilder := builder.NewRuleBuilder(rbbs.knowledgeLibrary)
	return ruleBuilder.BuildRuleFromResource(ruleBasedBackendServiceKLName, ruleBasedBackendServiceKLVersion, res)
}

func (rbbs RuleBasedBackendService) RuleBasedLoad(modelName string, continueIfLoaded bool, source string, optionalRequest interface{}) (*RuleBasedBackendResult, error) {
	backendId, bc, err := getModelLoaderIDFromModelName(rbbs.configLoader, modelName)
	if err != nil {
		return nil, err
	}
	result := RuleBasedBackendResult{ // By default, requests route themselves to their origin. Not worth specifying in every rule layout.
		ModelName:   modelName,
		Destination: source,
		// Action is intentionally omitted here.
	}
	lmm := rbbs.modelLoader.CheckIsLoaded(backendId, true)
	if continueIfLoaded && (lmm != nil) {
		result.Action = ruleBasedBackendResultActionDefinitions.Continue
		return &result, nil
	}

	ruleBasedLoadDataCtx := ast.NewDataContext()

	ruleBasedLoadDataCtx.Add("ModelLoader", rbbs.modelLoader)
	ruleBasedLoadDataCtx.Add("LoadedModelCount", rbbs.modelLoader.LoadedModelCount())
	ruleBasedLoadDataCtx.Add("LoadedModels", rbbs.modelLoader.SortedLoadedModelMetadata())
	ruleBasedLoadDataCtx.Add("LoadedModelMetadata", lmm)

	ruleBasedLoadDataCtx.Add("ActionDefs", ruleBasedBackendResultActionDefinitions)
	ruleBasedLoadDataCtx.Add("DestinationDefs", ruleBasedBackendResultActionDefinitions)

	ruleBasedLoadDataCtx.Add("RequestedModelName", modelName)
	ruleBasedLoadDataCtx.Add("Source", source)
	ruleBasedLoadDataCtx.Add("Request", optionalRequest)

	ruleBasedLoadDataCtx.Add("BackendConfig", bc)
	ruleBasedLoadDataCtx.Add("AppConfig", rbbs.appConfig)

	ruleBasedLoadDataCtx.Add("Result", result)

	knowledgeBase, err := rbbs.knowledgeLibrary.NewKnowledgeBaseInstance(ruleBasedBackendServiceKLName, ruleBasedBackendServiceKLVersion)
	if err != nil {
		return nil, err
	}
	engine := engine.NewGruleEngine()
	err = engine.Execute(ruleBasedLoadDataCtx, knowledgeBase)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
