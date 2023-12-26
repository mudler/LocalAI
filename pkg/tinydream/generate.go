//go:build tinydream
// +build tinydream

package tinydream

import (
	"fmt"
	"path/filepath"

	tinyDream "github.com/M0Rf30/go-tiny-dream"
)

func GenerateImage(height, width, step, seed int, positive_prompt, negative_prompt, dst, asset_dir string) error {
	fmt.Println(dst)
	if height > 512 || width > 512 {
		return tinyDream.GenerateImage(
			1,
			step,
			seed,
			positive_prompt,
			negative_prompt,
			filepath.Dir(dst),
			asset_dir,
		)
	}

	return tinyDream.GenerateImage(
		0,
		step,
		seed,
		positive_prompt,
		negative_prompt,
		filepath.Dir(dst),
		asset_dir,
	)
}
