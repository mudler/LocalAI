package tts

import "os"

type Piper struct {
	assetDir string
}

func New(assetDir string) (*Piper, error) {
	if _, err := os.Stat(assetDir); err != nil {
		return nil, err
	}
	return &Piper{
		assetDir: assetDir,
	}, nil
}

func (s *Piper) TTS(text, model, dst string) error {
	return tts(text, model, s.assetDir, "", dst)
}
