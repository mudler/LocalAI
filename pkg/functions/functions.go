package functions

import (
	"encoding/json"
	"fmt"

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

// sanitizeValue recursively sanitizes null values in a JSON structure, converting them to empty objects.
// It handles maps, slices, and nested structures.
func sanitizeValue(value interface{}, path string) interface{} {
	if value == nil {
		// Convert null to empty object
		xlog.Debug("SanitizeTools: found null value, converting to empty object", "path", path)
		return map[string]interface{}{}
	}

	switch v := value.(type) {
	case map[string]interface{}:
		// Recursively sanitize map values
		sanitized := make(map[string]interface{})
		for key, val := range v {
			newPath := path
			if newPath != "" {
				newPath += "."
			}
			newPath += key
			sanitized[key] = sanitizeValue(val, newPath)
		}
		return sanitized

	case []interface{}:
		// Recursively sanitize slice elements
		sanitized := make([]interface{}, len(v))
		for i, val := range v {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			sanitized[i] = sanitizeValue(val, newPath)
		}
		return sanitized

	default:
		// For primitive types (string, number, bool), return as-is
		return value
	}
}

// SanitizeTools removes null values from tool.parameters.properties and converts them to empty objects.
// This prevents Jinja template errors when processing tools with malformed parameter schemas.
// It works by marshaling to JSON, recursively sanitizing the JSON structure, and unmarshaling back.
func SanitizeTools(tools Tools) Tools {
	if len(tools) == 0 {
		return tools
	}

	xlog.Debug("SanitizeTools: processing tools", "count", len(tools))

	// Marshal to JSON to work with the actual JSON representation
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		xlog.Warn("SanitizeTools: failed to marshal tools to JSON", "error", err)
		return tools
	}

	// Parse JSON into a generic structure
	var toolsData []map[string]interface{}
	if err := json.Unmarshal(toolsJSON, &toolsData); err != nil {
		xlog.Warn("SanitizeTools: failed to unmarshal tools JSON", "error", err)
		return tools
	}

	// Recursively sanitize the JSON structure
	for i, tool := range toolsData {
		if function, ok := tool["function"].(map[string]interface{}); ok {
			// Recursively sanitize the entire tool structure
			tool["function"] = sanitizeValue(function, fmt.Sprintf("tools[%d].function", i))
		}
		toolsData[i] = tool
	}

	// Marshal back to JSON
	sanitizedJSON, err := json.Marshal(toolsData)
	if err != nil {
		xlog.Warn("SanitizeTools: failed to marshal sanitized tools", "error", err)
		return tools
	}

	// Unmarshal back into Tools structure
	var sanitized Tools
	if err := json.Unmarshal(sanitizedJSON, &sanitized); err != nil {
		xlog.Warn("SanitizeTools: failed to unmarshal sanitized tools", "error", err)
		return tools
	}

	return sanitized
}
