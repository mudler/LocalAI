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

	// AutomaticToolParsingFallback enables automatic tool call parsing fallback:
	// - Wraps raw string arguments as {"query": raw_string} when JSON parsing fails
	// - Parses tool calls from response content even when no tools were in the request
	AutomaticToolParsingFallback bool `yaml:"automatic_tool_parsing_fallback,omitempty" json:"automatic_tool_parsing_fallback,omitempty"`

	// DisablePEGParser disables the PEG parser and falls back to the legacy iterative parser
	DisablePEGParser bool `yaml:"disable_peg_parser,omitempty" json:"disable_peg_parser,omitempty"`

	// ToolFormatMarkers holds auto-detected markers from the C++ backend (via gRPC).
	// When set, these are used to build the PEG parser dynamically instead of using presets.
	ToolFormatMarkers *ToolFormatMarkers `yaml:"-" json:"-"`
}

// ToolFormatMarkers holds auto-detected tool format markers from the C++ autoparser.
type ToolFormatMarkers struct {
	FormatType string // "json_native", "tag_with_json", "tag_with_tagged"

	// Tool section markers
	SectionStart string
	SectionEnd   string
	PerCallStart string
	PerCallEnd   string

	// Function name markers
	FuncNamePrefix string
	FuncNameSuffix string
	FuncClose      string

	// Argument markers
	ArgNamePrefix  string
	ArgNameSuffix  string
	ArgValuePrefix string
	ArgValueSuffix string
	ArgSeparator   string
	ArgsStart      string
	ArgsEnd        string

	// JSON format fields
	NameField        string
	ArgsField        string
	IDField          string
	FunNameIsKey     bool
	ToolsArrayWrapped bool
	FunctionField    string
	ParameterOrder   []string

	// Generated ID field
	GenIDField string

	// Call ID markers
	CallIDPosition string // "none", "pre_func_name", "between_func_and_args", "post_args"
	CallIDPrefix   string
	CallIDSuffix   string

	// Reasoning markers
	ReasoningStart string
	ReasoningEnd   string

	// Content markers
	ContentStart string
	ContentEnd   string
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
	// AllowToolcallInThink allows tool calls inside thinking/reasoning blocks
	AllowToolcallInThink bool `yaml:"allow_toolcall_in_think,omitempty" json:"allow_toolcall_in_think,omitempty"`
}

type FuncCallResults struct {
	Name      string
	Arguments string
	ID        string
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
// ParseJSON parses JSON objects from a string, supporting multiple JSON objects
// Now defaults to iterative parser for better streaming support
// Falls back to legacy parser if iterative parser fails
func ParseJSON(s string) ([]map[string]any, error) {
	// Try iterative parser first (non-partial mode for complete parsing)
	results, err := ParseJSONIterative(s, false)
	if err == nil && len(results) > 0 {
		return results, nil
	}
	// Fall back to legacy parser for backward compatibility
	return parseJSONLegacy(s)
}

// ParseJSONIterative parses JSON using the iterative parser
// Supports partial parsing for streaming scenarios
// Returns objects and arrays (matching llama.cpp behavior)
func ParseJSONIterative(s string, isPartial bool) ([]map[string]any, error) {
	parser := NewChatMsgParser(s, isPartial)
	var results []map[string]any

	// Try to parse JSON values one by one
	for parser.Pos() < len(parser.Input()) {
		jsonValue, isPartialJSON, _, err := parser.TryConsumeJSON()
		if err != nil {
			// If it's a partial exception and we're in partial mode, return what we have
			if _, ok := err.(*ChatMsgPartialException); ok && isPartial {
				break
			}
			// For non-partial errors or when not in partial mode, try legacy parsing
			return parseJSONLegacy(s)
		}
		if jsonValue != nil {
			// Convert to map[string]any if it's an object, or handle arrays
			if obj, ok := jsonValue.(map[string]any); ok {
				results = append(results, obj)
			} else if arr, ok := jsonValue.([]any); ok {
				// Handle arrays: extract objects from array
				for _, item := range arr {
					if obj, ok := item.(map[string]any); ok {
						results = append(results, obj)
					}
				}
			}
		}
		if isPartialJSON {
			break
		}
		// Skip whitespace between JSON values
		parser.ConsumeSpaces()
	}

	if len(results) > 0 {
		return results, nil
	}

	// Fallback to legacy parsing if iterative parser found nothing
	return parseJSONLegacy(s)
}

// parseJSONLegacy is the original decoder-based JSON parsing (kept for compatibility)
func parseJSONLegacy(s string) ([]map[string]any, error) {
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

// GetXMLFormatPreset returns a preset XML format by name, or nil if not found
// This is exported for use in chat.go streaming integration
func GetXMLFormatPreset(name string) *XMLToolCallFormat {
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
			name: "qwen3.5",
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
				ScopeStart:           "<|tool_calls_section_begin|>",
				ToolStart:            "<|tool_call_begin|>",
				ToolSep:              "<|tool_call_argument_begin|>{",
				KeyStart:             "\"",
				KeyValSep:            "\":",
				ValEnd:               ",",
				ToolEnd:              "}<|tool_call_end|>",
				ScopeEnd:             "<|tool_calls_section_end|>",
				LastValEnd:           &emptyValEnd,
				RawArgVal:            &falseVal,
				AllowToolcallInThink: true, // Kimi-K2 supports tool calls in thinking blocks
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

// ParseXML parses XML-formatted tool calls from an LLM response string.
// Tries the iterative parser first, then falls back to the PEG parser.
func ParseXML(s string, format *XMLToolCallFormat) ([]FuncCallResults, error) {
	results, err := ParseXMLIterative(s, format, false)
	if err == nil && len(results) > 0 {
		return results, nil
	}
	// Fall back to PEG parser for formats that the iterative parser doesn't handle
	pegResults := ParseFunctionCallPEG(s, FunctionsConfig{XMLFormat: format})
	if len(pegResults) > 0 {
		return pegResults, nil
	}
	return results, err
}

// getScopeOrToolStart returns the scope start marker if set, else the tool start marker.
func getScopeOrToolStart(format *XMLToolCallFormat) string {
	if format == nil {
		return ""
	}
	if format.ScopeStart != "" {
		return format.ScopeStart
	}
	return format.ToolStart
}

// tryParseXMLFromScopeStart finds the first occurrence of scopeStart (or format.ToolStart),
// splits the input there, and parses only the suffix as XML tool calls. Returns (toolCalls, true)
// if any tool calls were parsed, else (nil, false). This mimics llama.cpp's PEG order so that
// reasoning or content before the tool block does not cause "whitespace only before scope" to fail.
func tryParseXMLFromScopeStart(s string, format *XMLToolCallFormat, isPartial bool) ([]FuncCallResults, bool) {
	if format == nil {
		return nil, false
	}
	scopeStart := getScopeOrToolStart(format)
	if scopeStart == "" {
		return nil, false
	}
	idx := strings.Index(s, scopeStart)
	if idx < 0 {
		return nil, false
	}
	toolCallsPart := s[idx:]
	parser := NewChatMsgParser(toolCallsPart, isPartial)
	success, err := parser.TryConsumeXMLToolCalls(format)
	if err != nil {
		if _, ok := err.(*ChatMsgPartialException); ok && isPartial {
			return parser.ToolCalls(), len(parser.ToolCalls()) > 0
		}
		return nil, false
	}
	if success && len(parser.ToolCalls()) > 0 {
		return parser.ToolCalls(), true
	}
	return nil, false
}

// ParseXMLIterative parses XML tool calls using the iterative parser
// This provides better streaming and partial parsing support.
// When format is nil or when format is set, tries "find scope/tool start, split, parse suffix"
// first (llama.cpp PEG order) so that content before the tool block does not cause parse failure.
func ParseXMLIterative(s string, format *XMLToolCallFormat, isPartial bool) ([]FuncCallResults, error) {
	// Try split-on-scope first so reasoning/content before tool block is skipped
	if format != nil {
		if results, ok := tryParseXMLFromScopeStart(s, format, isPartial); ok {
			return results, nil
		}
	} else {
		formats := getAllXMLFormats()
		for _, fmtPreset := range formats {
			if fmtPreset.format != nil {
				if results, ok := tryParseXMLFromScopeStart(s, fmtPreset.format, isPartial); ok {
					return results, nil
				}
			}
		}
	}

	parser := NewChatMsgParser(s, isPartial)

	// Auto-detect format if not provided
	if format == nil {
		formats := getAllXMLFormats()
		for _, fmtPreset := range formats {
			if fmtPreset.format != nil {
				// Try parsing with this format
				parser.MoveTo(0)
				parser.ClearTools()
				success, err := parser.TryConsumeXMLToolCalls(fmtPreset.format)
				if err != nil {
					// Check if it's a partial exception (recoverable)
					if _, ok := err.(*ChatMsgPartialException); ok {
						// Partial parse, return what we have
						return parser.ToolCalls(), nil
					}
					// Try next format
					continue
				}
				if success && len(parser.ToolCalls()) > 0 {
					return parser.ToolCalls(), nil
				}
			}
		}
		// No format matched, return empty
		return []FuncCallResults{}, nil
	}

	// Use specified format
	success, err := parser.TryConsumeXMLToolCalls(format)
	if err != nil {
		// Check if it's a partial exception (recoverable)
		if _, ok := err.(*ChatMsgPartialException); ok {
			// Partial parse, return what we have
			return parser.ToolCalls(), nil
		}
		return nil, err
	}

	if !success {
		return []FuncCallResults{}, nil
	}

	return parser.ToolCalls(), nil
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

// XML_TOOL_CALL_PARTIAL_FLAG is a marker used to indicate partial JSON in tool calls
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp line 314
const XML_TOOL_CALL_PARTIAL_FLAG = "XML_TOOL_CALL_PARTIAL_FLAG"

// partialJSON cleans up partial JSON by removing incomplete parts marked with XML_TOOL_CALL_PARTIAL_FLAG
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 314-330
func partialJSON(jsonStr string) (string, bool) {
	pos := strings.LastIndex(jsonStr, XML_TOOL_CALL_PARTIAL_FLAG)
	if pos == -1 {
		return jsonStr, false
	}
	// Check that only valid JSON characters follow the flag
	for i := pos + len(XML_TOOL_CALL_PARTIAL_FLAG); i < len(jsonStr); i++ {
		ch := jsonStr[i]
		if ch != '\'' && ch != '"' && ch != '}' && ch != ':' && ch != ']' && !strings.ContainsRune(" \t\n\r", rune(ch)) {
			return jsonStr, false
		}
	}
	// Remove the flag and everything after it
	if pos > 0 && jsonStr[pos-1] == '"' {
		pos--
	}
	return jsonStr[:pos], true
}

// genPartialJSON generates partial JSON with XML_TOOL_CALL_PARTIAL_FLAG marker
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 332-343
func genPartialJSON(args map[string]any, functionName string, rest string, needle string) (string, bool) {
	// Add the partial argument with the flag
	args[rest+needle] = XML_TOOL_CALL_PARTIAL_FLAG
	jsonBytes, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	jsonStr := string(jsonBytes)
	// Try to clean up the partial JSON
	if cleaned, isPartial := partialJSON(jsonStr); isPartial {
		return cleaned, true
	}
	return jsonStr, false
}


// parseParameterValue parses a parameter value based on format configuration
// Implements JSON-first parsing: tries JSON parsing first (if raw_argval is false/null),
// validates JSON is complete, then falls back to text parsing.
// This matches llama.cpp's behavior in chat-parser-xml-toolcall.cpp lines 501-555
func parseParameterValue(paramValue string, format *XMLToolCallFormat) any {
	// Trim if configured
	if format.TrimRawArgVal {
		paramValue = strings.TrimSpace(paramValue)
	}

	// Handle raw_argval option
	if format.RawArgVal != nil {
		if *format.RawArgVal {
			// Raw string only - no JSON parsing
			return paramValue
		}
		// raw_argval is false - JSON only, must be valid JSON
		var jsonValue any
		if err := json.Unmarshal([]byte(paramValue), &jsonValue); err == nil {
			// Valid JSON - return parsed value (including primitives)
			return jsonValue
		}
		// JSON parsing failed but raw_argval is false - return as string anyway
		// (llama.cpp would throw an error, but we're more lenient)
		return paramValue
	}

	// Default: raw_argval is nil - try JSON first, fallback to text
	// This matches llama.cpp's behavior where both are allowed when raw_argval is nullopt
	var jsonValue any
	if err := json.Unmarshal([]byte(paramValue), &jsonValue); err != nil {
		// Not valid JSON, treat as plain text string
		return paramValue
	}

	// Valid JSON was parsed - return the parsed value
	// This includes objects, arrays, and primitives (null, true, false, numbers, strings)
	// This matches llama.cpp's behavior where JSON values (including primitives) are used as-is
	return jsonValue
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

	extractJSON := func(results []string) (result []FuncCallResults, e error) {
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
				// Arguments from grammar result is a map[string]interface{}, but OpenAI expects a stringified JSON object
				// We marshal it to JSON string here to match OpenAI's format
				args, ok := s[functionArgumentsKey]
				if !ok {
					continue
					//return result, fmt.Errorf("unable to find arguments in result")
				}
				// Marshal arguments to JSON string (handles both object and string cases)
				var d []byte
				if argsStr, ok := args.(string); ok {
					// Check if the string is valid JSON; if not, auto-heal if enabled
					var testJSON map[string]any
					if json.Unmarshal([]byte(argsStr), &testJSON) == nil {
						d = []byte(argsStr)
					} else if functionConfig.AutomaticToolParsingFallback {
						healed := map[string]string{"query": argsStr}
						d, _ = json.Marshal(healed)
						xlog.Debug("Automatic tool parsing fallback: wrapped raw string arguments", "raw", argsStr)
					} else {
						d = []byte(argsStr)
					}
				} else {
					// Object, marshal to JSON
					d, _ = json.Marshal(args)
				}
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
		// Pre-compile regexes for better performance
		compiledRegexes := make([]*regexp.Regexp, 0, len(functionConfig.ResponseRegex))
		for _, r := range functionConfig.ResponseRegex {
			compiledRegexes = append(compiledRegexes, regexp.MustCompile(r))
		}
		for _, respRegex := range compiledRegexes {
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
		results, _ = extractJSON(llmResults)
	}

	// Try PEG parser (unless disabled) — this is the primary tool call parser
	pegFound := false
	if !functionConfig.DisablePEGParser {
		xlog.Debug("[ParseFunctionCall] trying PEG parser")
		pegResults := ParseFunctionCallPEG(llmresult, functionConfig)
		if len(pegResults) > 0 {
			xlog.Debug("[ParseFunctionCall] PEG parser found tool calls", "count", len(pegResults))
			results = mergeResults(results, pegResults)
			pegFound = true
		} else {
			xlog.Debug("[ParseFunctionCall] PEG parser found no tool calls")
		}
	} else {
		xlog.Debug("[ParseFunctionCall] PEG parser disabled, skipping")
	}

	// Fallback: try iterative XML parser only when PEG didn't find results
	// and the input looks like it contains XML tool call markers.
	// This handles edge cases like trailing content after tool calls.
	if !pegFound && (strings.Contains(llmresult, "<tool_call>") || strings.Contains(llmresult, "<function=")) {
		xlog.Debug("[ParseFunctionCall] PEG missed, falling back to iterative XML parser")
		if xmlResults, err := ParseXMLIterative(llmresult, nil, false); err == nil && len(xmlResults) > 0 {
			// Filter out malformed results where the name looks like JSON
			var validResults []FuncCallResults
			for _, r := range xmlResults {
				if !strings.HasPrefix(strings.TrimSpace(r.Name), "{") {
					validResults = append(validResults, r)
				}
			}
			if len(validResults) > 0 {
				xlog.Debug("[ParseFunctionCall] XML fallback found tool calls", "count", len(validResults))
				results = mergeResults(results, validResults)
			}
		}
	}

	return results
}

// mergeResults combines two result slices, deduplicating by name+arguments.
func mergeResults(existing, additional []FuncCallResults) []FuncCallResults {
	seen := make(map[string]bool)
	for _, r := range existing {
		seen[r.Name+"|"+r.Arguments] = true
	}
	for _, r := range additional {
		key := r.Name + "|" + r.Arguments
		if !seen[key] {
			existing = append(existing, r)
			seen[key] = true
		}
	}
	return existing
}

func ParseFunctionCallArgs(functionArguments string, functionConfig FunctionsConfig) string {
	// Clean up double curly braces (common issue with template engines)
	// Replace {{ with { and }} with } but only if they appear at the start/end
	// This handles cases like {{"key":"value"}} -> {"key":"value"}
	cleaned := functionArguments
	//if strings.HasPrefix(cleaned, "{{") && strings.HasSuffix(cleaned, "}}") {
	// Check if it's double braces at the boundaries
	//	cleaned = strings.TrimPrefix(cleaned, "{")
	//	cleaned = strings.TrimSuffix(cleaned, "}")
	//}

	if len(functionConfig.ArgumentRegex) == 0 {
		return cleaned
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
