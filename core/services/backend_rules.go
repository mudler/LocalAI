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

const ruleBasedBackendServiceKLName = "RuleBasedBackendService"
const ruleBasedBackendServiceKLVersion = "0.0.1"

type RuleBasedBackendResult struct {
	Action    string
	ModelName string
	// TODO other?
}

type ruleBasedBackendResultActionDefinitionsStruct struct {
	Blank    string
	Continue string
	Error    string
	Enqueue  string
}

var ruleBasedBackendResultActionDefinitions := ruleBasedBackendResultActionDefinitionsStruct{
	Blank:    "",
	Continue: "continue",
	Error:    "error",
	Enqueue:  "enqueue",
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

func (rbbs RuleBasedBackendService) RuleBasedLoad(modelName string, alreadyLoadedResult *RuleBasedBackendResult, source string, optionalRequest interface{}) (*RuleBasedBackendResult, error) {
	backendId, bc, err := getModelLoaderIDFromModelName(rbbs.configLoader, modelName)
	if err != nil {
		return nil, err
	}
	lmm := rbbs.modelLoader.CheckIsLoaded(backendId, true)
	if lmm != nil {
		return alreadyLoadedResult, nil
	}
	result := RuleBasedBackendResult{}
	ruleBasedLoadDataCtx := ast.NewDataContext()

	ruleBasedLoadDataCtx.Add("ModelLoader", rbbs.modelLoader)
	ruleBasedLoadDataCtx.Add("LoadedModelCount", rbbs.modelLoader.LoadedModelCount())
	ruleBasedLoadDataCtx.Add("LoadedModels", rbbs.modelLoader.SortedLoadedModelMetadata())

	ruleBasedLoadDataCtx.Add("ActionDefs", ruleBasedBackendResultActionDefinitions)

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
