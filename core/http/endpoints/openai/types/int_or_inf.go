package types

import (
	"encoding/json"
	"math"
)

const (
	// Inf is the maximum value for an IntOrInf.
	Inf IntOrInf = math.MaxInt
)

// IntOrInf is a type that can be either an int or "inf".
type IntOrInf int

// IsInf returns true if the value is "inf".
func (m IntOrInf) IsInf() bool {
	return m == Inf
}

// MarshalJSON marshals the IntOrInf to JSON.
func (m IntOrInf) MarshalJSON() ([]byte, error) {
	if m == Inf {
		return []byte("\"inf\""), nil
	}
	return json.Marshal(int(m))
}

// UnmarshalJSON unmarshals the IntOrInf from JSON.
func (m *IntOrInf) UnmarshalJSON(data []byte) error {
	if string(data) == "\"inf\"" {
		*m = Inf
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, (*int)(m))
}
