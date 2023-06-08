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
