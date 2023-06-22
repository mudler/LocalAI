//go:build !tts
// +build !tts

package tts

import "fmt"

func tts(text, model, assetDir, arLib, dst string) error {
	return fmt.Errorf("this version of LocalAI was built without the tts tag")
}
