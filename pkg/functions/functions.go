package functions

import (
	"encoding/json"

	"github.com/rs/zerolog/log"
)

type Function struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}
type Functions []Function

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function,omitempty"`
}
type Tools []Tool

func (f Functions) ToJSONStructure() JSONFunctionStructure {
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
		js.OneOf = append(js.OneOf, Item{
			Type: "object",
			Properties: Properties{
				Function: FunctionName{Const: function.Name},
				Arguments: Argument{
					Type:       "object",
					Properties: prop,
				},
			},
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
