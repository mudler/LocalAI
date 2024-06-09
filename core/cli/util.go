package cli

import (
	"fmt"

	"github.com/rs/zerolog/log"

	cliContext "github.com/go-skynet/LocalAI/core/cli/context"
	gguf "github.com/thxcode/gguf-parser-go"
)

type UtilCMD struct {
	GGUFInfo GGUFInfoCMD `cmd:"" name:"gguf-info" help:"Get information about a GGUF file"`
}

type GGUFInfoCMD struct {
	Args []string `arg:"" optional:"" name:"args" help:"Arguments to pass to the utility command"`
}

func (u *GGUFInfoCMD) Run(ctx *cliContext.Context) error {
	if u.Args == nil || len(u.Args) == 0 {
		return fmt.Errorf("no GGUF file provided")
	}
	// We try to guess only if we don't have a template defined already
	f, err := gguf.ParseGGUFFile(u.Args[0])
	if err != nil {
		// Only valid for gguf files
		log.Error().Msgf("guessDefaultsFromFile: %s", "not a GGUF file")
		return err
	}

	log.Info().
		Any("eosTokenID", f.Tokenizer().EOSTokenID).
		Any("bosTokenID", f.Tokenizer().BOSTokenID).
		Any("modelName", f.Model().Name).
		Any("architecture", f.Architecture().Architecture).Msgf("GGUF file loaded: %s", u.Args[0])

	return nil
}
