package functions

import (
	"encoding/json"

	"github.com/rs/zerolog/log"
)

const (
	defaultFunctionNameKey      = "name"
	defaultFunctionArgumentsKey = "arguments"
)

type Function struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Strict      bool                   `json:"strict"`
	Parameters  map[string]interface{} `json:"parameters"`
}
type Functions []Function

type FunctionName struct {
	Const string `json:"const"`
}

type Argument struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function,omitempty"`
}
type Tools []Tool

// ToJSONNameStructure converts a list of functions to a JSON structure that can be parsed to a grammar
// This allows the LLM to return a response of the type: { "name": "function_name", "arguments": { "arg1": "value1", "arg2": "value2" } }
func (f Functions) ToJSONStructure(name, args string) JSONFunctionStructure {
	nameKey := defaultFunctionNameKey
	argsKey := defaultFunctionArgumentsKey
	if name != "" {
		nameKey = name
	}
	if args != "" {
		argsKey = args
	}
	js := JSONFunctionStructure{}
	for _, function := range f {
		//	t := function.Parameters["type"]
		//tt := t.(string)

		properties := function.Parameters["properties"]
		defs := function.Parameters["$defs"]
		dat, _ := json.Marshal(properties)
		dat2, _ := json.Marshal(defs)
		prop := map[string]interface{}{}
		defsD := map[string]interface{}{}

		err := json.Unmarshal(dat, &prop)
		if err != nil {
			log.Error().Err(err).Msg("error unmarshalling dat")
		}
		err = json.Unmarshal(dat2, &defsD)
		if err != nil {
			log.Error().Err(err).Msg("error unmarshalling dat2")
		}
		if js.Defs == nil {
			js.Defs = defsD
		}

		property := map[string]interface{}{}
		property[nameKey] = FunctionName{Const: function.Name}
		property[argsKey] = Argument{
			Type:       "object",
			Properties: prop,
		}
		js.OneOf = append(js.OneOf, Item{
			Type:       "object",
			Properties: property,
		})
	}
	return js
}

// Select returns a list of functions containing the function with the given name
func (f Functions) Select(name string) Functions {
	var funcs Functions

	for _, f := range f {
		if f.Name == name {
			funcs = []Function{f}
			break
		}
	}

	return funcs
}
