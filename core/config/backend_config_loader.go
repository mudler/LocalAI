package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	sort.SliceStable(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})
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

	cfg.SetDefaults(opts...)
	return cfg, nil
}

func readBackendConfigFile(file string, opts ...ConfigLoaderOption) ([]*BackendConfig, error) {
	c := &[]*BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	for _, cc := range *c {
		cc.SetDefaults(opts...)
	}

	return *c, nil
}

func readBackendConfig(file string, opts ...ConfigLoaderOption) (*BackendConfig, error) {
	c := &BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	c.SetDefaults(opts...)
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

func updateBackendConfigFromOpenAIRequest(bc *BackendConfig, request *schema.OpenAIRequest) {
	if request.Echo {
		bc.Echo = request.Echo
	}
	if request.TopK != nil && *request.TopK != 0 {
		bc.TopK = request.TopK
	}
	if request.TopP != nil && *request.TopP != 0 {
		bc.TopP = request.TopP
	}

	if request.Backend != "" {
		bc.Backend = request.Backend
	}

	if request.ClipSkip != 0 {
		bc.Diffusers.ClipSkip = request.ClipSkip
	}

	if request.ModelBaseName != "" {
		bc.AutoGPTQ.ModelBaseName = request.ModelBaseName
	}

	if request.NegativePromptScale != 0 {
		bc.NegativePromptScale = request.NegativePromptScale
	}

	if request.UseFastTokenizer {
		bc.UseFastTokenizer = request.UseFastTokenizer
	}

	if request.NegativePrompt != "" {
		bc.NegativePrompt = request.NegativePrompt
	}

	if request.RopeFreqBase != 0 {
		bc.RopeFreqBase = request.RopeFreqBase
	}

	if request.RopeFreqScale != 0 {
		bc.RopeFreqScale = request.RopeFreqScale
	}

	if request.Grammar != "" {
		bc.Grammar = request.Grammar
	}

	if request.Temperature != nil && *request.Temperature != 0 {
		bc.Temperature = request.Temperature
	}

	if request.Maxtokens != nil && *request.Maxtokens != 0 {
		bc.Maxtokens = request.Maxtokens
	}

	switch stop := request.Stop.(type) {
	case string:
		if stop != "" {
			bc.StopWords = append(bc.StopWords, stop)
		}
	case []interface{}:
		for _, pp := range stop {
			if s, ok := pp.(string); ok {
				bc.StopWords = append(bc.StopWords, s)
			}
		}
	}

	if len(request.Tools) > 0 {
		for _, tool := range request.Tools {
			request.Functions = append(request.Functions, tool.Function)
		}
	}

	if request.ToolsChoice != nil {
		var toolChoice grammar.Tool
		switch content := request.ToolsChoice.(type) {
		case string:
			_ = json.Unmarshal([]byte(content), &toolChoice)
		case map[string]interface{}:
			dat, _ := json.Marshal(content)
			_ = json.Unmarshal(dat, &toolChoice)
		}
		request.FunctionCall = map[string]interface{}{
			"name": toolChoice.Function.Name,
		}
	}

	// Decode each request's message content
	index := 0
	for i, m := range request.Messages {
		switch content := m.Content.(type) {
		case string:
			request.Messages[i].StringContent = content
		case []interface{}:
			dat, _ := json.Marshal(content)
			c := []schema.Content{}
			json.Unmarshal(dat, &c)
			for _, pp := range c {
				if pp.Type == "text" {
					request.Messages[i].StringContent = pp.Text
				} else if pp.Type == "image_url" {
					// Detect if pp.ImageURL is an URL, if it is download the image and encode it in base64:
					base64, err := utils.GetImageURLAsBase64(pp.ImageURL.URL)
					if err == nil {
						request.Messages[i].StringImages = append(request.Messages[i].StringImages, base64) // TODO: make sure that we only return base64 stuff
						// set a placeholder for each image
						request.Messages[i].StringContent = fmt.Sprintf("[img-%d]", index) + request.Messages[i].StringContent
						index++
					} else {
						fmt.Print("Failed encoding image", err)
					}
				}
			}
		}
	}

	if request.RepeatPenalty != 0 {
		bc.RepeatPenalty = request.RepeatPenalty
	}

	if request.FrequencyPenalty != 0 {
		bc.FrequencyPenalty = request.FrequencyPenalty
	}

	if request.PresencePenalty != 0 {
		bc.PresencePenalty = request.PresencePenalty
	}

	if request.Keep != 0 {
		bc.Keep = request.Keep
	}

	if request.Batch != 0 {
		bc.Batch = request.Batch
	}

	if request.IgnoreEOS {
		bc.IgnoreEOS = request.IgnoreEOS
	}

	if request.Seed != nil {
		bc.Seed = request.Seed
	}

	if request.TypicalP != nil {
		bc.TypicalP = request.TypicalP
	}

	switch inputs := request.Input.(type) {
	case string:
		if inputs != "" {
			bc.InputStrings = append(bc.InputStrings, inputs)
		}
	case []interface{}:
		for _, pp := range inputs {
			switch i := pp.(type) {
			case string:
				bc.InputStrings = append(bc.InputStrings, i)
			case []interface{}:
				tokens := []int{}
				for _, ii := range i {
					tokens = append(tokens, int(ii.(float64)))
				}
				bc.InputToken = append(bc.InputToken, tokens)
			}
		}
	}

	// Can be either a string or an object
	switch fnc := request.FunctionCall.(type) {
	case string:
		if fnc != "" {
			bc.SetFunctionCallString(fnc)
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
		bc.SetFunctionCallNameString(name)
	}

	switch p := request.Prompt.(type) {
	case string:
		bc.PromptStrings = append(bc.PromptStrings, p)
	case []interface{}:
		for _, pp := range p {
			if s, ok := pp.(string); ok {
				bc.PromptStrings = append(bc.PromptStrings, s)
			}
		}
	}
}
