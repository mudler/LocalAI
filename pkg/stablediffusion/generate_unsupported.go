//go:build !stablediffusion
// +build !stablediffusion

package stablediffusion

import "fmt"

func GenerateImage(height, width, mode, step, seed int, positive_prompt, negative_prompt, dst, asset_dir string) error {
	return fmt.Errorf("This version of LocalAI was built without the stablediffusion tag")
}
