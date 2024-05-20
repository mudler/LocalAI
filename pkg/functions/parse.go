package functions

import (
	"encoding/json"
	"regexp"

	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

// FunctionsConfig is the configuration for the tool/function call.
// It includes setting to map the function name and arguments from the response
// and, for instance, also if processing the requests with BNF grammars.
type FunctionsConfig struct {
	// DisableNoAction disables the "no action" tool
	// By default we inject a tool that does nothing and is used to return an answer from the LLM
	DisableNoAction bool `yaml:"disable_no_action"`

	// NoActionFunctionName is the name of the function that does nothing. It defaults to "answer"
	NoActionFunctionName string `yaml:"no_action_function_name"`

	// NoActionDescriptionName is the name of the function that returns the description of the no action function
	NoActionDescriptionName string `yaml:"no_action_description_name"`

	// ParallelCalls enables the LLM to return multiple function calls in the same response
	ParallelCalls bool `yaml:"parallel_calls"`

	// GrammarMessage enables the LLM to return strings and not only JSON objects
	// This is useful for models to not constraing returning only JSON and also messages back to the user
	GrammarMessage bool `yaml:"grammar_message"`

	// NoGrammar disables the grammar parsing and parses the responses directly from the LLM
	NoGrammar bool `yaml:"no_grammar"`

	// ResponseRegex is a named regex to extract the function name and arguments from the response
	ResponseRegex string `yaml:"response_regex"`

	// JSONRegexMatch is a regex to extract the JSON object from the response
	JSONRegexMatch []string `yaml:"json_regex_match"`

	// GrammarPrefix is the suffix to append to the grammar when being generated
	// This is useful when models prepend a tag before returning JSON
	GrammarPrefix string `yaml:"grammar_prefix"`

	// ReplaceResults allow to replace strings in the results before parsing them
	ReplaceResults []ReplaceResult `yaml:"replace_results"`

	// FunctionName enable the LLM to return { "name": "function_name", "arguments": { "arg1": "value1", "arg2": "value2" } }
	// instead of { "function": "function_name", "arguments": { "arg1": "value1", "arg2": "value2" } }.
	// This might be useful for certain models trained with the function name as the first token.
	FunctionName bool `yaml:"return_name_in_function_response"`
}

type ReplaceResult struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
}

type FuncCallResults struct {
	Name      string
	Arguments string
}

func ParseFunctionCall(llmresult string, functionConfig FunctionsConfig) []FuncCallResults {
	log.Debug().Msgf("LLM result: %s", llmresult)

	for _, item := range functionConfig.ReplaceResults {
		k, v := item.Key, item.Value
		log.Debug().Msgf("Replacing %s with %s", k, v)
		re := regexp.MustCompile(k)
		llmresult = re.ReplaceAllString(llmresult, v)
	}

	log.Debug().Msgf("LLM result(processed): %s", llmresult)

	functionNameKey := "function"
	if functionConfig.FunctionName {
		functionNameKey = "name"
	}

	results := []FuncCallResults{}

	returnResult := func(s string) (result []FuncCallResults, e error) {
		// As we have to change the result before processing, we can't stream the answer token-by-token (yet?)
		var ss []map[string]interface{}
		result = make([]FuncCallResults, 0)
		s = utils.EscapeNewLines(s)
		err := json.Unmarshal([]byte(s), &ss)
		if err != nil {
			// If the LLM result is a single object, try unmarshaling it into a single map
			var singleObj map[string]interface{}
			err = json.Unmarshal([]byte(s), &singleObj)
			if err != nil {
				log.Warn().Err(err).Str("escapedLLMResult", s).Msg("unable to unmarshal llm result")
			} else {
				ss = []map[string]interface{}{singleObj}
			}
		}

		log.Debug().Msgf("Function return: %s %+v", s, ss)

		for _, s := range ss {
			// The grammar defines the function name as "function", while OpenAI returns "name"
			func_name, ok := s[functionNameKey]
			if !ok {
				continue
				//return result, fmt.Errorf("unable to find function name in result")
			}
			// Similarly, while here arguments is a map[string]interface{}, OpenAI actually want a stringified object
			args, ok := s["arguments"] // arguments needs to be a string, but we return an object from the grammar result (TODO: fix)
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

		return result, nil
	}

	// the response is a string that we have to parse
	result := make(map[string]string)

	if len(functionConfig.JSONRegexMatch) != 0 {
		for _, r := range functionConfig.JSONRegexMatch {
			// We use a regex to extract the JSON object from the response
			var respRegex = regexp.MustCompile(r)
			match := respRegex.FindStringSubmatch(llmresult)
			if len(match) >= 2 {
				llmresult = match[1]
				break
			}
		}
	}

	if functionConfig.ResponseRegex != "" {
		// We use named regexes here to extract the function name and arguments
		// obviously, this expects the LLM to be stable and return correctly formatted JSON
		// TODO: optimize this and pre-compile it
		var respRegex = regexp.MustCompile(functionConfig.ResponseRegex)
		match := respRegex.FindStringSubmatch(llmresult)
		for i, name := range respRegex.SubexpNames() {
			if i != 0 && name != "" && len(match) > i {
				result[name] = match[i]
			}
		}

		// TODO: open point about multiple results and/or mixed with chat messages
		// This is not handled as for now, we only expect one function call per response
		functionName := result[functionNameKey]
		if functionName == "" {
			return results
		}
		results = append(results, FuncCallResults{Name: result[functionNameKey], Arguments: result["arguments"]})
	} else {
		results, _ = returnResult(llmresult)
	}

	return results
}
