// Command labbin is the binary the P2P NAT lab runs inside its containers
// (see tests/e2e/p2p/lab and the README next to it). It drives the real P2P
// packages: p2pnet for the host, p2prelaymgr for the server side of a relay,
// p2ppair and pairclient for invites. The relay itself is not part of this
// binary: the lab runs the real localai-relay binary.
//
//	keygen   --key F                       create an identity, print its peer ID
//	serve    --key F ...                   a server behind a NAT with a small HTTP service
//	dial     PEER --key F ...              connect to a server and run checks
//	redeem   --link L --key F ...          redeem an invite link
//	relay-admin --key F --relay A OP ...   talk to the relay admin protocol
//	observer --listen A ...                a public peer that lets others learn their address
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	args := os.Args[2:]
	var code int
	switch os.Args[1] {
	case "keygen":
		code = cmdKeygen(args)
	case "serve":
		code = cmdServe(ctx, args)
	case "dial":
		code = cmdDial(ctx, args)
	case "redeem":
		code = cmdRedeem(ctx, args)
	case "relay-admin":
		code = cmdRelayAdmin(ctx, args)
	case "observer":
		code = cmdObserver(ctx, args)
	default:
		usage()
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: p2plab keygen|serve|dial|redeem|relay-admin|observer [flags]")
	os.Exit(2)
}

// list is a repeatable string flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

// peerArgs parses peer IDs, or exits with a message.
func peerArgs(ids []string) []p2pnet.ID {
	out := make([]p2pnet.ID, 0, len(ids))
	for _, s := range ids {
		id, err := p2pnet.ParsePeerID(s)
		if err != nil {
			fatalf("%v", err)
		}
		out = append(out, id)
	}
	return out
}

// addrInfos parses multiaddrs that end in /p2p/<peer ID>.
func addrInfos(addrs []string) []p2pnet.AddrInfo {
	out := make([]p2pnet.AddrInfo, 0, len(addrs))
	for _, s := range addrs {
		ai, err := p2pnet.ParseAddrInfo(s)
		if err != nil {
			fatalf("%v", err)
		}
		out = append(out, ai)
	}
	return out
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "lab: "+format+"\n", a...)
	os.Exit(1)
}

// logf writes a log line to standard error with a time stamp, so that a case
// can read it with the other logs of a node.
func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, a...)...)
}
