package main

import (
	"flag"
	"fmt"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func cmdKeygen(args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	key := fs.String("key", "", "identity file (created when missing)")
	_ = fs.Parse(args)
	if *key == "" {
		fatalf("keygen: --key is required")
	}
	if _, err := p2pnet.LoadOrCreateIdentity(*key); err != nil {
		fatalf("keygen: %v", err)
	}
	id, err := p2pnet.IdentityPeerID(*key)
	if err != nil {
		fatalf("keygen: %v", err)
	}
	fmt.Println(id)
	return 0
}
