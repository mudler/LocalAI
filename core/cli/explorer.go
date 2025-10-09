package cli

import (
	"context"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/rs/zerolog/log"
)

type ExplorerCMD struct {
	Address                  string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	PoolDatabase             string `env:"LOCALAI_POOL_DATABASE,POOL_DATABASE" default:"explorer.json" help:"Path to the pool database" group:"api"`
	ConnectionTimeout        string `env:"LOCALAI_CONNECTION_TIMEOUT,CONNECTION_TIMEOUT" default:"2m" help:"Connection timeout for the explorer" group:"api"`
	ConnectionErrorThreshold int    `env:"LOCALAI_CONNECTION_ERROR_THRESHOLD,CONNECTION_ERROR_THRESHOLD" default:"3" help:"Connection failure threshold for the explorer" group:"api"`

	WithSync bool `env:"LOCALAI_WITH_SYNC,WITH_SYNC" default:"false" help:"Enable sync with the network" group:"api"`
	OnlySync bool `env:"LOCALAI_ONLY_SYNC,ONLY_SYNC" default:"false" help:"Only sync with the network" group:"api"`
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

	if e.WithSync {
		ds := explorer.NewDiscoveryServer(db, dur, e.ConnectionErrorThreshold)
		go ds.Start(context.Background(), true)
	}

	if e.OnlySync {
		ds := explorer.NewDiscoveryServer(db, dur, e.ConnectionErrorThreshold)
		ctx := context.Background()

		return ds.Start(ctx, false)
	}

	appHTTP := http.Explorer(db)

	signals.RegisterGracefulTerminationHandler(func() {
		if err := appHTTP.Shutdown(); err != nil {
			log.Error().Err(err).Msg("error during shutdown")
		}
	})

	return appHTTP.Listen(e.Address)
}
