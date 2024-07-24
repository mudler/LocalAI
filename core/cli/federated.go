package cli

import (
	"context"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/p2p"
)

type FederatedCLI struct {
	Address        string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	Peer2PeerToken string `env:"LOCALAI_P2P_TOKEN,P2P_TOKEN,TOKEN" name:"p2ptoken" help:"Token for P2P mode (optional)" group:"p2p"`
	LoadBalanced   bool   `env:"LOCALAI_LOAD_BALANCED,LOAD_BALANCED" default:"false" help:"Enable load balancing" group:"p2p"`
}

func (f *FederatedCLI) Run(ctx *cliContext.Context) error {

	fs := p2p.NewFederatedServer(f.Address, p2p.FederatedID, f.Peer2PeerToken, f.LoadBalanced)

	return fs.Start(context.Background())
}
