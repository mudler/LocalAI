package openresponses

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/functions"
)

func parseStreamingJSONToolCalls(text string) []functions.FuncCallResults {
	// Partial parsing heals unfinished arguments. The caller emits terminal
	// events and never revisits emitted calls, so only accept complete JSON.
	// Keep completed objects returned before an unfinished trailing object.
	objects, _ := functions.ParseJSONIterative(text, false)
	var calls []functions.FuncCallResults
	for _, object := range objects {
		name, ok := object["name"].(string)
		if !ok || name == "" {
			continue
		}
		arguments := "{}"
		if value, ok := object["arguments"]; ok {
			if s, ok := value.(string); ok {
				arguments = s
			} else {
				data, err := json.Marshal(value)
				if err != nil {
					continue
				}
				arguments = string(data)
			}
		}
		calls = append(calls, functions.FuncCallResults{Name: name, Arguments: arguments})
	}
	return calls
}
