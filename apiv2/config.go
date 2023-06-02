package apiv2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v2"
)

type ConfigRegistration struct {
	Endpoint string `yaml:"endpoint" json:"endpoint" mapstructure:"endpoint"`
	Model    string `yaml:"model" json:"model" mapstructure:"model"`
}

type ConfigLocalPaths struct {
	Model    string `yaml:"model" mapstructure:"model"`
	Template string `yaml:"template" mapstructure:"template"`
}

type ConfigStub struct {
	Registration ConfigRegistration `yaml:"registration" mapstructure:"registration"`
	LocalPaths   ConfigLocalPaths   `yaml:"local_paths" mapstructure:"local_paths"`
}

type SpecificConfig[RequestModel any] struct {
	ConfigStub      `mapstructure:",squash"`
	RequestDefaults RequestModel `yaml:"request_defaults" mapstructure:"request_defaults"`
}

// type Config struct {
// 	Registration    ConfigRegistration `yaml:"registration"`
// 	LocalPaths      ConfigLocalPaths   `yaml:"local_paths"`
// 	RequestDefaults interface{}        `yaml:"request_defaults"`
// }

type Config interface {
	GetRequestDefaults() interface{}
	GetLocalPaths() ConfigLocalPaths
	GetRegistration() ConfigRegistration
}

func (cs ConfigStub) GetRequestDefaults() interface{} {
	return nil
}

func (cs ConfigStub) GetLocalPaths() ConfigLocalPaths {
	return cs.LocalPaths
}

func (cs ConfigStub) GetRegistration() ConfigRegistration {
	return cs.Registration
}

func (sc SpecificConfig[RequestModel]) GetRequestDefaults() interface{} {
	return sc.RequestDefaults
}

func (sc SpecificConfig[RequestModel]) GetRequest() RequestModel {
	return sc.RequestDefaults
}

func (sc SpecificConfig[RequestModel]) GetLocalPaths() ConfigLocalPaths {
	return sc.LocalPaths
}

func (sc SpecificConfig[RequestModel]) GetRegistration() ConfigRegistration {
	return sc.Registration
}

type ConfigManager struct {
	configs map[ConfigRegistration]Config
	sync.Mutex
}

func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		configs: make(map[ConfigRegistration]Config),
	}
}

// Private helper method doesn't enforce the mutex. This is because loading at the directory level keeps the lock up the whole time, and I like that.
func (cm *ConfigManager) loadConfigFile(path string) (*Config, error) {
	fmt.Printf("INTERNAL loadConfigFile for %s\n", path)
	stub := ConfigStub{}
	f, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, &stub); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}
	fmt.Printf("RAW STUB: %+v\n", stub)

	endpoint := stub.Registration.Endpoint

	// EndpointConfigMap is generated over in localai.gen.go
	// It's a map that translates a string endpoint function name to an empty SpecificConfig[T], with the type parameter for that request.
	if structType, ok := EndpointConfigMap[endpoint]; ok {
		fmt.Printf("~~ EndpointConfigMap[%s]: %+v\n", endpoint, structType)
		tmpUnmarshal := map[string]interface{}{}
		if err := yaml.Unmarshal(f, &tmpUnmarshal); err != nil {
			if e, ok := err.(*yaml.TypeError); ok {
				fmt.Println("\n!!!!!Type error:", e)
			}
			return nil, fmt.Errorf("cannot unmarshal config file for %s: %w", endpoint, err)
		}
		fmt.Printf("$$$ tmpUnmarshal: %+v\n", tmpUnmarshal)
		mapstructure.Decode(tmpUnmarshal, &structType)

		fmt.Printf("AFTER UNMARSHAL %T\n%+v\n=======\n", structType, structType)

		// rawConfig.RequestDefaults = structType.GetRequestDefaults()

		cm.configs[structType.GetRegistration()] = structType
		// fmt.Printf("\n\n\n!!!!!HIT BOTTOM!!!!!!")
		return &structType, nil
		// fmt.Printf("\n\n\n!!!!!\n\n\nBIG MISS!\n\n%+v\n\n%T\n%T=====", specificStruct, specificStruct, structType)
	}

	// for i, ts := range EndpointToRequestBodyMap {
	// 	fmt.Printf("%s: %+v\n", i, ts)
	// }

	return nil, fmt.Errorf("failed to parse config for endpoint %s", endpoint)
}

func (cm *ConfigManager) LoadConfigFile(path string) (*Config, error) {
	fmt.Printf("LoadConfigFile TOP for %s", path)

	cm.Lock()
	fmt.Println("cm.Lock done")

	defer cm.Unlock()
	fmt.Println("cm.Unlock done")

	return cm.loadConfigFile(path)
}

func (cm *ConfigManager) LoadConfigDirectory(path string) ([]ConfigRegistration, error) {
	fmt.Printf("LoadConfigDirectory TOP for %s\n", path)
	cm.Lock()
	defer cm.Unlock()
	files, err := os.ReadDir(path)
	if err != nil {
		return []ConfigRegistration{}, err
	}
	fmt.Printf("os.ReadDir done, found %d files\n", len(files))

	for _, file := range files {
		// Skip anything that isn't yaml
		if !strings.Contains(file.Name(), ".yaml") {
			continue
		}
		_, err := cm.loadConfigFile(filepath.Join(path, file.Name()))
		if err != nil {
			return []ConfigRegistration{}, err
		}
	}

	fmt.Printf("LoadConfigDirectory DONE %d", len(cm.configs))

	return cm.listConfigs(), nil
}

func (cm *ConfigManager) GetConfig(r ConfigRegistration) (Config, bool) {
	cm.Lock()
	defer cm.Unlock()
	v, exists := cm.configs[r]
	return v, exists
}

// This is a convience function for endpoint functions to use.
// The advantage is it avoids errors in the endpoint string
// Not a clue what the performance cost of this is.
func (cm *ConfigManager) GetConfigForThisEndpoint(m string) (Config, bool) {
	endpoint := printCurrentFunctionName(2)
	return cm.GetConfig(ConfigRegistration{
		Model:    m,
		Endpoint: endpoint,
	})
}

func (cm *ConfigManager) listConfigs() []ConfigRegistration {
	var res []ConfigRegistration
	for k := range cm.configs {
		res = append(res, k)
	}
	return res
}

func (cm *ConfigManager) ListConfigs() []ConfigRegistration {
	cm.Lock()
	defer cm.Unlock()
	return cm.listConfigs()
}

// // Not sure about this one, but it seems like a decent place to stick it for an experiment at least.
// func (cm *ConfigManager) GetTextConfigForRequest()

// func (cm *ConfigMerger) LoadConfigs(path string) error {
// 	cm.Lock()
// 	defer cm.Unlock()
// 	files, err := ioutil.ReadDir(path)
// 	if err != nil {
// 		return err
// 	}

// 	for _, file := range files {
// 		// Skip templates, YAML and .keep files
// 		if !strings.Contains(file.Name(), ".yaml") {
// 			continue
// 		}
// 		c, err := ReadConfig(filepath.Join(path, file.Name()))
// 		if err == nil {
// 			cm.configs[ConfigLookup{Name: c.Name, Endpoint: c.Endpoint}] = *c
// 		}
// 	}

// 	return nil
// }

// func (cm *ConfigMerger) Get

// func updateConfig(config *Config, input *OpenAIRequest) {
// 	if input.Echo {
// 		config.Echo = input.Echo
// 	}
// 	if input.TopK != 0 {
// 		config.TopK = input.TopK
// 	}
// 	if input.TopP != 0 {
// 		config.TopP = input.TopP
// 	}

// 	if input.Temperature != 0 {
// 		config.Temperature = input.Temperature
// 	}

// 	if input.Maxtokens != 0 {
// 		config.Maxtokens = input.Maxtokens
// 	}

// 	switch stop := input.Stop.(type) {
// 	case string:
// 		if stop != "" {
// 			config.StopWords = append(config.StopWords, stop)
// 		}
// 	case []interface{}:
// 		for _, pp := range stop {
// 			if s, ok := pp.(string); ok {
// 				config.StopWords = append(config.StopWords, s)
// 			}
// 		}
// 	}

// 	if input.RepeatPenalty != 0 {
// 		config.RepeatPenalty = input.RepeatPenalty
// 	}

// 	if input.Keep != 0 {
// 		config.Keep = input.Keep
// 	}

// 	if input.Batch != 0 {
// 		config.Batch = input.Batch
// 	}

// 	if input.F16 {
// 		config.F16 = input.F16
// 	}

// 	if input.IgnoreEOS {
// 		config.IgnoreEOS = input.IgnoreEOS
// 	}

// 	if input.Seed != 0 {
// 		config.Seed = input.Seed
// 	}

// 	if input.Mirostat != 0 {
// 		config.Mirostat = input.Mirostat
// 	}

// 	if input.MirostatETA != 0 {
// 		config.MirostatETA = input.MirostatETA
// 	}

// 	if input.MirostatTAU != 0 {
// 		config.MirostatTAU = input.MirostatTAU
// 	}

// 	switch inputs := input.Input.(type) {
// 	case string:
// 		if inputs != "" {
// 			config.InputStrings = append(config.InputStrings, inputs)
// 		}
// 	case []interface{}:
// 		for _, pp := range inputs {
// 			switch i := pp.(type) {
// 			case string:
// 				config.InputStrings = append(config.InputStrings, i)
// 			case []interface{}:
// 				tokens := []int{}
// 				for _, ii := range i {
// 					tokens = append(tokens, int(ii.(float64)))
// 				}
// 				config.InputToken = append(config.InputToken, tokens)
// 			}
// 		}
// 	}

// 	switch p := input.Prompt.(type) {
// 	case string:
// 		config.PromptStrings = append(config.PromptStrings, p)
// 	case []interface{}:
// 		for _, pp := range p {
// 			if s, ok := pp.(string); ok {
// 				config.PromptStrings = append(config.PromptStrings, s)
// 			}
// 		}
// 	}
// }
// func readInput(c *fiber.Ctx, loader *model.ModelLoader, randomModel bool) (string, *OpenAIRequest, error) {
// 	input := new(OpenAIRequest)
// 	// Get input data from the request body
// 	if err := c.BodyParser(input); err != nil {
// 		return "", nil, err
// 	}

// 	modelFile := input.Model

// 	if c.Params("model") != "" {
// 		modelFile = c.Params("model")
// 	}

// 	received, _ := json.Marshal(input)

// 	log.Debug().Msgf("Request received: %s", string(received))

// 	// Set model from bearer token, if available
// 	bearer := strings.TrimLeft(c.Get("authorization"), "Bearer ")
// 	bearerExists := bearer != "" && loader.ExistsInModelPath(bearer)

// 	// If no model was specified, take the first available
// 	if modelFile == "" && !bearerExists && randomModel {
// 		models, _ := loader.ListModels()
// 		if len(models) > 0 {
// 			modelFile = models[0]
// 			log.Debug().Msgf("No model specified, using: %s", modelFile)
// 		} else {
// 			log.Debug().Msgf("No model specified, returning error")
// 			return "", nil, fmt.Errorf("no model specified")
// 		}
// 	}

// 	// If a model is found in bearer token takes precedence
// 	if bearerExists {
// 		log.Debug().Msgf("Using model from bearer token: %s", bearer)
// 		modelFile = bearer
// 	}
// 	return modelFile, input, nil
// }

// func readConfig(modelFile string, input *OpenAIRequest, cm *ConfigMerger, loader *model.ModelLoader, debug bool, threads, ctx int, f16 bool) (*Config, *OpenAIRequest, error) {
// 	// Load a config file if present after the model name
// 	modelConfig := filepath.Join(loader.ModelPath, modelFile+".yaml")
// 	if _, err := os.Stat(modelConfig); err == nil {
// 		if err := cm.LoadConfig(modelConfig); err != nil {
// 			return nil, nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
// 		}
// 	}

// 	var config *Config
// 	cfg, exists := cm.GetConfig(modelFile)
// 	if !exists {
// 		config = &Config{
// 			OpenAIRequest: defaultRequest(modelFile),
// 			ContextSize:   ctx,
// 			Threads:       threads,
// 			F16:           f16,
// 			Debug:         debug,
// 		}
// 	} else {
// 		config = &cfg
// 	}

// 	// Set the parameters for the language model prediction
// 	updateConfig(config, input)

// 	// Don't allow 0 as setting
// 	if config.Threads == 0 {
// 		if threads != 0 {
// 			config.Threads = threads
// 		} else {
// 			config.Threads = 4
// 		}
// 	}

// 	// Enforce debug flag if passed from CLI
// 	if debug {
// 		config.Debug = true
// 	}

// 	return config, input, nil
// }
