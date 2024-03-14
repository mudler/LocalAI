package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

type BackendConfigLoader struct {
	configs map[string]BackendConfig
	sync.Mutex
}

// Merged over from #1822, not entirely sure if it should stay or be subsumed into *appConfig based service refactors
// It's not too bad to convert one to the other by calling all existing setter functions...
//
//	but it'll be a pain to maintain that if we add more setters and forget to add elsewhere.
type ConfigLoaderOptions struct {
	debug            bool
	threads, ctxSize int
	f16              bool
}

func LoadOptionDebug(debug bool) ConfigLoaderOption {
	return func(o *ConfigLoaderOptions) {
		o.debug = debug
	}
}

func LoadOptionThreads(threads int) ConfigLoaderOption {
	return func(o *ConfigLoaderOptions) {
		o.threads = threads
	}
}

func LoadOptionContextSize(ctxSize int) ConfigLoaderOption {
	return func(o *ConfigLoaderOptions) {
		o.ctxSize = ctxSize
	}
}

func LoadOptionF16(f16 bool) ConfigLoaderOption {
	return func(o *ConfigLoaderOptions) {
		o.f16 = f16
	}
}

type ConfigLoaderOption func(*ConfigLoaderOptions)

func (lo *ConfigLoaderOptions) Apply(options ...ConfigLoaderOption) {
	for _, l := range options {
		l(lo)
	}
}

func NewBackendConfigLoader() *BackendConfigLoader {
	return &BackendConfigLoader{
		configs: make(map[string]BackendConfig),
	}
}

func (bcl *BackendConfigLoader) LoadBackendConfig(file string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := readBackendConfig(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	bcl.configs[c.Name] = *c
	return nil
}

func (bcl *BackendConfigLoader) GetBackendConfig(m string) (BackendConfig, bool) {
	bcl.Lock()
	defer bcl.Unlock()
	v, exists := bcl.configs[m]
	return v, exists
}

func (bcl *BackendConfigLoader) GetAllBackendConfigs() []BackendConfig {
	bcl.Lock()
	defer bcl.Unlock()
	var res []BackendConfig
	for _, v := range bcl.configs {
		res = append(res, v)
	}
	return res
}

func (bcl *BackendConfigLoader) ListBackendConfigs() []string {
	bcl.Lock()
	defer bcl.Unlock()
	var res []string
	for k := range bcl.configs {
		res = append(res, k)
	}
	return res
}

// Preload prepare models if they are not local but url or huggingface repositories
func (bcl *BackendConfigLoader) Preload(modelPath string) error {
	bcl.Lock()
	defer bcl.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	log.Info().Msgf("Preloading models from %s", modelPath)

	renderMode := "dark"
	if os.Getenv("COLOR") != "" {
		renderMode = os.Getenv("COLOR")
	}

	glamText := func(t string) {
		out, err := glamour.Render(t, renderMode)
		if err == nil && os.Getenv("NO_COLOR") == "" {
			fmt.Println(out)
		} else {
			fmt.Println(t)
		}
	}

	for i, config := range bcl.configs {

		// Download files and verify their SHA
		for _, file := range config.DownloadFiles {
			log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

			if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
				return err
			}
			// Create file path
			filePath := filepath.Join(modelPath, file.Filename)

			if err := downloader.DownloadFile(file.URI, filePath, file.SHA256, status); err != nil {
				return err
			}
		}

		modelURL := config.PredictionOptions.Model
		modelURL = downloader.ConvertURL(modelURL)

		if downloader.LooksLikeURL(modelURL) {
			// md5 of model name
			md5Name := utils.MD5(modelURL)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); errors.Is(err, os.ErrNotExist) {
				err := downloader.DownloadFile(modelURL, filepath.Join(modelPath, md5Name), "", status)
				if err != nil {
					return err
				}
			}

			cc := bcl.configs[i]
			c := &cc
			c.PredictionOptions.Model = md5Name
			bcl.configs[i] = *c
		}
		if bcl.configs[i].Name != "" {
			glamText(fmt.Sprintf("**Model name**: _%s_", bcl.configs[i].Name))
		}
		if bcl.configs[i].Description != "" {
			//glamText("**Description**")
			glamText(bcl.configs[i].Description)
		}
		if bcl.configs[i].Usage != "" {
			//glamText("**Usage**")
			glamText(bcl.configs[i].Usage)
		}
	}
	return nil
}

func (bcl *BackendConfigLoader) LoadBackendConfigsFromPath(path string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	files := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
	}
	for _, file := range files {
		// Skip templates, YAML and .keep files
		if !strings.Contains(file.Name(), ".yaml") && !strings.Contains(file.Name(), ".yml") {
			continue
		}
		c, err := readBackendConfig(filepath.Join(path, file.Name()), opts...)
		if err == nil {
			bcl.configs[c.Name] = *c
		}
	}

	return nil
}

func (bcl *BackendConfigLoader) LoadBackendConfigFile(file string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := readBackendConfigFile(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		bcl.configs[cc.Name] = *cc
	}
	return nil
}

//////////

// Load a config file for a model
func (bcl *BackendConfigLoader) LoadBackendConfigFileByName(modelName string, modelPath string, opts ...ConfigLoaderOption) (*BackendConfig, error) {
	lo := &ConfigLoaderOptions{}
	lo.Apply(opts...)

	// Load a config file if present after the model name
	cfg := &BackendConfig{
		PredictionOptions: schema.PredictionOptions{
			Model: modelName,
		},
	}

	cfgExisting, exists := bcl.GetBackendConfig(modelName)
	if exists {
		cfg = &cfgExisting
	} else {
		// Load a config file if present after the model name
		modelConfig := filepath.Join(modelPath, modelName+".yaml")
		if _, err := os.Stat(modelConfig); err == nil {
			if err := bcl.LoadBackendConfig(modelConfig); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = bcl.GetBackendConfig(modelName)
			if exists {
				cfg = &cfgExisting
			}
		}
	}

	cfg.SetDefaults(lo.debug, lo.threads, lo.ctxSize, lo.f16)
	return cfg, nil
}

func readBackendConfigFile(file string, opts ...ConfigLoaderOption) ([]*BackendConfig, error) {
	lo := &ConfigLoaderOptions{}
	lo.Apply(opts...)

	c := &[]*BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	for _, cc := range *c {
		cc.SetDefaults(lo.debug, lo.threads, lo.ctxSize, lo.f16)
	}

	return *c, nil
}

func readBackendConfig(file string, opts ...ConfigLoaderOption) (*BackendConfig, error) {
	lo := &ConfigLoaderOptions{}
	lo.Apply(opts...)

	c := &BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	c.SetDefaults(lo.debug, lo.threads, lo.ctxSize, lo.f16)
	return c, nil
}

func (bcl *BackendConfigLoader) LoadBackendConfigForModelAndOpenAIRequest(modelFile string, input *schema.OpenAIRequest, appConfig *ApplicationConfig) (*BackendConfig, *schema.OpenAIRequest, error) {
	cfg, err := bcl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
		LoadOptionContextSize(appConfig.ContextSize),
		LoadOptionDebug(appConfig.Debug),
		LoadOptionF16(appConfig.F16),
		LoadOptionThreads(appConfig.Threads),
	)

	// Set the parameters for the language model prediction
	updateBackendConfigFromOpenAIRequest(cfg, input)

	return cfg, input, err
}

func updateBackendConfigFromOpenAIRequest(config *BackendConfig, input *schema.OpenAIRequest) {
	if input.Echo {
		config.Echo = input.Echo
	}
	if input.TopK != nil && *input.TopK != 0 {
		config.TopK = input.TopK
	}
	if input.TopP != nil && *input.TopP != 0 {
		config.TopP = input.TopP
	}

	if input.Backend != "" {
		config.Backend = input.Backend
	}

	if input.ClipSkip != 0 {
		config.Diffusers.ClipSkip = input.ClipSkip
	}

	if input.ModelBaseName != "" {
		config.AutoGPTQ.ModelBaseName = input.ModelBaseName
	}

	if input.NegativePromptScale != 0 {
		config.NegativePromptScale = input.NegativePromptScale
	}

	if input.UseFastTokenizer {
		config.UseFastTokenizer = input.UseFastTokenizer
	}

	if input.NegativePrompt != "" {
		config.NegativePrompt = input.NegativePrompt
	}

	if input.RopeFreqBase != 0 {
		config.RopeFreqBase = input.RopeFreqBase
	}

	if input.RopeFreqScale != 0 {
		config.RopeFreqScale = input.RopeFreqScale
	}

	if input.Grammar != "" {
		config.Grammar = input.Grammar
	}

	if input.Temperature != nil && *input.Temperature != 0 {
		config.Temperature = input.Temperature
	}

	if input.Maxtokens != nil && *input.Maxtokens != 0 {
		config.Maxtokens = input.Maxtokens
	}

	switch stop := input.Stop.(type) {
	case string:
		if stop != "" {
			config.StopWords = append(config.StopWords, stop)
		}
	case []interface{}:
		for _, pp := range stop {
			if s, ok := pp.(string); ok {
				config.StopWords = append(config.StopWords, s)
			}
		}
	}

	if len(input.Tools) > 0 {
		for _, tool := range input.Tools {
			input.Functions = append(input.Functions, tool.Function)
		}
	}

	if input.ToolsChoice != nil {
		var toolChoice grammar.Tool
		json.Unmarshal([]byte(input.ToolsChoice.(string)), &toolChoice)
		input.FunctionCall = map[string]interface{}{
			"name": toolChoice.Function.Name,
		}
	}

	// Decode each request's message content
	index := 0
	for i, m := range input.Messages {
		switch content := m.Content.(type) {
		case string:
			input.Messages[i].StringContent = content
		case []interface{}:
			dat, _ := json.Marshal(content)
			c := []schema.Content{}
			json.Unmarshal(dat, &c)
			for _, pp := range c {
				if pp.Type == "text" {
					input.Messages[i].StringContent = pp.Text
				} else if pp.Type == "image_url" {
					// Detect if pp.ImageURL is an URL, if it is download the image and encode it in base64:
					base64, err := utils.GetImageURLAsBase64(pp.ImageURL.URL)
					if err == nil {
						input.Messages[i].StringImages = append(input.Messages[i].StringImages, base64) // TODO: make sure that we only return base64 stuff
						// set a placeholder for each image
						input.Messages[i].StringContent = fmt.Sprintf("[img-%d]", index) + input.Messages[i].StringContent
						index++
					} else {
						fmt.Print("Failed encoding image", err)
					}
				}
			}
		}
	}

	if input.RepeatPenalty != 0 {
		config.RepeatPenalty = input.RepeatPenalty
	}

	if input.Keep != 0 {
		config.Keep = input.Keep
	}

	if input.Batch != 0 {
		config.Batch = input.Batch
	}

	// No longer needed - set elsewhere from ConfigLoaderOptions. Preserving for future removal TODO FIXME
	// if input.F16 {
	// 	config.F16 = input.F16
	// }

	if input.IgnoreEOS {
		config.IgnoreEOS = input.IgnoreEOS
	}

	if input.Seed != nil {
		config.Seed = input.Seed
	}

	// No longer needed - set elsewhere via config file only now, I believe? Preserving for future removal TODO FIXME
	// if input.TopP != nil && *input.Mirostat != 0 {
	// 	config.LLMConfig.Mirostat = input.Mirostat
	// }

	// if input.MirostatETA != 0 {
	// 	config.LLMConfig.MirostatETA = input.MirostatETA
	// }

	// if input.MirostatTAU != 0 {
	// 	config.LLMConfig.MirostatTAU = input.MirostatTAU
	// }

	if input.TypicalP != 0 {
		config.TypicalP = input.TypicalP
	}

	switch inputs := input.Input.(type) {
	case string:
		if inputs != "" {
			config.InputStrings = append(config.InputStrings, inputs)
		}
	case []interface{}:
		for _, pp := range inputs {
			switch i := pp.(type) {
			case string:
				config.InputStrings = append(config.InputStrings, i)
			case []interface{}:
				tokens := []int{}
				for _, ii := range i {
					tokens = append(tokens, int(ii.(float64)))
				}
				config.InputToken = append(config.InputToken, tokens)
			}
		}
	}

	// Can be either a string or an object
	switch fnc := input.FunctionCall.(type) {
	case string:
		if fnc != "" {
			config.SetFunctionCallString(fnc)
		}
	case map[string]interface{}:
		var name string
		n, exists := fnc["name"]
		if exists {
			nn, e := n.(string)
			if e {
				name = nn
			}
		}
		config.SetFunctionCallNameString(name)
	}

	switch p := input.Prompt.(type) {
	case string:
		config.PromptStrings = append(config.PromptStrings, p)
	case []interface{}:
		for _, pp := range p {
			if s, ok := pp.(string); ok {
				config.PromptStrings = append(config.PromptStrings, s)
			}
		}
	}
}
