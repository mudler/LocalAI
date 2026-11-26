package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

// server is a lab server: a p2pnet host that admits a set of paired peers, the
// manager that keeps its relay in sync, the pairing service and the small HTTP
// service.
type server struct {
	host *p2pnet.Host
	gate *p2pnet.Allowlist
	mgr  p2prelaymgr.Manager
	pair *p2ppair.Service

	mu     sync.Mutex
	paired []p2pnet.ID
}

func (s *server) pairedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.paired))
	for i, p := range s.paired {
		out[i] = p.String()
	}
	return out
}

func (s *server) allow(p p2pnet.ID) {
	s.mu.Lock()
	s.paired = append(s.paired, p)
	s.mu.Unlock()
	s.gate.Allow(p)
}

func (s *server) disallow(p p2pnet.ID) {
	s.mu.Lock()
	for i, q := range s.paired {
		if q == p {
			s.paired = append(s.paired[:i], s.paired[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

func cmdServe(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	key := fs.String("key", "", "identity file")
	var allow, relays, boot, listen list
	fs.Var(&allow, "allow", "peer ID to admit (repeatable)")
	fs.Var(&relays, "relay", "relay multiaddr with /p2p/<id> (repeatable)")
	fs.Var(&boot, "bootstrap", "peer to connect to at start, for example an observer (repeatable)")
	fs.Var(&listen, "listen", "listen multiaddr (repeatable)")
	retry := fs.Duration("relay-backoff", 0, "how long to wait before asking a relay again (default: the library default)")
	doSync := fs.Bool("relay-sync", false, "add the first --relay to a relay manager, which pushes the --allow set as admin")
	revokeAfter := fs.Duration("revoke-after", 0, "revoke the first --allow peer after this time")
	relayOnly := fs.Bool("revoke-relay-only", false, "with --revoke-after: remove the peer from the relay only, not from this server")
	pairingOpen := fs.Bool("pairing-open", false, "let unpaired peers use the HTTP service, as a server does while an invite is pending")
	admitAll := fs.Bool("admit-all", false, "admit every peer, as a server behind an open relay does when nobody is paired")
	limitsAfter := fs.Duration("limits-after", 0, "change the limits of the relay after this time, as the server does for an administrator (needs --relay-sync)")
	limitBytes := fs.Int64("limit-bytes", -1, "with --limits-after: data per circuit (0 removes the limit, -1 leaves it)")
	limitSeconds := fs.Int64("limit-seconds", -1, "with --limits-after: time per circuit (0 removes the limit, -1 leaves it)")
	invite := fs.Bool("invite", false, "mint an invite link at start through the pairing service and print it; the relay of --relay-sync is its carrier, the --public-relay relays its hints")
	invitePass := fs.Bool("invite-pass", true, "with --invite: register a relay pass for the link")
	var public list
	fs.Var(&public, "public-relay", "a relay to hold a reservation on that the relay manager does not own, as AutoRelay does with a public relay it found (repeatable)")
	pairDir := fs.String("pair-dir", "", "keep invites and devices in this folder, so that they outlive the process (default: in memory)")
	_ = fs.Parse(args)
	if *key == "" {
		fatalf("serve: --key is required")
	}

	s := &server{gate: p2pnet.NewAllowlist()}
	s.gate.SetPairing(*pairingOpen)
	var gate p2pnet.Gate = s.gate
	if *admitAll {
		gate = openGate{streams: true}
	}
	for _, p := range peerArgs(allow) {
		s.allow(p)
	}
	h, err := p2pnet.New(ctx, p2pnet.Config{
		IdentityPath:   *key,
		ListenAddrs:    append([]string{}, listen...),
		BootstrapPeers: addrInfos(boot),
		StaticRelays:   addrInfos(append(append([]string{}, relays...), public...)),
		RelayRetry:     *retry,
		Gate:           gate,
	})
	if err != nil {
		fatalf("serve: %v", err)
	}
	defer func() { _ = h.Close() }()
	s.host = h
	started := time.Now()

	fmt.Println("PEERID", h.ID())
	if err := s.serveHTTP(); err != nil {
		fatalf("serve: %v", err)
	}
	if err := s.serveHalfClose(); err != nil {
		fatalf("serve: %v", err)
	}
	if *doSync {
		if len(relays) == 0 {
			fatalf("serve: --relay-sync needs --relay")
		}
		if err := s.startRelayManager(ctx, relays[0]); err != nil {
			fatalf("serve: %v", err)
		}
	}
	if err := s.startPairing(ctx, *pairDir, *invitePass); err != nil {
		fatalf("serve: %v", err)
	}
	if *invite {
		if err := s.mintInvite(ctx, len(public) > 0); err != nil {
			fatalf("serve: invite: %v", err)
		}
	}
	if *revokeAfter > 0 {
		go func() {
			select {
			case <-time.After(*revokeAfter):
				s.revoke(peerArgs(allow)[0], *relayOnly)
			case <-ctx.Done():
			}
		}()
	}
	if *limitsAfter > 0 {
		go func() {
			select {
			case <-time.After(*limitsAfter):
				s.setLimits(ctx, *limitBytes, *limitSeconds)
			case <-ctx.Done():
			}
		}()
	}
	go announceRelayAddr(ctx, h, started)
	<-ctx.Done()
	if s.mgr != nil {
		_ = s.mgr.Close()
	}
	return 0
}

// announceRelayAddr prints the first relay address the host gets and how long
// it took.
func announceRelayAddr(ctx context.Context, h *p2pnet.Host, started time.Time) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, a := range h.Addrs() {
			if strings.Contains(a.String(), "/p2p-circuit") {
				fmt.Println("FIRST_RELAY_ADDR", time.Since(started).Round(time.Millisecond), a)
				return
			}
		}
	}
}

func (s *server) startRelayManager(ctx context.Context, relay string) error {
	mgr, err := p2prelaymgr.New(p2prelaymgr.Config{Host: s.host, PairedPeers: s.pairedIDs})
	if err != nil {
		return err
	}
	if _, err := mgr.Add(ctx, relay, "lab", ""); err != nil {
		return err
	}
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	s.mgr = mgr
	return nil
}

// revoke does what the server does when an administrator revokes a device:
// stop admitting it, close its connections and tell the relay.
func (s *server) revoke(p p2pnet.ID, relayOnly bool) {
	if !relayOnly {
		s.disallow(p)
		s.host.Revoke(p)
	}
	if s.mgr != nil {
		s.mgr.PeerRemoved(p.String())
	}
	fmt.Println("REVOKED", p)
}

// setLimits changes the limits of the server's relay through the manager,
// which is what the server does when an administrator saves new limits.
func (s *server) setLimits(ctx context.Context, bytes, seconds int64) {
	var patch p2prelaymgr.LimitsPatch
	if bytes >= 0 {
		patch.CircuitBytes = &bytes
	}
	if seconds >= 0 {
		patch.CircuitSeconds = &seconds
	}
	info, err := s.mgr.SetLimits(ctx, s.mgr.List()[0].ID, patch)
	if err != nil {
		fmt.Printf("LIMITS_SET err=%q\n", err)
		return
	}
	fmt.Printf("LIMITS_SET ok bytes=%d seconds=%d restarted=%v\n", info.Limits.CircuitBytes, info.Limits.CircuitSeconds, info.Restarted)
}

// serveHTTP serves the HTTP service and the redeem route on ProtoHTTP.
func (s *server) serveHTTP() error {
	l, err := s.host.Listen(p2pnet.ProtoHTTP, nil)
	if err != nil {
		return err
	}
	mux := appHandler()
	mux.HandleFunc("POST /api/p2p/redeem", s.redeem)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if p, ok := p2pnet.PeerOf(c); ok {
				return context.WithValue(ctx, peerKey{}, p)
			}
			return ctx
		},
	}
	go func() { _ = srv.Serve(l) }()
	return nil
}

type peerKey struct{}

// serveHalfClose answers a stream that the client half-closed.
func (s *server) serveHalfClose() error {
	l, err := s.host.Listen(protoHalfClose, nil)
	if err != nil {
		return err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				n, _ := io.Copy(io.Discard, c)
				_, _ = fmt.Fprintf(c, "got %d bytes", n)
				_ = c.Close()
			}()
		}
	}()
	return nil
}
