package cli

import (
	"context"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http"
)

type ExplorerCMD struct {
	Address      string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	PoolDatabase string `env:"LOCALAI_POOL_DATABASE,POOL_DATABASE" default:"explorer.json" help:"Path to the pool database" group:"api"`
}

func (e *ExplorerCMD) Run(ctx *cliContext.Context) error {

	db, err := explorer.NewDatabase(e.PoolDatabase)
	if err != nil {
		return err
	}
	appHTTP := http.Explorer(db)

	ds := explorer.NewDiscoveryServer(db)

	go ds.Start(context.Background())

	return appHTTP.Listen(e.Address)
}
