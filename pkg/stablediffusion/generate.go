//go:build stablediffusion
// +build stablediffusion

package stablediffusion

import (
	stableDiffusion "github.com/mudler/go-stable-diffusion"
)

func GenerateImage(height, width, mode, step, seed int, positive_prompt, negative_prompt, dst, asset_dir string) error {
	if height > 512 || width > 512 {
		return stableDiffusion.GenerateImageUpscaled(
			height,
			width,
			step,
			seed,
			positive_prompt,
			negative_prompt,
			dst,
			asset_dir,
		)
	}
	return stableDiffusion.GenerateImage(
		height,
		width,
		mode,
		step,
		seed,
		positive_prompt,
		negative_prompt,
		dst,
		"",
		asset_dir,
	)
}
