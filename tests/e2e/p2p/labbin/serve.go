package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
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
// manager that keeps its relay in sync, and the small HTTP service.
type server struct {
	host *p2pnet.Host
	gate *p2pnet.Allowlist
	mgr  p2prelaymgr.Manager

	mu     sync.Mutex
	paired []p2pnet.ID

	invite *invite
}

// invite is a pending invite: one code, used once.
type invite struct {
	code string
	used bool
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
	invite := fs.Bool("invite", false, "mint an invite link at start (needs --relay-sync) and print it")
	invitePass := fs.Bool("invite-pass", true, "with --invite: register a relay pass for the link")
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
		StaticRelays:   addrInfos(relays),
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
	if *invite {
		if err := s.mintInvite(ctx, relays, *invitePass); err != nil {
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

// mintInvite makes an invite link like the pairing service does: a one-time
// code, the relay of the server, and a relay pass registered on that relay.
// Pairing opens while the invite is pending.
func (s *server) mintInvite(ctx context.Context, relays []string, withPass bool) error {
	if s.mgr == nil || len(relays) == 0 {
		return fmt.Errorf("--invite needs --relay-sync")
	}
	code, err := newInviteCode()
	if err != nil {
		return err
	}
	ai := addrInfos(relays[:1])[0]
	l := &p2ppair.Link{
		V: 1, Kind: p2ppair.KindDevice, Peer: s.host.ID(), Code: code, Label: "lab",
		Expires: time.Now().Add(10 * time.Minute), Relays: []p2pnet.AddrInfo{ai},
	}
	if withPass {
		rl := s.mgr.List()[0]
		addr, err := p2pnet.ParseMultiaddr(rl.Addr)
		if err != nil {
			return err
		}
		pass, err := p2prelaymgr.NewPassRegistrar(s.mgr).RegisterPass(ctx, p2ppair.PassRequest{
			Relay: p2ppair.Relay{ID: rl.ID, Label: rl.Label, Addr: addr}, InviteID: "lab", Code: code,
			TTL: 10 * time.Minute, Label: "lab",
		})
		if err != nil {
			return fmt.Errorf("register the relay pass: %w", err)
		}
		l.Pass = pass
	}
	s.invite = &invite{code: code}
	s.gate.SetPairing(true)
	fmt.Println("INVITE", l.String())
	return nil
}

// inviteAlphabet is the alphabet of invite codes: no 0, O, 1, I or L.
const inviteAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// newInviteCode returns a code of ten characters, the length that the link
// format expects.
func newInviteCode() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = inviteAlphabet[int(b[i])%len(inviteAlphabet)]
	}
	return string(b[:]), nil
}

// redeem is the server side of pairing: the code works once, and the peer that
// presents it becomes a paired device.
func (s *server) redeem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, _ := r.Context().Value(peerKey{}).(p2pnet.ID)
	s.mu.Lock()
	inv := s.invite
	ok := inv != nil && !inv.used && subtle.ConstantTimeCompare([]byte(inv.code), []byte(req.Code)) == 1
	if ok {
		inv.used = true
	}
	s.mu.Unlock()
	if !ok || p == "" {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	s.allow(p)
	s.gate.SetPairing(false)
	if s.mgr != nil {
		s.mgr.PeerAdded(p.String())
	}
	fmt.Println("REDEEMED", p)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"device_id": "lab-device", "kind": "device", "phrase": "lab phrase",
		"server": map[string]string{"peer_id": s.host.ID().String()},
	})
}
