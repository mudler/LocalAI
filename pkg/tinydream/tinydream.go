package tinydream

import "os"

type TinyDream struct {
	assetDir string
}

func New(assetDir string) (*TinyDream, error) {
	if _, err := os.Stat(assetDir); err != nil {
		return nil, err
	}
	return &TinyDream{
		assetDir: assetDir,
	}, nil
}

func (td *TinyDream) GenerateImage(height, width, step, seed int, positive_prompt, negative_prompt, dst string) error {
	return GenerateImage(height, width, step, seed, positive_prompt, negative_prompt, dst, td.assetDir)
}
