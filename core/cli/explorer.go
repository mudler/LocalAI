package cli

import (
	"context"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http"
)

type ExplorerCMD struct {
	Address                  string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	PoolDatabase             string `env:"LOCALAI_POOL_DATABASE,POOL_DATABASE" default:"explorer.json" help:"Path to the pool database" group:"api"`
	ConnectionTimeout        string `env:"LOCALAI_CONNECTION_TIMEOUT,CONNECTION_TIMEOUT" default:"2m" help:"Connection timeout for the explorer" group:"api"`
	ConnectionErrorThreshold int    `env:"LOCALAI_CONNECTION_ERROR_THRESHOLD,CONNECTION_ERROR_THRESHOLD" default:"3" help:"Connection failure threshold for the explorer" group:"api"`
}

func (e *ExplorerCMD) Run(ctx *cliContext.Context) error {

	db, err := explorer.NewDatabase(e.PoolDatabase)
	if err != nil {
		return err
	}

	dur, err := time.ParseDuration(e.ConnectionTimeout)
	if err != nil {
		return err
	}
	ds := explorer.NewDiscoveryServer(db, dur, e.ConnectionErrorThreshold)

	go ds.Start(context.Background())
	appHTTP := http.Explorer(db, ds)

	return appHTTP.Listen(e.Address)
}
