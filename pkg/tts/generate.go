//go:build tts
// +build tts

package tts

import (
	piper "github.com/mudler/go-piper"
)

func tts(text, model, assetDir, arLib, dst string) error {
	return piper.TextToWav(text, model, assetDir, arLib, dst)
}
