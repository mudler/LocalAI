package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

func cmdRelayAdmin(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("relay-admin", flag.ExitOnError)
	var nf nodeFlags
	nf.add(fs)
	relay := fs.String("relay", "", "relay multiaddr with /p2p/<id>")
	code := fs.String("code", "", "pass: the pass code")
	ttl := fs.Duration("ttl", 5*time.Minute, "pass: how long it lasts")
	expect := fs.String("expect", "", "check: the peer ID the relay should hold as the paired set")
	bytes := fs.Int64("bytes", -1, "limits: data per circuit (0 removes the limit, -1 leaves it)")
	seconds := fs.Int64("seconds", -1, "limits: time per circuit (0 removes the limit, -1 leaves it)")
	rate := fs.Int64("rate", -1, "limits: bytes per second per circuit (0 removes the limit, -1 leaves it)")
	// The operation comes after the common flags; its own flags and
	// arguments follow it: relay-admin --key K --relay A add ID.
	_ = fs.Parse(args)
	if fs.NArg() == 0 || *relay == "" {
		fatalf("relay-admin: need --relay and an operation (status|list|add|remove|pass|limits|check)")
	}
	op := fs.Arg(0)
	_ = fs.Parse(fs.Args()[1:])
	ai := addrInfos([]string{*relay})[0]
	h := nf.host(ctx)
	defer func() { _ = h.Close() }()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := h.Libp2p().Connect(cctx, ai); err != nil {
		fmt.Printf("ok=false err=%q\n", "cannot connect: "+err.Error())
		return 1
	}
	if op == "check" {
		return adminCheck(cctx, h, *relay, *expect)
	}

	req := relayproto.Request{Op: op}
	switch op {
	case "status", "list":
	case "add", "remove":
		for _, id := range fs.Args() {
			req.Peers = append(req.Peers, relayproto.Peer{ID: id})
		}
	case "pass":
		req.Op = relayproto.OpPass
		req.Pass = &relayproto.Pass{Code: *code, TTLSeconds: int(ttl.Seconds()), Label: "lab"}
	case "limits":
		req.Limits = &relayproto.LimitsPatch{}
		for _, f := range []struct {
			v   int64
			dst **int64
		}{{*bytes, &req.Limits.CircuitMaxBytes}, {*seconds, &req.Limits.CircuitMaxSeconds}, {*rate, &req.Limits.RateBPS}} {
			if f.v >= 0 {
				v := f.v
				*f.dst = &v
			}
		}
		if req.Limits.CircuitMaxBytes == nil && req.Limits.CircuitMaxSeconds == nil && req.Limits.RateBPS == nil {
			req.Limits = nil
		}
	default:
		fatalf("relay-admin: unknown operation %q", op)
	}
	resp, _ := relayproto.Call(cctx, h.Libp2p(), ai.ID, req)
	fmt.Printf("ok=%v err=%q count=%d\n", resp.OK, resp.Err, resp.Count)
	for _, p := range resp.Peers {
		fmt.Printf("PEER %s\n", p.ID)
	}
	if st := resp.Status; st != nil {
		fmt.Printf("STATUS mode=%s admins=%d members=%d reservations=%d circuits=%d denied=%d limit_bytes=%d limit_seconds=%d rate_bps=%d\n",
			st.Mode, st.Admins, st.Members, st.Reserv, st.Circuits, st.Denied, st.CircuitMaxBytes, st.CircuitMaxSeconds, st.RateBPS)
	}
	if l := resp.Limits; l != nil {
		fmt.Printf("LIMITS bytes=%d seconds=%d rate_bps=%d restarted=%v\n", l.CircuitMaxBytes, l.CircuitMaxSeconds, l.RateBPS, l.Restarted)
	}
	if !resp.OK {
		return 1
	}
	return 0
}

// adminCheck runs the relay check of a server, as the interface does, and
// prints each step. expect is the paired set the server claims to have.
func adminCheck(ctx context.Context, h *p2pnet.Host, relay, expect string) int {
	mgr, err := p2prelaymgr.New(p2prelaymgr.Config{Host: h, PairedPeers: func() []string {
		if expect == "" {
			return nil
		}
		return []string{expect}
	}})
	if err != nil {
		fatalf("%v", err)
	}
	defer func() { _ = mgr.Close() }()
	r, err := mgr.Add(ctx, relay, "lab", "")
	if err != nil {
		fatalf("%v", err)
	}
	c, err := mgr.Check(ctx, r.ID)
	if err != nil {
		fatalf("%v", err)
	}
	for _, d := range c.Detail {
		fmt.Println("CHECK", d)
	}
	fmt.Printf("STATE %s mode=%s\n", c.Relay.State, c.Relay.Mode)
	return 0
}

// cmdObserver runs a public peer that admits every connection. Peers behind a
// NAT connect to four of them to learn their public address, which a hole
// punch needs.
func cmdObserver(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("observer", flag.ExitOnError)
	key := fs.String("key", "/root/observer.key", "identity file")
	var listen list
	fs.Var(&listen, "listen", "listen multiaddr (repeatable)")
	_ = fs.Parse(args)
	h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: *key, ListenAddrs: append([]string{}, listen...), Gate: openGate{}})
	if err != nil {
		fatalf("observer: %v", err)
	}
	defer func() { _ = h.Close() }()
	fmt.Println("PEERID", h.ID())
	<-ctx.Done()
	return 0
}

// openGate admits every connection. It admits streams only when streams is
// set: an observer needs none, a server behind an open relay serves anyone.
type openGate struct{ streams bool }

func (openGate) AdmitConn(p2pnet.ID) bool { return true }

func (g openGate) AdmitStream(p2pnet.ID, p2pnet.Protocol) bool { return g.streams }
