package config

import (
	"fmt"
	"path/filepath"

	gguf "github.com/thxcode/gguf-parser-go"
)

func guessTemplate(cfg *BackendConfig, modelPath string) {

	if modelPath == "" {
		return
	}

	// We try to guess only if we don't have a template defined already+
	f, err := gguf.ParseGGUFFile(filepath.Join(modelPath, cfg.ModelFileName()))
	if err != nil {
		// Only valid for gguf files
		return
	}

	fmt.Println(f.Architecture().Architecture)
	fmt.Println("Model name", f.Model().Name)
	fmt.Printf("%+v\n", f.Model())
	fmt.Println("EOS Token", f.Tokenizer().EOSTokenID)

	fmt.Println(f.Tokenizer())

	if cfg.Name == "" {
		cfg.Name = f.Model().Name
	}

	if cfg.HasTemplate() {
		return
	}

	panic("foo")

}
