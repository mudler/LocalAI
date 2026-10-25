// SPDX-License-Identifier: MIT
// Package metadata defines application conventions for opaque backend metadata.
// New conventions do not require changes to the gRPC transport schema.
package metadata

import (
	"encoding/json"
	"fmt"
	"math"
)

type Usage struct {
	InputUnits     int             `json:"input_units"`
	OutputUnits    int             `json:"output_units"`
	AccountingRule string          `json:"accounting_rule,omitempty"`
	Details        json.RawMessage `json:"details,omitempty"`
}

func (u Usage) validate() error {
	if u.InputUnits < 0 || u.OutputUnits < 0 || u.InputUnits > math.MaxInt-u.OutputUnits {
		return fmt.Errorf("usage units must be nonnegative integers with a representable total")
	}
	return nil
}

func EncodeUsage(u Usage) ([]byte, error) {
	if err := u.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"usage": u})
}

// ParseUsage ignores unrelated metadata, but refuses incomplete or invalid
// counters so malformed metadata cannot silently produce a bill.
func ParseUsage(data []byte) (*Usage, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if envelope == nil {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	raw, found := envelope["usage"]
	if !found {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"input_units", "output_units"} {
		value, found := fields[key]
		if !found || string(value) == "null" {
			return nil, fmt.Errorf("usage requires %s", key)
		}
	}
	var u Usage
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, err
	}
	if err := u.validate(); err != nil {
		return nil, err
	}
	return &u, nil
}
