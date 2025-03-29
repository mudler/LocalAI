package cli

import (
	"context"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	cliP2P "github.com/mudler/LocalAI/core/cli/p2p"
	"github.com/mudler/LocalAI/core/p2p"
)

type FederatedCLI struct {
	cliP2P.P2PCommonFlags `embed:""`

	Address      string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	RandomWorker bool   `env:"LOCALAI_RANDOM_WORKER,RANDOM_WORKER" default:"false" help:"Select a random worker from the pool" group:"p2p"`
	TargetWorker string `env:"LOCALAI_TARGET_WORKER,TARGET_WORKER" help:"Target worker to run the federated server on" group:"p2p"`
}

func (f *FederatedCLI) Run(ctx *cliContext.Context) error {

	fs := p2p.NewFederatedServer(f.Address, p2p.NetworkID(f.Peer2PeerNetworkID, p2p.FederatedID), f.Peer2PeerToken, !f.RandomWorker, f.TargetWorker)

	return fs.Start(context.Background(), f.P2PCommonFlags)
}
