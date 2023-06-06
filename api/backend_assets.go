package api

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/rs/zerolog/log"
)

func PrepareBackendAssets(backendAssets embed.FS, dst string) error {

	// Extract files from the embedded FS
	err := assets.ExtractFiles(backendAssets, dst)
	if err != nil {
		return err
	}

	// Set GPT4ALL libs where we extracted the files
	// https://github.com/nomic-ai/gpt4all/commit/27e80e1d10985490c9fd4214e4bf458cfcf70896
	gpt4alldir := filepath.Join(dst, "backend-assets", "gpt4all")
	os.Setenv("GPT4ALL_IMPLEMENTATIONS_PATH", gpt4alldir)
	log.Debug().Msgf("GPT4ALL_IMPLEMENTATIONS_PATH: %s", gpt4alldir)

	return nil
}
