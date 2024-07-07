//go:build !p2p
// +build !p2p

package p2p

import (
	"context"
	"fmt"
)

func GenerateToken() string {
	return "not implemented"
}

func LLamaCPPRPCServerDiscoverer(ctx context.Context, token, servicesID string) error {
	return fmt.Errorf("not implemented")
}

func BindLLamaCPPWorker(ctx context.Context, host, port, token, servicesID string) error {
	return fmt.Errorf("not implemented")
}

func IsP2PEnabled() bool {
	return false
}
