package apiv2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	llama "github.com/go-skynet/go-llama.cpp"
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

// These functions I'm a bit dubious about. I think there's a better refactoring down in pkg/model
// But to get a minimal test up and running, here we go!
// TODO: non text completion
func (sc SpecificConfig[RequestModel]) ToModelOptions() []llama.ModelOption {

	llamaOpts := []llama.ModelOption{}

	switch req := sc.GetRequestDefaults().(type) {
	case CreateCompletionRequest:
	case CreateChatCompletionRequest:
		if req.XLocalaiExtensions.F16 != nil && *(req.XLocalaiExtensions.F16) {
			llamaOpts = append(llamaOpts, llama.EnableF16Memory)
		}

		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			llamaOpts = append(llamaOpts, llama.SetContext(*req.MaxTokens)) // todo is this right?
		}

		// TODO DO MORE!

	}
	// Code to Port:

	// if c.Embeddings {
	// 	llamaOpts = append(llamaOpts, llama.EnableEmbeddings)
	// }

	// if c.NGPULayers != 0 {
	// 	llamaOpts = append(llamaOpts, llama.SetGPULayers(c.NGPULayers))
	// }

	return llamaOpts
}

func (sc SpecificConfig[RequestModel]) ToPredictOptions() []llama.PredictOption {
	llamaOpts := []llama.PredictOption{}

	switch req := sc.GetRequestDefaults().(type) {
	case CreateCompletionRequest:
	case CreateChatCompletionRequest:

		if req.Temperature != nil {
			llamaOpts = append(llamaOpts, llama.SetTemperature(float64(*req.Temperature))) // Oh boy. TODO Investigate. This is why I'm doing this.
		}

		if req.TopP != nil {
			llamaOpts = append(llamaOpts, llama.SetTopP(float64(*req.TopP))) // CAST
		}

		if req.MaxTokens != nil {
			llamaOpts = append(llamaOpts, llama.SetTokens(*req.MaxTokens))
		}

		if req.FrequencyPenalty != nil {
			llamaOpts = append(llamaOpts, llama.SetPenalty(float64(*req.FrequencyPenalty))) // CAST
		}

		if stop0, err := req.Stop.AsCreateChatCompletionRequestStop0(); err == nil {
			llamaOpts = append(llamaOpts, llama.SetStopWords(stop0))
		}

		if stop1, err := req.Stop.AsCreateChatCompletionRequestStop1(); err == nil && len(stop1) > 0 {
			llamaOpts = append(llamaOpts, llama.SetStopWords(stop1...))
		}

		if req.XLocalaiExtensions != nil {

			if req.XLocalaiExtensions.TopK != nil {
				llamaOpts = append(llamaOpts, llama.SetTopK(*req.XLocalaiExtensions.TopK))
			}

			if req.XLocalaiExtensions.F16 != nil && *(req.XLocalaiExtensions.F16) {
				llamaOpts = append(llamaOpts, llama.EnableF16KV)
			}

			if req.XLocalaiExtensions.Seed != nil {
				llamaOpts = append(llamaOpts, llama.SetSeed(*req.XLocalaiExtensions.Seed))
			}

			if req.XLocalaiExtensions.IgnoreEos != nil && *(req.XLocalaiExtensions.IgnoreEos) {
				llamaOpts = append(llamaOpts, llama.IgnoreEOS)
			}

			if req.XLocalaiExtensions.Debug != nil && *(req.XLocalaiExtensions.Debug) {
				llamaOpts = append(llamaOpts, llama.Debug)
			}

			if req.XLocalaiExtensions.Mirostat != nil {
				llamaOpts = append(llamaOpts, llama.SetMirostat(*req.XLocalaiExtensions.Mirostat))
			}

			if req.XLocalaiExtensions.MirostatEta != nil {
				llamaOpts = append(llamaOpts, llama.SetMirostatETA(*req.XLocalaiExtensions.MirostatEta))
			}

			if req.XLocalaiExtensions.MirostatTau != nil {
				llamaOpts = append(llamaOpts, llama.SetMirostatTAU(*req.XLocalaiExtensions.MirostatTau))
			}

			if req.XLocalaiExtensions.Keep != nil {
				llamaOpts = append(llamaOpts, llama.SetNKeep(*req.XLocalaiExtensions.Keep))
			}

			if req.XLocalaiExtensions.Batch != nil && *(req.XLocalaiExtensions.Batch) != 0 {
				llamaOpts = append(llamaOpts, llama.SetBatch(*req.XLocalaiExtensions.Batch))
			}

		}

	}

	// CODE TO PORT

	// predictOptions := []llama.PredictOption{

	// 	llama.SetThreads(c.Threads),
	// }

	// if c.PromptCacheAll {
	// 	predictOptions = append(predictOptions, llama.EnablePromptCacheAll)
	// }

	// if c.PromptCachePath != "" {
	// 	// Create parent directory
	// 	p := filepath.Join(modelPath, c.PromptCachePath)
	// 	os.MkdirAll(filepath.Dir(p), 0755)
	// 	predictOptions = append(predictOptions, llama.SetPathPromptCache(p))
	// }

	return llamaOpts
}
