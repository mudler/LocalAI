package cli

import (
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/http"
)

type ExplorerCMD struct {
	Address string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
}

func (explorer *ExplorerCMD) Run(ctx *cliContext.Context) error {
	appHTTP := http.Explorer()

	return appHTTP.Listen(explorer.Address)
}
