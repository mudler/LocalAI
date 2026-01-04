package functions

import (
	"encoding/json"

	"github.com/mudler/xlog"
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

// ToJSONStructure converts a list of functions to a JSON structure that can be parsed to a grammar
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
			xlog.Error("error unmarshalling dat", "error", err)
		}
		err = json.Unmarshal(dat2, &defsD)
		if err != nil {
			xlog.Error("error unmarshalling dat2", "error", err)
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
		/*
			js.AnyOf = append(js.OneOf, Item{
				Type:       "object",
				Properties: property,
			})
		*/
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

// SanitizeTools removes null values from tool.parameters.properties and converts them to empty objects.
// This prevents Jinja template errors when processing tools with malformed parameter schemas.
func SanitizeTools(tools Tools) Tools {
	if len(tools) == 0 {
		return tools
	}

	sanitized := make(Tools, 0, len(tools))
	for _, tool := range tools {
		// Create a copy of the tool to avoid modifying the original
		sanitizedTool := Tool{
			Type:     tool.Type,
			Function: tool.Function,
		}

		// Check if the tool has parameters
		if sanitizedTool.Function.Parameters != nil {
			// Check if parameters has a "properties" field
			if properties, ok := sanitizedTool.Function.Parameters["properties"]; ok {
				// Try to cast properties to a map
				if propertiesMap, ok := properties.(map[string]interface{}); ok {
					// Create a new map for sanitized properties
					sanitizedProperties := make(map[string]interface{})
					hasNullValues := false

					// Iterate through properties and remove null values
					for key, value := range propertiesMap {
						if value == nil {
							// Convert null to empty object
							sanitizedProperties[key] = map[string]interface{}{}
							hasNullValues = true
							xlog.Warn("Found null value in tool parameter properties, converting to empty object",
								"tool", sanitizedTool.Function.Name,
								"parameter", key)
						} else {
							// Preserve valid values
							sanitizedProperties[key] = value
						}
					}

					// Update the properties if we found null values
					if hasNullValues {
						sanitizedTool.Function.Parameters["properties"] = sanitizedProperties
					}
				}
			}
		}

		sanitized = append(sanitized, sanitizedTool)
	}

	return sanitized
}
