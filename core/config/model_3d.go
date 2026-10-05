// SPDX-License-Identifier: MIT
package config

import (
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

// ThreeDOperations is shared by discovery and request validation so the form
// cannot advertise inputs or parameters that its selected backend rejects.
func (c *ModelConfig) ThreeDOperations() []schema.ThreeDOperation {
	switch c.Backend {
	case "kimodocpp":
		if !c.HasUsecases(FLAG_3D_ANIMATION) {
			return nil
		}
		params := []schema.ThreeDParameter{
			{Name: "frames", Label: "Frames (30 FPS)", Type: "integer", Default: "150", Min: 60, Max: 150},
			{Name: "steps", Label: "Sampling steps", Type: "integer", Default: "100", Min: 1, Max: 1000, Advanced: true},
			{Name: "text_guidance", Label: "Text guidance", Type: "number", Default: "2", Min: 0, Max: 100, Advanced: true},
			{Name: "seed", Label: "Seed", Type: "uint64", Default: "0", Advanced: true},
		}
		for i := range params {
			for _, option := range c.Options {
				if key, value, ok := strings.Cut(option, ":"); ok && key == params[i].Name {
					params[i].Default = value
				}
			}
		}
		return []schema.ThreeDOperation{{
			ID: "animate", Label: "Animate", Endpoint: "/3d/animate", Output: "skeleton_animation",
			Inputs:     []schema.ThreeDInput{{Name: "prompt", Type: "text", Label: "Motion prompt", Required: true, MaxBytes: 4096}},
			Parameters: params,
		}}
	case "trellis2cpp":
		if !c.HasUsecases(FLAG_3D) {
			return nil
		}
		steps, guidance := "12", "7.5"
		if c.Step > 0 {
			steps = strconv.Itoa(c.Step)
		}
		if c.CFGScale > 0 {
			guidance = strconv.FormatFloat(float64(c.CFGScale), 'g', -1, 32)
		}
		return []schema.ThreeDOperation{
			{ID: "generate", Label: "Generate mesh", Endpoint: "/3d/generations", Output: "mesh",
				Inputs: []schema.ThreeDInput{{Name: "image", Type: "image", Label: "Conditioning image", Required: true}},
				Parameters: []schema.ThreeDParameter{
					{Name: "quality", Label: "Quality", Type: "enum", Default: "auto", Options: []string{"auto", "coarse", "512", "1024"}},
					{Name: "background", Label: "Background", Type: "enum", Default: "auto", Options: []string{"auto", "keep", "black", "white"}},
					{Name: "step", Label: "Sampling steps", Type: "integer", Default: steps, Min: 1, Advanced: true},
					{Name: "texture_steps", Label: "Texture steps", Type: "integer", Default: "12", Min: 1, Advanced: true},
					{Name: "cfg_scale", Label: "Guidance", Type: "number", Default: guidance, Min: 0, Advanced: true},
					{Name: "seed", Label: "Seed", Type: "integer", Min: 0, Max: 2147483647, Advanced: true},
				}},
			{ID: "remesh", Label: "Remesh", Endpoint: "/3d/remesh", Output: "mesh",
				Inputs:     []schema.ThreeDInput{{Name: "mesh", Type: "mesh", Label: "Source mesh", Required: true}},
				Parameters: []schema.ThreeDParameter{{Name: "detail", Label: "Detail (%)", Type: "number", Default: "0.5", Min: 0.35, Max: 2.5}}},
		}
	}
	return nil
}
