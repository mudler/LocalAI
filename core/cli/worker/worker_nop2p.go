//go:build !p2p
// +build !p2p

package worker

import (
	"fmt"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
)

type P2P struct{}

func (r *P2P) Run(ctx *cliContext.Context) error {
	return fmt.Errorf("p2p mode is not enabled in this build")
}
