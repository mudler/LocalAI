package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// cmdRedeem redeems an invite link the way a device does, then dials the
// server once more with the options of the link.
func cmdRedeem(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("redeem", flag.ExitOnError)
	var nf nodeFlags
	nf.add(fs)
	link := fs.String("link", "", "the invite link")
	timeout := fs.Duration("timeout", 60*time.Second, "overall timeout")
	warmup := fs.Duration("warmup", 0, "wait this long after start before the redeem")
	_ = fs.Parse(args)
	l, err := p2ppair.ParseLink(*link)
	if err != nil {
		fatalf("%v", err)
	}
	h := nf.host(ctx, l.Peer)
	defer func() { _ = h.Close() }()
	ctx, cancel := context.WithTimeout(ctx, *timeout+*warmup)
	defer cancel()
	time.Sleep(*warmup)
	res, err := pairclient.Redeem(ctx, h, l, pairclient.Request{Name: "lab", Platform: "linux"})
	if err != nil {
		fmt.Printf("REDEEM fail err=%q\n", err)
		return 1
	}
	fmt.Printf("REDEEM ok phrase=%q server=%s\n", res.Phrase, res.Server.PeerID)
	if !probe(ctx, h, l.Peer, pairclient.DialOptions(l)) {
		return 1
	}
	fmt.Println("PATH", pathName(h.Path(l.Peer)))
	return 0
}
