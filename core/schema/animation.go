// SPDX-License-Identifier: MIT
package schema

import (
	"fmt"
	"math"
	"slices"
	"strconv"
)

// AnimationInput describes one named conditioning input. Media data uses the
// same URL/base64 conventions as the other media APIs; text is literal UTF-8.
type AnimationInput struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type Model3DAnimationRequest struct {
	BasicModelRequest
	Inputs         map[string]AnimationInput `json:"inputs"`
	Params         map[string]string         `json:"params,omitempty"`
	ResponseFormat string                    `json:"response_format,omitempty"`
}

// ThreeDOperation describes a supported workflow, including the complete set
// of inputs required together. Different workflows may share an endpoint.
type ThreeDOperation struct {
	ID         string            `json:"id"`
	Label      string            `json:"label"`
	Endpoint   string            `json:"endpoint"`
	Output     string            `json:"output"`
	Inputs     []ThreeDInput     `json:"inputs"`
	Parameters []ThreeDParameter `json:"parameters"`
}

type ThreeDInput struct {
	MaxBytes int    `json:"max_bytes,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

type ThreeDParameter struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Default  string   `json:"default,omitempty"`
	Min      float64  `json:"min,omitempty"`
	Max      float64  `json:"max,omitempty"`
	Options  []string `json:"options,omitempty"`
	Advanced bool     `json:"advanced,omitempty"`
}

func (p ThreeDParameter) Validate(value string) error {
	valid := false
	switch p.Type {
	case "enum":
		valid = slices.Contains(p.Options, value)
	case "uint64":
		_, err := strconv.ParseUint(value, 10, 64)
		valid = err == nil
	case "integer", "number":
		number, err := strconv.ParseFloat(value, 64)
		if p.Type == "integer" {
			_, err = strconv.ParseInt(value, 10, 64)
		}
		valid = err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) &&
			number >= p.Min && (p.Max == 0 || number <= p.Max)
	}
	if !valid {
		return fmt.Errorf("invalid parameter %q", p.Name)
	}
	return nil
}
