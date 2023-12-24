//go:build !tinydream
// +build !tinydream

package tinydream

import "fmt"

func GenerateImage(height, width, step, seed int, positive_prompt, negative_prompt, dst, asset_dir string) error {
	return fmt.Errorf("This version of LocalAI was built without the tinytts tag")
}
