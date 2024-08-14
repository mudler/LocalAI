package cli

import (
	"context"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/p2p"
)

type FederatedCLI struct {
	Address            string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	Peer2PeerToken     string `env:"LOCALAI_P2P_TOKEN,P2P_TOKEN,TOKEN" name:"p2ptoken" help:"Token for P2P mode (optional)" group:"p2p"`
	RandomWorker       bool   `env:"LOCALAI_RANDOM_WORKER,RANDOM_WORKER" default:"false" help:"Select a random worker from the pool" group:"p2p"`
	Peer2PeerNetworkID string `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode, can be set arbitrarly by the user for grouping a set of instances." group:"p2p"`
	TargetWorker       string `env:"LOCALAI_TARGET_WORKER,TARGET_WORKER" help:"Target worker to run the federated server on" group:"p2p"`
}

func (f *FederatedCLI) Run(ctx *cliContext.Context) error {

	fs := p2p.NewFederatedServer(f.Address, p2p.NetworkID(f.Peer2PeerNetworkID, p2p.FederatedID), f.Peer2PeerToken, !f.RandomWorker, f.TargetWorker)

	return fs.Start(context.Background())
}
