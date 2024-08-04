package cli

import (
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/http"
	"github.com/rs/zerolog/log"
)

type ExplorerCMD struct {
	Address string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
}

func (explorer *ExplorerCMD) Run(ctx *cliContext.Context) error {
	appHTTP, err := http.App(cl, ml, options)
	if err != nil {
		log.Error().Err(err).Msg("error during HTTP App construction")
		return err
	}

	return appHTTP.Listen(explorer.Address)
}
