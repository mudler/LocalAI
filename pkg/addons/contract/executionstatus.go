// SPDX-License-Identifier: MIT
package contract

import (
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
	// NetworkPolicyEnforced is the provider's report that every network
	// connection of its commands goes through the host's network policy. It is
	// kept only for an available, isolated provider; the host still decides
	// whether to rely on it.
	NetworkPolicyEnforced bool `json:"networkPolicyEnforced,omitempty"`
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
	fields, err := exactObjectFields(b, MaxExecutionStatusBytes, "readiness", "isolation", "explanation", "networkPolicyEnforced")
	if err != nil {
		return unknown, err
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
	if _, present := fields["explanation"]; present && !ValidPlainText(v.Explanation, MaxExecutionExplanationChars) {
		return unknown, fmt.Errorf("invalid execution explanation")
	}
	if v.Readiness != "available" {
		v.Isolation = "unknown"
	}
	if v.Readiness != "available" || v.Isolation != "isolated" {
		v.NetworkPolicyEnforced = false
	}
	return v, nil
}
