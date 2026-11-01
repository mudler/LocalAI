// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const (
	MaxExecutionStatusBytes      = 2048
	MaxExecutionExplanationChars = 200
)

// ExecutionStatus is provider-reported observation, not authority or a host
// isolation guarantee. Only DecodeExecutionStatus validates a wire payload.
type ExecutionStatus struct {
	Readiness   string `json:"readiness"`
	Isolation   string `json:"isolation"`
	Explanation string `json:"explanation,omitempty"`
}

// DecodeExecutionStatus decodes Result.Payload, never the result envelope.
// Unknown/duplicate/trailing fields (including outputs) are refused. On any
// error it returns unknown/unknown, not partially trusted provider data.
func DecodeExecutionStatus(b []byte) (ExecutionStatus, error) {
	unknown := ExecutionStatus{Readiness: "unknown", Isolation: "unknown"}
	var v ExecutionStatus
	if !utf8.Valid(b) {
		return unknown, fmt.Errorf("invalid execution status UTF-8")
	}
	if err := strictJSON(b, &v, MaxExecutionStatusBytes); err != nil {
		return unknown, err
	}
	if v.Readiness != "available" && v.Readiness != "unavailable" && v.Readiness != "unknown" {
		return unknown, fmt.Errorf("invalid execution readiness")
	}
	if v.Isolation != "isolated" && v.Isolation != "host" && v.Isolation != "unknown" {
		return unknown, fmt.Errorf("invalid execution isolation")
	}
	// Distinguish absent explanation from explicit null or empty text.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return unknown, err
	}
	if _, present := fields["explanation"]; present && !ValidPlainText(v.Explanation, MaxExecutionExplanationChars) {
		return unknown, fmt.Errorf("invalid execution explanation")
	}
	if v.Readiness != "available" {
		v.Isolation = "unknown"
	}
	return v, nil
}
