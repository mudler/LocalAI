package functions

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/functions/grammars"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// @Description GrammarConfig contains configuration for grammar parsing
type GrammarConfig struct {
	// ParallelCalls enables the LLM to return multiple function calls in the same response
	ParallelCalls bool `yaml:"parallel_calls,omitempty" json:"parallel_calls,omitempty"`

	DisableParallelNewLines bool `yaml:"disable_parallel_new_lines,omitempty" json:"disable_parallel_new_lines,omitempty"`

	// MixedMode enables the LLM to return strings and not only JSON objects
	// This is useful for models to not constraining returning only JSON and also messages back to the user
	MixedMode bool `yaml:"mixed_mode,omitempty" json:"mixed_mode,omitempty"`

	// NoMixedFreeString disables the mixed mode for free strings
	// In this way if the LLM selects a free string, it won't be mixed necessarily with JSON objects.
	// For example, if enabled the LLM or returns a JSON object or a free string, but not a mix of both
	// If disabled(default): the LLM can return a JSON object surrounded by free strings (e.g. `this is the JSON result: { "bar": "baz" } for your question`). This forces the LLM to return at least a JSON object, but its not going to be strict
	NoMixedFreeString bool `yaml:"no_mixed_free_string,omitempty" json:"no_mixed_free_string,omitempty"`

	// NoGrammar disables the grammar parsing and parses the responses directly from the LLM
	NoGrammar bool `yaml:"disable,omitempty" json:"disable,omitempty"`

	// Prefix is the suffix to append to the grammar when being generated
	// This is useful when models prepend a tag before returning JSON
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`

	// ExpectStringsAfterJSON enables mixed string suffix
	ExpectStringsAfterJSON bool `yaml:"expect_strings_after_json,omitempty" json:"expect_strings_after_json,omitempty"`

	// PropOrder selects what order to print properties
	// for instance name,arguments will make print { "name": "foo", "arguments": { "bar": "baz" } }
	// instead of { "arguments": { "bar": "baz" }, "name": "foo" }
	PropOrder string `yaml:"properties_order,omitempty" json:"properties_order,omitempty"`

	// SchemaType can be configured to use a specific schema type to force the grammar
	// available : json, llama3.1
	SchemaType string `yaml:"schema_type,omitempty" json:"schema_type,omitempty"`

	GrammarTriggers []GrammarTrigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`
}

// @Description GrammarTrigger defines a trigger word for grammar parsing
type GrammarTrigger struct {
	// Trigger is the string that triggers the grammar
	Word string `yaml:"word,omitempty" json:"word,omitempty"`
}

// @Description FunctionsConfig is the configuration for the tool/function call.
// It includes setting to map the function name and arguments from the response
// and, for instance, also if processing the requests with BNF grammars.
type FunctionsConfig struct {
	// DisableNoAction disables the "no action" tool
	// By default we inject a tool that does nothing and is used to return an answer from the LLM
	DisableNoAction bool `yaml:"disable_no_action,omitempty" json:"disable_no_action,omitempty"`

	// Grammar is the configuration for the grammar
	GrammarConfig GrammarConfig `yaml:"grammar,omitempty" json:"grammar,omitempty"`

	// NoActionFunctionName is the name of the function that does nothing. It defaults to "answer"
	NoActionFunctionName string `yaml:"no_action_function_name,omitempty" json:"no_action_function_name,omitempty"`

	// NoActionDescriptionName is the name of the function that returns the description of the no action function
	NoActionDescriptionName string `yaml:"no_action_description_name,omitempty" json:"no_action_description_name,omitempty"`

	// ResponseRegex is a named regex to extract the function name and arguments from the response
	ResponseRegex []string `yaml:"response_regex,omitempty" json:"response_regex,omitempty"`

	// JSONRegexMatch is a regex to extract the JSON object from the response
	JSONRegexMatch []string `yaml:"json_regex_match,omitempty" json:"json_regex_match,omitempty"`

	// ArgumentRegex is a named regex to extract the arguments from the response. Use ArgumentRegexKey and ArgumentRegexValue to set the names of the named regex for key and value of the arguments.
	ArgumentRegex []string `yaml:"argument_regex,omitempty" json:"argument_regex,omitempty"`
	// ArgumentRegex named regex names for key and value extractions. default: key and value
	ArgumentRegexKey   string `yaml:"argument_regex_key_name,omitempty" json:"argument_regex_key_name,omitempty"`     // default: key
	ArgumentRegexValue string `yaml:"argument_regex_value_name,omitempty" json:"argument_regex_value_name,omitempty"` // default: value

	// ReplaceFunctionResults allow to replace strings in the results before parsing them
	ReplaceFunctionResults []ReplaceResult `yaml:"replace_function_results,omitempty" json:"replace_function_results,omitempty"`

	// ReplaceLLMResult allow to replace strings in the results before parsing them
	ReplaceLLMResult []ReplaceResult `yaml:"replace_llm_results,omitempty" json:"replace_llm_results,omitempty"`

	// CaptureLLMResult is a regex to extract a string from the LLM response
	// that is used as return string when using tools.
	// This is useful for e.g. if the LLM outputs a reasoning and we want to get the reasoning as a string back
	CaptureLLMResult []string `yaml:"capture_llm_results,omitempty" json:"capture_llm_results,omitempty"`

	// FunctionName enable the LLM to return { "name": "function_name", "arguments": { "arg1": "value1", "arg2": "value2" } }
	// instead of { "function": "function_name", "arguments": { "arg1": "value1", "arg2": "value2" } }.
	// This might be useful for certain models trained with the function name as the first token.
	FunctionNameKey      string `yaml:"function_name_key,omitempty" json:"function_name_key,omitempty"`
	FunctionArgumentsKey string `yaml:"function_arguments_key,omitempty" json:"function_arguments_key,omitempty"`

	// XMLFormatPreset is an optional preset format name to force (e.g., "qwen3-coder", "glm-4.5", "minimax-m2")
	// If empty, auto-detection will try all formats
	XMLFormatPreset string `yaml:"xml_format_preset,omitempty" json:"xml_format_preset,omitempty"`
	// XMLFormat is an optional custom XML format configuration
	// If set, only this format will be tried (overrides XMLFormatPreset)
	XMLFormat *XMLToolCallFormat `yaml:"xml_format,omitempty" json:"xml_format,omitempty"`
}

// @Description ReplaceResult defines a key-value replacement for function results
type ReplaceResult struct {
	Key   string `yaml:"key,omitempty" json:"key,omitempty"`
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
}

// @Description XMLToolCallFormat defines the structure for parsing XML-style tool calls
// This mirrors llama.cpp's xml_tool_call_format structure
type XMLToolCallFormat struct {
	// ScopeStart is the optional wrapper start tag (e.g., "<minimax:tool_call>")
	ScopeStart string `yaml:"scope_start,omitempty" json:"scope_start,omitempty"`
	// ToolStart is the tool call start tag (e.g., "<tool_call>", "<invoke name=\"")
	ToolStart string `yaml:"tool_start,omitempty" json:"tool_start,omitempty"`
	// ToolSep is the separator after tool name (e.g., ">", "\">")
	ToolSep string `yaml:"tool_sep,omitempty" json:"tool_sep,omitempty"`
	// KeyStart is the parameter key start tag (e.g., "<parameter=", "<arg_key>")
	KeyStart string `yaml:"key_start,omitempty" json:"key_start,omitempty"`
	// KeyValSep is the separator between key and value (e.g., ">", "</arg_key>")
	KeyValSep string `yaml:"key_val_sep,omitempty" json:"key_val_sep,omitempty"`
	// ValEnd is the parameter value end tag (e.g., "</parameter>", "</arg_value>")
	ValEnd string `yaml:"val_end,omitempty" json:"val_end,omitempty"`
	// ToolEnd is the tool call end tag (e.g., "</tool_call>", "</invoke>")
	ToolEnd string `yaml:"tool_end,omitempty" json:"tool_end,omitempty"`
	// ScopeEnd is the optional wrapper end tag (e.g., "</minimax:tool_call>")
	ScopeEnd string `yaml:"scope_end,omitempty" json:"scope_end,omitempty"`
	// KeyValSep2 is the optional second separator (for GLM 4.5 format: "</arg_key>\n<arg_value>")
	KeyValSep2 *string `yaml:"key_val_sep2,omitempty" json:"key_val_sep2,omitempty"`
	// RawArgVal indicates whether to treat values as raw strings (true) vs JSON (false), nil means both allowed
	RawArgVal *bool `yaml:"raw_argval,omitempty" json:"raw_argval,omitempty"`
	// LastValEnd is the alternative value end for last parameter
	LastValEnd *string `yaml:"last_val_end,omitempty" json:"last_val_end,omitempty"`
	// LastToolEnd is the alternative tool end for last tool call
	LastToolEnd *string `yaml:"last_tool_end,omitempty" json:"last_tool_end,omitempty"`
	// TrimRawArgVal indicates whether to trim whitespace from raw values
	TrimRawArgVal bool `yaml:"trim_raw_argval,omitempty" json:"trim_raw_argval,omitempty"`
}

type FuncCallResults struct {
	Name      string
	Arguments string
}

func (g FunctionsConfig) GrammarOptions() []func(o *grammars.GrammarOption) {
	opts := []func(o *grammars.GrammarOption){}
	if g.GrammarConfig.MixedMode {
		opts = append(opts, grammars.EnableMaybeString)
	}
	if g.GrammarConfig.ParallelCalls {
		opts = append(opts, grammars.EnableMaybeArray)
	}
	if g.GrammarConfig.DisableParallelNewLines {
		opts = append(opts, grammars.DisableParallelNewLines)
	}
	if g.GrammarConfig.Prefix != "" {
		opts = append(opts, grammars.SetPrefix(g.GrammarConfig.Prefix))
	}
	if g.GrammarConfig.NoMixedFreeString {
		opts = append(opts, grammars.NoMixedFreeString)
	}
	if g.GrammarConfig.ExpectStringsAfterJSON {
		opts = append(opts, grammars.ExpectStringsAfterJSON)
	}

	if g.GrammarConfig.SchemaType != "" {
		opts = append(opts, grammars.WithSchemaType(grammars.NewType(g.GrammarConfig.SchemaType)))
	}

	if g.FunctionNameKey != "" {
		opts = append(opts, grammars.WithFunctionName(g.FunctionNameKey))
	}

	opts = append(opts, grammars.SetPropOrder(g.GrammarConfig.PropOrder))
	return opts
}

func CleanupLLMResult(llmresult string, functionConfig FunctionsConfig) string {
	xlog.Debug("LLM result", "result", llmresult)

	for _, item := range functionConfig.ReplaceLLMResult {
		k, v := item.Key, item.Value
		xlog.Debug("Replacing", "key", k, "value", v)
		re := regexp.MustCompile(k)
		llmresult = re.ReplaceAllString(llmresult, v)
	}
	xlog.Debug("LLM result(processed)", "result", llmresult)

	return llmresult
}

func ParseTextContent(llmresult string, functionConfig FunctionsConfig) string {
	xlog.Debug("ParseTextContent", "result", llmresult)
	xlog.Debug("CaptureLLMResult", "config", functionConfig.CaptureLLMResult)

	for _, r := range functionConfig.CaptureLLMResult {
		// We use a regex to extract the JSON object from the response
		var respRegex = regexp.MustCompile(r)
		match := respRegex.FindStringSubmatch(llmresult)
		if len(match) >= 1 {
			m := strings.TrimSpace(match[1])
			return m
		}
	}

	return ""
}

// ParseJSON is a function that parses a JSON string that might contain multiple JSON objects
// and syntax errors in between by shifting the offset
// This for e.g. allow to parse
// { "foo": "bar" } invalid { "baz": "qux" }
// into
// [ { "foo": "bar" }, { "baz": "qux" } ]
// Credits to Michael Yang (https://github.com/mxyng) for the original implementation
// This is a slightly reworked version, improved for readability and error handling
func ParseJSON(s string) ([]map[string]any, error) {
	var objs []map[string]any
	offset := 0

	for offset < len(s) {
		var obj map[string]any
		decoder := json.NewDecoder(strings.NewReader(s[offset:]))

		err := decoder.Decode(&obj)
		switch {
		case errors.Is(err, io.EOF):
			return objs, nil
		case err == nil:
			offset += int(decoder.InputOffset())
			objs = append(objs, obj)
		default: // handle the error type
			var syntaxErr *json.SyntaxError
			var unmarshalTypeErr *json.UnmarshalTypeError

			switch {
			case errors.As(err, &syntaxErr):
				offset += int(syntaxErr.Offset)
			case errors.As(err, &unmarshalTypeErr):
				offset += int(unmarshalTypeErr.Offset)
			default:
				return objs, err
			}
		}
	}

	return objs, nil
}

// getXMLFormatPreset returns a preset XML format by name, or nil if not found
func getXMLFormatPreset(name string) *XMLToolCallFormat {
	formats := getAllXMLFormats()
	for _, format := range formats {
		if format.name == name {
			return format.format
		}
	}
	return nil
}

// xmlFormatPreset holds a preset format with its name
type xmlFormatPreset struct {
	name   string
	format *XMLToolCallFormat
}

// getAllXMLFormats returns all preset XML formats matching llama.cpp's formats
func getAllXMLFormats() []xmlFormatPreset {
	falseVal := false
	commaSpace := ", "
	emptyValEnd := ""

	return []xmlFormatPreset{
		{
			name: "functionary",
			format: &XMLToolCallFormat{
				ScopeStart: "",
				ToolStart:  "<function=",
				ToolSep:    ">",
				KeyStart:   "", // Parameters are JSON, not XML tags
				KeyValSep:  "",
				ValEnd:     "",
				ToolEnd:    "</function>",
				ScopeEnd:   "",
				RawArgVal:  &falseVal, // JSON only
			},
		},
		{
			name: "qwen3-coder",
			format: &XMLToolCallFormat{
				ScopeStart:    "<tool_call>",
				ToolStart:     "<function=",
				ToolSep:       ">",
				KeyStart:      "<parameter=",
				KeyValSep:     ">",
				ValEnd:        "</parameter>",
				ToolEnd:       "</function>",
				ScopeEnd:      "</tool_call>",
				TrimRawArgVal: true,
			},
		},
		{
			name: "glm-4.5",
			format: &XMLToolCallFormat{
				ScopeStart: "",
				ToolStart:  "<tool_call>",
				ToolSep:    "",
				KeyStart:   "<arg_key>",
				KeyValSep:  "</arg_key>",
				KeyValSep2: func() *string { s := "<arg_value>"; return &s }(),
				ValEnd:     "</arg_value>",
				ToolEnd:    "</tool_call>",
				ScopeEnd:   "",
			},
		},
		{
			name: "minimax-m2",
			format: &XMLToolCallFormat{
				ScopeStart: "<minimax:tool_call>",
				ToolStart:  "<invoke name=\"",
				ToolSep:    "\">",
				KeyStart:   "<parameter name=\"",
				KeyValSep:  "\">",
				ValEnd:     "</parameter>",
				ToolEnd:    "</invoke>",
				ScopeEnd:   "</minimax:tool_call>",
			},
		},
		{
			name: "kimi-k2",
			format: &XMLToolCallFormat{
				ScopeStart: "<|tool_calls_section_begin|>",
				ToolStart:  "<|tool_call_begin|>",
				ToolSep:    "<|tool_call_argument_begin|>{",
				KeyStart:   "\"",
				KeyValSep:  "\":",
				ValEnd:     ",",
				ToolEnd:    "}<|tool_call_end|>",
				ScopeEnd:   "<|tool_calls_section_end|>",
				LastValEnd: &emptyValEnd,
				RawArgVal:  &falseVal,
			},
		},
		{
			name: "apriel-1.5",
			format: &XMLToolCallFormat{
				ScopeStart:  "<tool_calls>[",
				ToolStart:   "{\"name\": \"",
				ToolSep:     "\", \"arguments\": {",
				KeyStart:    "\"",
				KeyValSep:   "\": ",
				ValEnd:      commaSpace,
				ToolEnd:     "}, ",
				ScopeEnd:    "]</tool_calls>",
				LastValEnd:  &emptyValEnd,
				LastToolEnd: func() *string { s := "}"; return &s }(),
				RawArgVal:   &falseVal,
			},
		},
		{
			name: "xiaomi-mimo",
			format: &XMLToolCallFormat{
				ScopeStart: "",
				ToolStart:  "<tool_call>\n{\"name\": \"",
				ToolSep:    "\", \"arguments\": {",
				KeyStart:   "\"",
				KeyValSep:  "\": ",
				ValEnd:     commaSpace,
				ToolEnd:    "}\n</tool_call>",
				ScopeEnd:   "",
				LastValEnd: &emptyValEnd,
				RawArgVal:  &falseVal,
			},
		},
	}
}

// parseXMLAutoDetect tries all preset formats in sequence and returns results from the first one that succeeds
func parseXMLAutoDetect(s string) ([]FuncCallResults, error) {
	formats := getAllXMLFormats()
	for _, preset := range formats {
		results, err := parseXMLWithFormat(s, preset.format)
		if err == nil && len(results) > 0 {
			xlog.Debug("XML auto-detection succeeded", "format", preset.name, "count", len(results))
			return results, nil
		}
	}
	return nil, nil
}

// ParseXML is a function that parses XML-style tool calls from a string that might contain
// text and valid XML tool calls. If format is nil, it will auto-detect by trying all formats.
// Returns a slice of FuncCallResults with function names and JSON-encoded arguments.
func ParseXML(s string, format *XMLToolCallFormat) ([]FuncCallResults, error) {
	if format == nil {
		return parseXMLAutoDetect(s)
	}
	return parseXMLWithFormat(s, format)
}

// parseXMLWithFormat parses XML tool calls using a specific format configuration
func parseXMLWithFormat(s string, format *XMLToolCallFormat) ([]FuncCallResults, error) {
	var results []FuncCallResults

	// Handle Functionary format (JSON parameters inside XML tags)
	if format.KeyStart == "" && format.ToolStart == "<function=" {
		return parseFunctionaryFormat(s, format)
	}

	// Handle formats with JSON-like structure (Apriel-1.5, Xiaomi-MiMo)
	// Note: Kimi-K2 is NOT JSON-like - it uses standard XML format with JSON arguments
	if format.ToolStart != "" && strings.Contains(format.ToolStart, "{\"name\"") {
		return parseJSONLikeXMLFormat(s, format)
	}

	// Handle GLM 4.5 format specially (function name on separate line after <tool_call>)
	if format.ToolStart == "<tool_call>" && format.ToolSep == "" && format.KeyStart == "<arg_key>" {
		return parseGLM45Format(s, format)
	}

	// Build regex patterns from format configuration
	// Escape special regex characters in format strings
	escapeRegex := func(str string) string {
		return regexp.QuoteMeta(str)
	}

	// Build scope pattern (optional)
	// llama.cpp validates that only whitespace appears before scope_start
	var scopePattern *regexp.Regexp
	if format.ScopeStart != "" {
		// Match scope_start with optional whitespace before it, but validate it's only whitespace
		scopeRegex := `(?s)(\s*)` + escapeRegex(format.ScopeStart) + `\s*(.*?)\s*` + escapeRegex(format.ScopeEnd)
		scopePattern = regexp.MustCompile(scopeRegex)
	}

	// Build tool call patterns - try both primary and alternative tool_end
	var toolCallPatterns []*regexp.Regexp

	buildToolCallPattern := func(toolEnd string) string {
		toolCallRegex := `(?s)` + escapeRegex(format.ToolStart)
		if format.ToolSep != "" {
			// Tool name is between ToolStart and ToolSep
			// Use non-greedy match to capture function name until ToolSep
			// We can't use [^...] for multi-character strings, so use .*? with ToolSep
			toolCallRegex += `(.*?)` + escapeRegex(format.ToolSep)
			toolCallRegex += `(.*?)` + escapeRegex(toolEnd)
		} else {
			// Tool name might be on a separate line (GLM 4.5) or after ToolStart
			// For GLM 4.5: <tool_call>\nfunction_name\n<arg_key>...
			// Match function name until we find key_start or newline
			if format.KeyStart != "" {
				// Match whitespace/newlines, then function name, then whitespace, then key_start
				// We'll capture the function name and the rest (including key_start)
				toolCallRegex += `\s*([^\n` + escapeRegex(format.KeyStart) + `]+?)\s*` + escapeRegex(format.KeyStart) + `(.*?)` + escapeRegex(toolEnd)
			} else {
				// Match until newline
				toolCallRegex += `\s*([^\n]+)\s*(.*?)` + escapeRegex(toolEnd)
			}
		}
		return toolCallRegex
	}

	// Primary pattern with tool_end
	toolCallPatterns = append(toolCallPatterns, regexp.MustCompile(buildToolCallPattern(format.ToolEnd)))
	// Alternative pattern with last_tool_end if specified
	if format.LastToolEnd != nil && *format.LastToolEnd != "" {
		toolCallPatterns = append(toolCallPatterns, regexp.MustCompile(buildToolCallPattern(*format.LastToolEnd)))
	}

	// Extract content to search in
	searchContent := s
	if scopePattern != nil {
		scopeMatches := scopePattern.FindAllStringSubmatch(s, -1)
		if len(scopeMatches) == 0 {
			// Scope not found, try parsing without scope (scope is optional)
			searchContent = s
		} else {
			// Process each scope match separately
			for _, scopeMatch := range scopeMatches {
				if len(scopeMatch) >= 3 {
					// scopeMatch[1] is the whitespace before scope_start (we validate it's only whitespace)
					// scopeMatch[2] is the content inside the scope
					prelude := scopeMatch[1]
					// Validate that prelude contains only whitespace (llama.cpp behavior)
					allWhitespace := true
					for _, r := range prelude {
						if !strings.ContainsRune(" \t\n\r", r) {
							allWhitespace = false
							break
						}
					}
					if !allWhitespace {
						// Non-whitespace before scope_start, skip this match
						continue
					}
					scopeContent := scopeMatch[2]
					// Find all tool calls within this scope - try both patterns
					var toolCallMatches [][]string
					for _, pattern := range toolCallPatterns {
						matches := pattern.FindAllStringSubmatch(scopeContent, -1)
						toolCallMatches = append(toolCallMatches, matches...)
					}
					for _, match := range toolCallMatches {
						if len(match) >= 3 {
							functionName := strings.TrimSpace(match[1])

							// Handle Kimi-K2 function name prefix stripping: "functions.name:index" -> "name"
							if strings.HasPrefix(functionName, "functions.") {
								// Remove "functions." prefix
								functionName = functionName[10:]
								// Remove ":index" suffix if present
								if idx := strings.LastIndex(functionName, ":"); idx != -1 {
									// Check if what follows ":" is all digits
									suffix := functionName[idx+1:]
									if len(suffix) > 0 {
										allDigits := true
										for _, r := range suffix {
											if r < '0' || r > '9' {
												allDigits = false
												break
											}
										}
										if allDigits {
											functionName = functionName[:idx]
										}
									}
								}
							}

							var functionContent string
							if format.ToolSep == "" && format.KeyStart != "" {
								// Content includes key_start, so prepend it
								functionContent = format.KeyStart + match[2]
							} else {
								functionContent = match[2]
							}

							// Parse parameters based on format
							args, err := parseXMLParametersWithFormat(functionContent, format)
							if err != nil {
								xlog.Debug("error parsing XML parameters", "error", err, "content", functionContent)
								continue
							}

							argsJSON, _ := json.Marshal(args)
							results = append(results, FuncCallResults{
								Name:      functionName,
								Arguments: string(argsJSON),
							})
						}
					}
				}
			}
			return results, nil
		}
	}

	// No scope, find all tool calls directly in the string - try both patterns
	var toolCallMatches [][]string
	for _, pattern := range toolCallPatterns {
		matches := pattern.FindAllStringSubmatch(searchContent, -1)
		toolCallMatches = append(toolCallMatches, matches...)
	}
	if len(toolCallMatches) == 0 {
		return nil, nil
	}

	// Process each tool call
	for _, match := range toolCallMatches {
		if len(match) < 3 {
			continue
		}
		functionName := strings.TrimSpace(match[1])

		// Handle Kimi-K2 function name prefix stripping: "functions.name:index" -> "name"
		if strings.HasPrefix(functionName, "functions.") {
			// Remove "functions." prefix
			functionName = functionName[10:]
			// Remove ":index" suffix if present
			if idx := strings.LastIndex(functionName, ":"); idx != -1 {
				// Check if what follows ":" is all digits
				suffix := functionName[idx+1:]
				if len(suffix) > 0 {
					allDigits := true
					for _, r := range suffix {
						if r < '0' || r > '9' {
							allDigits = false
							break
						}
					}
					if allDigits {
						functionName = functionName[:idx]
					}
				}
			}
		}

		var functionContent string
		if len(match) >= 3 {
			if format.ToolSep == "" && format.KeyStart != "" {
				// For GLM 4.5 format, match[2] contains the content starting from key_start
				functionContent = match[2]
			} else {
				functionContent = match[2]
			}
		}

		// Parse parameters based on format
		args, err := parseXMLParametersWithFormat(functionContent, format)
		if err != nil {
			xlog.Debug("error parsing XML parameters", "error", err, "content", functionContent)
			continue
		}

		argsJSON, _ := json.Marshal(args)
		results = append(results, FuncCallResults{
			Name:      functionName,
			Arguments: string(argsJSON),
		})
	}

	return results, nil
}

// parseGLM45Format handles GLM 4.5 format: <tool_call>\nfunction_name\n<arg_key>...</arg_key><arg_value>...</arg_value>...
func parseGLM45Format(s string, format *XMLToolCallFormat) ([]FuncCallResults, error) {
	var results []FuncCallResults

	// Pattern: <tool_call>\nfunction_name\n<arg_key>...</arg_key><arg_value>...</arg_value>...</tool_call>
	pattern := regexp.MustCompile(`(?s)<tool_call>\s*([^\n<]+)\s*(.*?)\s*</tool_call>`)
	matches := pattern.FindAllStringSubmatch(s, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			functionName := strings.TrimSpace(match[1])

			// Handle Kimi-K2 function name prefix stripping: "functions.name:index" -> "name"
			if strings.HasPrefix(functionName, "functions.") {
				// Remove "functions." prefix
				functionName = functionName[10:]
				// Remove ":index" suffix if present
				if idx := strings.LastIndex(functionName, ":"); idx != -1 {
					// Check if what follows ":" is all digits
					suffix := functionName[idx+1:]
					if len(suffix) > 0 {
						allDigits := true
						for _, r := range suffix {
							if r < '0' || r > '9' {
								allDigits = false
								break
							}
						}
						if allDigits {
							functionName = functionName[:idx]
						}
					}
				}
			}

			functionContent := match[2]

			// Parse parameters using GLM 4.5 format
			args, err := parseXMLParametersWithFormat(functionContent, format)
			if err != nil {
				xlog.Debug("error parsing GLM 4.5 parameters", "error", err, "content", functionContent)
				continue
			}

			argsJSON, _ := json.Marshal(args)
			results = append(results, FuncCallResults{
				Name:      functionName,
				Arguments: string(argsJSON),
			})
		}
	}

	return results, nil
}

// parseFunctionaryFormat handles Functionary format: <function=name>{"key": "value"}</function>
func parseFunctionaryFormat(s string, format *XMLToolCallFormat) ([]FuncCallResults, error) {
	var results []FuncCallResults

	// Pattern: <function=name>JSON</function>
	pattern := regexp.MustCompile(`(?s)<function=([^>]+)>(.*?)</function>`)
	matches := pattern.FindAllStringSubmatch(s, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			functionName := strings.TrimSpace(match[1])
			jsonContent := strings.TrimSpace(match[2])

			// Parse JSON content as arguments
			var args map[string]any
			if err := json.Unmarshal([]byte(jsonContent), &args); err != nil {
				xlog.Debug("error parsing Functionary JSON", "error", err, "content", jsonContent)
				continue
			}

			argsJSON, _ := json.Marshal(args)
			results = append(results, FuncCallResults{
				Name:      functionName,
				Arguments: string(argsJSON),
			})
		}
	}

	return results, nil
}

// parseJSONLikeXMLFormat handles formats like Apriel-1.5, Xiaomi-MiMo, Kimi-K2 that have JSON-like structure
func parseJSONLikeXMLFormat(s string, format *XMLToolCallFormat) ([]FuncCallResults, error) {
	var results []FuncCallResults

	// Build pattern to match the JSON-like structure
	escapeRegex := func(str string) string {
		return regexp.QuoteMeta(str)
	}

	// Pattern: scope_start + tool_start + name + tool_sep + arguments + tool_end + scope_end
	var pattern *regexp.Regexp
	if format.ScopeStart != "" {
		patternStr := `(?s)` + escapeRegex(format.ScopeStart) + `(.*?)` + escapeRegex(format.ScopeEnd)
		pattern = regexp.MustCompile(patternStr)
	} else {
		patternStr := `(?s)` + escapeRegex(format.ToolStart) + `([^"]+)"` + escapeRegex(format.ToolSep) + `(.*?)` + escapeRegex(format.ToolEnd)
		pattern = regexp.MustCompile(patternStr)
	}

	matches := pattern.FindAllStringSubmatch(s, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		// Extract JSON content
		jsonContent := match[1]
		if format.ScopeStart != "" {
			// Need to extract individual tool calls from the array
			// Pattern: {"name": "...", "arguments": {...}}
			toolPattern := regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"([^"]+)"\s*,\s*"arguments"\s*:\s*(\{.*?\})\s*\}`)
			toolMatches := toolPattern.FindAllStringSubmatch(jsonContent, -1)
			for _, toolMatch := range toolMatches {
				if len(toolMatch) >= 3 {
					functionName := strings.TrimSpace(toolMatch[1])
					argsJSON := toolMatch[2]
					results = append(results, FuncCallResults{
						Name:      functionName,
						Arguments: argsJSON,
					})
				}
			}
		} else {
			// Single tool call
			namePattern := regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
			nameMatch := namePattern.FindStringSubmatch(jsonContent)
			if len(nameMatch) >= 2 {
				functionName := strings.TrimSpace(nameMatch[1])
				argsPattern := regexp.MustCompile(`"arguments"\s*:\s*(\{.*\})`)
				argsMatch := argsPattern.FindStringSubmatch(jsonContent)
				argsJSON := "{}"
				if len(argsMatch) >= 2 {
					argsJSON = argsMatch[1]
				}
				results = append(results, FuncCallResults{
					Name:      functionName,
					Arguments: argsJSON,
				})
			}
		}
	}

	return results, nil
}

// parseXMLParametersWithFormat extracts parameters from XML content based on format configuration
func parseXMLParametersWithFormat(content string, format *XMLToolCallFormat) (map[string]any, error) {
	args := make(map[string]any)

	// Handle GLM 4.5 format: <arg_key>key</arg_key><arg_value>value</arg_value>
	if format.KeyValSep2 != nil && *format.KeyValSep2 == "<arg_value>" {
		return parseGLM45Parameters(content, format)
	}

	// Special case: If content is already valid JSON and format expects JSON (like Kimi-K2),
	// try to parse it as JSON first
	if format.KeyStart == "\"" && format.KeyValSep == "\":" && (format.RawArgVal == nil || !*format.RawArgVal) {
		// Try parsing as complete JSON object first
		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}") {
			var jsonArgs map[string]any
			if err := json.Unmarshal([]byte(content), &jsonArgs); err == nil {
				// Successfully parsed as JSON, return it
				return jsonArgs, nil
			}
		}
	}

	// Handle standard parameter format: <parameter=name>value</parameter> or <parameter name="name">value</parameter>
	if format.KeyStart != "" {
		return parseStandardParameters(content, format)
	}

	return args, nil
}

// parseGLM45Parameters handles GLM 4.5 format with <arg_key> and <arg_value> pairs
func parseGLM45Parameters(content string, format *XMLToolCallFormat) (map[string]any, error) {
	args := make(map[string]any)

	// Pattern: <arg_key>key</arg_key><arg_value>value</arg_value>
	pattern := regexp.MustCompile(`(?s)<arg_key>(.*?)</arg_key>\s*<arg_value>(.*?)</arg_value>`)
	matches := pattern.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			paramName := strings.TrimSpace(match[1])
			paramValue := strings.TrimSpace(match[2])
			args[paramName] = parseParameterValue(paramValue, format)
		}
	}

	return args, nil
}

// parseStandardParameters handles standard parameter formats
func parseStandardParameters(content string, format *XMLToolCallFormat) (map[string]any, error) {
	args := make(map[string]any)

	escapeRegex := func(str string) string {
		return regexp.QuoteMeta(str)
	}

	// Build parameter patterns - try both primary and alternative endings
	var parameterPatterns []*regexp.Regexp

	if strings.Contains(format.KeyStart, "=") {
		// Format: <parameter=name>value</parameter>
		patternStr := `(?s)` + escapeRegex(format.KeyStart) + `([^>]+)` + escapeRegex(format.KeyValSep) + `(.*?)` + escapeRegex(format.ValEnd)
		parameterPatterns = append(parameterPatterns, regexp.MustCompile(patternStr))
		// Add alternative ending if specified
		if format.LastValEnd != nil && *format.LastValEnd != "" {
			altPatternStr := `(?s)` + escapeRegex(format.KeyStart) + `([^>]+)` + escapeRegex(format.KeyValSep) + `(.*?)` + escapeRegex(*format.LastValEnd)
			parameterPatterns = append(parameterPatterns, regexp.MustCompile(altPatternStr))
		}
	} else if strings.Contains(format.KeyStart, "name=\"") {
		// Format: <parameter name="name">value</parameter>
		patternStr := `(?s)` + escapeRegex(format.KeyStart) + `([^"]+)"` + escapeRegex(format.KeyValSep) + `(.*?)` + escapeRegex(format.ValEnd)
		parameterPatterns = append(parameterPatterns, regexp.MustCompile(patternStr))
		// Add alternative ending if specified
		if format.LastValEnd != nil && *format.LastValEnd != "" {
			altPatternStr := `(?s)` + escapeRegex(format.KeyStart) + `([^"]+)"` + escapeRegex(format.KeyValSep) + `(.*?)` + escapeRegex(*format.LastValEnd)
			parameterPatterns = append(parameterPatterns, regexp.MustCompile(altPatternStr))
		}
	} else {
		// Fallback: try to match key_start...key_val_sep...val_end
		patternStr := `(?s)` + escapeRegex(format.KeyStart) + `([^` + escapeRegex(format.KeyValSep) + `]+)` + escapeRegex(format.KeyValSep)
		if format.KeyValSep2 != nil {
			patternStr += escapeRegex(*format.KeyValSep2)
		}
		patternStr += `(.*?)` + escapeRegex(format.ValEnd)
		parameterPatterns = append(parameterPatterns, regexp.MustCompile(patternStr))
		// Add alternative ending if specified
		if format.LastValEnd != nil && *format.LastValEnd != "" {
			altPatternStr := `(?s)` + escapeRegex(format.KeyStart) + `([^` + escapeRegex(format.KeyValSep) + `]+)` + escapeRegex(format.KeyValSep)
			if format.KeyValSep2 != nil {
				altPatternStr += escapeRegex(*format.KeyValSep2)
			}
			altPatternStr += `(.*?)` + escapeRegex(*format.LastValEnd)
			parameterPatterns = append(parameterPatterns, regexp.MustCompile(altPatternStr))
		}
	}

	// Track which parameters we've parsed to avoid duplicates
	// Use a map to store position info so we can handle last_val_end correctly
	type paramMatch struct {
		name     string
		value    string
		position int
	}
	var allMatches []paramMatch

	// Collect all matches from all patterns
	for _, pattern := range parameterPatterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) >= 3 {
				paramName := strings.TrimSpace(match[1])
				paramValue := strings.TrimSpace(match[2])
				// Find the position of this match in the content
				pos := strings.Index(content, match[0])
				if pos != -1 {
					allMatches = append(allMatches, paramMatch{
						name:     paramName,
						value:    paramValue,
						position: pos,
					})
				}
			}
		}
	}

	// Sort by position to process in order
	// If we have last_val_end, the last parameter should use it
	// For now, we'll use the first match for each parameter name (primary pattern takes precedence)
	seenParams := make(map[string]bool)
	for _, match := range allMatches {
		if !seenParams[match.name] {
			args[match.name] = parseParameterValue(match.value, format)
			seenParams[match.name] = true
		}
	}

	return args, nil
}

// parseParameterValue parses a parameter value based on format configuration
func parseParameterValue(paramValue string, format *XMLToolCallFormat) any {
	// Trim if configured
	if format.TrimRawArgVal {
		paramValue = strings.TrimSpace(paramValue)
	}

	// Handle raw_argval option
	if format.RawArgVal != nil {
		if *format.RawArgVal {
			// Raw string only
			return paramValue
		} else {
			// JSON only
			var jsonValue any
			if err := json.Unmarshal([]byte(paramValue), &jsonValue); err == nil {
				return jsonValue
			}
			// If JSON parsing fails, return as string anyway
			return paramValue
		}
	}

	// Default: try JSON, fallback to string
	var jsonValue any
	if err := json.Unmarshal([]byte(paramValue), &jsonValue); err != nil {
		// Not valid JSON, treat as string
		return paramValue
	}

	// If it's a JSON object or array, use the parsed value
	// Otherwise, if it's a primitive (number, bool, null), keep as string to preserve XML semantics
	switch jsonValue.(type) {
	case map[string]any, []any:
		// JSON object or array - use parsed value
		return jsonValue
	default:
		// Primitive JSON value (number, bool, null) - keep as string to match XML semantics
		return paramValue
	}
}

func ParseFunctionCall(llmresult string, functionConfig FunctionsConfig) []FuncCallResults {

	xlog.Debug("LLM result", "result", llmresult)

	for _, item := range functionConfig.ReplaceFunctionResults {
		k, v := item.Key, item.Value
		xlog.Debug("Replacing", "key", k, "value", v)
		re := regexp.MustCompile(k)
		llmresult = re.ReplaceAllString(llmresult, v)
	}
	xlog.Debug("LLM result(function cleanup)", "result", llmresult)

	functionNameKey := defaultFunctionNameKey
	functionArgumentsKey := defaultFunctionArgumentsKey
	if functionConfig.FunctionNameKey != "" {
		functionNameKey = functionConfig.FunctionNameKey
	}
	if functionConfig.FunctionArgumentsKey != "" {
		functionArgumentsKey = functionConfig.FunctionArgumentsKey
	}

	results := []FuncCallResults{}
	llmResults := []string{}

	returnResult := func(results []string) (result []FuncCallResults, e error) {
		// As we have to change the result before processing, we can't stream the answer token-by-token (yet?)
		result = make([]FuncCallResults, 0)

		for _, s := range results {
			var ss []map[string]any

			s = utils.EscapeNewLines(s)
			ss, err := ParseJSON(s)
			//err := json.Unmarshal([]byte(s), &ss)
			if err != nil {
				xlog.Debug("unable to unmarshal llm result in a single object or an array of JSON objects", "error", err, "escapedLLMResult", s)
			}

			xlog.Debug("Function return", "result", s, "parsed", ss)

			for _, s := range ss {
				// The grammar defines the function name as "function", while OpenAI returns "name"
				func_name, ok := s[functionNameKey]
				if !ok {
					continue
					//return result, fmt.Errorf("unable to find function name in result")
				}
				// Similarly, while here arguments is a map[string]interface{}, OpenAI actually want a stringified object
				args, ok := s[functionArgumentsKey] // arguments needs to be a string, but we return an object from the grammar result (TODO: fix)
				if !ok {
					continue
					//return result, fmt.Errorf("unable to find arguments in result")
				}
				d, _ := json.Marshal(args)
				funcName, ok := func_name.(string)
				if !ok {
					continue
					//return result, fmt.Errorf("unable to cast function name to string")
				}

				result = append(result, FuncCallResults{Name: funcName, Arguments: string(d)})
			}
		}

		return result, nil
	}

	// the response is a string that we have to parse
	result := make(map[string]string)
	if len(functionConfig.JSONRegexMatch) != 0 {
		for _, r := range functionConfig.JSONRegexMatch {
			// We use a regex to extract the JSON object from the response
			var respRegex = regexp.MustCompile(r)
			match := respRegex.FindAllStringSubmatch(llmresult, -1)
			var allMatches []string
			for _, m := range match {
				if len(m) > 1 {
					// we match the first group
					allMatches = append(allMatches, m[1])
				}
			}
			if len(allMatches) > 0 {
				llmResults = append(llmResults, allMatches...)
				break
			}
		}
	}

	if len(functionConfig.ResponseRegex) > 0 {
		// We use named regexes here to extract the function name and arguments
		// obviously, this expects the LLM to be stable and return correctly formatted JSON
		// TODO: optimize this and pre-compile it
		for _, r := range functionConfig.ResponseRegex {
			var respRegex = regexp.MustCompile(r)
			matches := respRegex.FindAllStringSubmatch(llmresult, -1)
			for _, match := range matches {
				for i, name := range respRegex.SubexpNames() {
					if i != 0 && name != "" && len(match) > i {
						result[name] = match[i]
					}
				}

				functionName := result[functionNameKey]
				if functionName == "" {
					return results
				}
				results = append(results, FuncCallResults{Name: result[functionNameKey], Arguments: ParseFunctionCallArgs(result[functionArgumentsKey], functionConfig)})
			}
		}
	} else {
		if len(llmResults) == 0 {
			llmResults = append(llmResults, llmresult)
		}
		results, _ = returnResult(llmResults)
	}

	// Determine which XML format to use (if any)
	var xmlFormat *XMLToolCallFormat
	if functionConfig.XMLFormat != nil {
		// Custom format specified
		xmlFormat = functionConfig.XMLFormat
		xlog.Debug("Using custom XML format")
	} else if functionConfig.XMLFormatPreset != "" {
		// Preset format specified
		xmlFormat = getXMLFormatPreset(functionConfig.XMLFormatPreset)
		if xmlFormat == nil {
			xlog.Debug("Unknown XML format preset, falling back to auto-detection", "preset", functionConfig.XMLFormatPreset)
		} else {
			xlog.Debug("Using XML format preset", "preset", functionConfig.XMLFormatPreset)
		}
	}
	// If xmlFormat is still nil, ParseXML will auto-detect

	// If no results from JSON parsing, try XML parsing
	// This handles cases where the response contains XML tool calls instead of JSON,
	// or mixed content with XML tool calls
	if len(results) == 0 {
		xmlResults, err := ParseXML(llmresult, xmlFormat)
		if err == nil && len(xmlResults) > 0 {
			xlog.Debug("Found XML tool calls", "count", len(xmlResults))
			results = append(results, xmlResults...)
		}
	} else {
		// Even if we found JSON results, check for XML tool calls in the response
		// This handles mixed content scenarios (text + JSON + XML)
		xmlResults, err := ParseXML(llmresult, xmlFormat)
		if err == nil && len(xmlResults) > 0 {
			xlog.Debug("Found additional XML tool calls alongside JSON", "xml_count", len(xmlResults))
			results = append(results, xmlResults...)
		}
	}

	return results
}

func ParseFunctionCallArgs(functionArguments string, functionConfig FunctionsConfig) string {
	if len(functionConfig.ArgumentRegex) == 0 {
		return functionArguments
	}

	// We use named regexes here to extract the function argument key value pairs and convert this to valid json.
	// TODO: there might be responses where an object as a value is expected/required. This is currently not handled.
	args := make(map[string]string)

	agrsRegexKeyName := "key"
	agrsRegexValueName := "value"

	if functionConfig.ArgumentRegexKey != "" {
		agrsRegexKeyName = functionConfig.ArgumentRegexKey
	}
	if functionConfig.ArgumentRegexValue != "" {
		agrsRegexValueName = functionConfig.ArgumentRegexValue
	}

	for _, r := range functionConfig.ArgumentRegex {
		var respRegex = regexp.MustCompile(r)
		var nameRange []string = respRegex.SubexpNames()
		var keyIndex = slices.Index(nameRange, agrsRegexKeyName)
		var valueIndex = slices.Index(nameRange, agrsRegexValueName)
		matches := respRegex.FindAllStringSubmatch(functionArguments, -1)
		for _, match := range matches {
			args[match[keyIndex]] = match[valueIndex]
		}
	}

	jsonBytes, _ := json.Marshal(args)

	return string(jsonBytes)
}
