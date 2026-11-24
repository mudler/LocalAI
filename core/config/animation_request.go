// SPDX-License-Identifier: MIT
package config

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/schema"
)

// ValidateAnimationRequest is shared by HTTP and pipeline execution so both enforce
// the model's advertised input and parameter contract before loading it.
func (cfg *ModelConfig) ValidateAnimationRequest(input *schema.Model3DAnimationRequest) error {
	if input.ResponseFormat != "" && input.ResponseFormat != "url" && input.ResponseFormat != "b64_json" {
		return fmt.Errorf("response_format must be url or b64_json")
	}
	for _, operation := range cfg.ThreeDOperations() {
		if operation.Endpoint != "/3d/animate" || !AnimationInputsMatch(input.Inputs, operation.Inputs) {
			continue
		}
		parameters := make(map[string]schema.ThreeDParameter, len(operation.Parameters))
		for _, parameter := range operation.Parameters {
			parameters[parameter.Name] = parameter
		}
		for name, value := range input.Params {
			parameter, ok := parameters[name]
			if !ok {
				return fmt.Errorf("unsupported animation parameter %q", name)
			}
			if err := parameter.Validate(value); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("the selected model does not support these animation inputs; consult its three_d_operations capabilities")
}

func AnimationInputsMatch(inputs map[string]schema.AnimationInput, requirements []schema.ThreeDInput) bool {
	known := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		known[requirement.Name] = true
		input, present := inputs[requirement.Name]
		if !present && !requirement.Required {
			continue
		}
		if !present || input.Type != requirement.Type || strings.TrimSpace(input.Data) == "" {
			return false
		}
		if input.Type == "text" && (!utf8.ValidString(input.Data) || strings.ContainsRune(input.Data, 0) ||
			(requirement.MaxBytes > 0 && len(input.Data) > requirement.MaxBytes)) {
			return false
		}
	}
	for name := range inputs {
		if !known[name] {
			return false
		}
	}
	return true
}
