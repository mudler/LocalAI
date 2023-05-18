package stablediffusion

import "os"

type StableDiffusion struct {
	assetDir string
}

func New(assetDir string) (*StableDiffusion, error) {
	if _, err := os.Stat(assetDir); err != nil {
		return nil, err
	}
	return &StableDiffusion{
		assetDir: assetDir,
	}, nil
}

func (s *StableDiffusion) GenerateImage(height, width, mode, step, seed int, positive_prompt, negative_prompt, dst string) error {
	return GenerateImage(height, width, mode, step, seed, positive_prompt, negative_prompt, dst, s.assetDir)
}
