package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

// maxHints is the number of hint relays a link may carry; the pairing service
// applies the same cap.
const maxHints = 2

// startPairing opens the pairing service of the server, as mode share does:
// a JSON store, the public direct addresses of the host, the relays of the
// manager as carriers (with relay passes when withPass is set) and the
// public relays of the host as hints. Pairing stays open while an invite is
// pending, also one minted by an earlier run with the same dir.
func (s *server) startPairing(ctx context.Context, dir string, withPass bool) error {
	st, err := p2ppair.NewJSONStore(dir, "lab")
	if err != nil {
		return err
	}
	cfg := p2ppair.Config{
		ServerPeer: s.host.ID(),
		Addrs:      s.publicAddrs,
		Hints:      s.relayHints,
	}
	if s.mgr != nil {
		cfg.Relays = managerRelays{s.mgr}
		if withPass {
			cfg.Registrar = p2prelaymgr.NewPassRegistrar(s.mgr)
		}
	}
	if s.pair, err = p2ppair.NewService(st, cfg); err != nil {
		return err
	}
	if s.pendingInvites(ctx) > 0 {
		s.gate.SetPairing(true)
	}
	go s.printInvitesOnSignal(ctx)
	return nil
}

// mintInvite mints a device invite through the pairing service and prints
// its link and warnings. It waits first until what the link needs is known:
// the mode of the managed relay and, with public relays, a reservation on one
// of them. A link minted without them would be valid but would test nothing.
func (s *server) mintInvite(ctx context.Context, wantHints bool) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		ready := !wantHints || len(s.relayHints()) > 0
		if s.mgr != nil && len(managerRelays{s.mgr}.Relays(ctx)) == 0 {
			ready = false
		}
		if ready {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	m, err := s.pair.Mint(ctx, p2ppair.MintRequest{Kind: p2ppair.KindDevice, Label: "lab", CreatedBy: "lab"})
	if err != nil {
		return err
	}
	s.gate.SetPairing(true)
	warns := make([]string, 0, len(m.Warnings))
	for _, w := range m.Warnings {
		warns = append(warns, string(w))
	}
	fmt.Printf("INVITE_WARNINGS %s\n", strings.Join(warns, ","))
	fmt.Println("INVITE", m.Link.String())
	return nil
}

// pendingInvites counts the invites that can still be redeemed.
func (s *server) pendingInvites(ctx context.Context) int {
	invs, err := s.pair.ListInvites(ctx)
	if err != nil {
		logf("list invites: %v", err)
		return 0
	}
	n := 0
	for _, inv := range invs {
		if inv.Status(time.Now()) == p2ppair.InvitePending {
			n++
		}
	}
	return n
}

// printInvitesOnSignal prints an INVITE_STATUS line for each invite when the
// process gets SIGUSR1, so that a case can read the state of the store.
func (s *server) printInvitesOnSignal(ctx context.Context) {
	// Without a signal, signal.Notify would relay every signal.
	if inviteStatusSignal == nil {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, inviteStatusSignal)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		}
		invs, err := s.pair.ListInvites(ctx)
		if err != nil {
			fmt.Printf("INVITE_STATUS err=%q\n", err)
			continue
		}
		for _, inv := range invs {
			fmt.Println("INVITE_STATUS", inv.ID, inv.Status(time.Now()))
		}
	}
}

// publicAddrs are the direct addresses a link carries: the public ones.
func (s *server) publicAddrs() []p2pnet.Multiaddr {
	var out []p2pnet.Multiaddr
	for _, a := range s.host.Addrs() {
		if p2pnet.IsPublicAddr(a) && !strings.Contains(a.String(), "/p2p-circuit") {
			out = append(out, a)
		}
	}
	return out
}

// relayHints are the relays of the circuit addresses of the host that the
// manager does not own, one transport address each, TCP first. It is a short
// copy of what mode share does; the pairing service applies the rules of the
// link (cap, carriers, length).
func (s *server) relayHints() []p2pnet.AddrInfo {
	carriers := map[string]bool{}
	if s.mgr != nil {
		for _, r := range s.mgr.List() {
			carriers[r.ID] = true
		}
	}
	var out []p2pnet.AddrInfo
	for _, a := range s.host.Addrs() {
		str := a.String()
		i := strings.Index(str, "/p2p-circuit")
		if i <= 0 {
			continue
		}
		ai, err := p2pnet.ParsePeerAddr(str[:i])
		if err != nil || len(ai.Addrs) != 1 || carriers[ai.ID.String()] {
			continue
		}
		tcp := strings.Contains(ai.Addrs[0].String(), "/tcp/")
		j := -1
		for k := range out {
			if out[k].ID == ai.ID {
				j = k
			}
		}
		switch {
		case j < 0 && len(out) < maxHints:
			out = append(out, *ai)
		case j >= 0 && tcp && !strings.Contains(out[j].Addrs[0].String(), "/tcp/"):
			out[j].Addrs = ai.Addrs
		}
	}
	return out
}

// managerRelays is the relay source of the pairing service: the relays of the
// manager whose mode is known, as mode share lists them.
type managerRelays struct{ m p2prelaymgr.Manager }

func (r managerRelays) Relays(context.Context) []p2ppair.Relay {
	var out []p2ppair.Relay
	for _, rl := range r.m.List() {
		if rl.Mode != "gated" && rl.Mode != "open" {
			continue
		}
		addr, err := p2pnet.ParseMultiaddr(rl.Addr)
		if err != nil {
			continue
		}
		out = append(out, p2ppair.Relay{ID: rl.ID, Label: rl.Label, Addr: addr, Open: rl.Mode == "open"})
	}
	return out
}

// redeem is the server side of pairing: the pairing service checks the code,
// and the peer that presents it becomes a paired device.
func (s *server) redeem(w http.ResponseWriter, r *http.Request) {
	var req p2ppair.RedeemRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, _ := r.Context().Value(peerKey{}).(p2pnet.ID)
	res, err := s.pair.Redeem(r.Context(), p, req)
	switch {
	case errors.Is(err, p2ppair.ErrInviteInvalid):
		http.Error(w, "gone", http.StatusGone)
		return
	case errors.Is(err, p2ppair.ErrRateLimited):
		http.Error(w, "too many tries", http.StatusTooManyRequests)
		return
	case errors.Is(err, p2ppair.ErrPeerPaired):
		http.Error(w, "this device is already paired", http.StatusConflict)
		return
	case errors.Is(err, p2ppair.ErrMalformed):
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	case err != nil:
		logf("redeem: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.allow(p)
	s.gate.SetPairing(s.pendingInvites(r.Context()) > 0)
	if s.mgr != nil {
		s.mgr.PeerAdded(p.String())
	}
	fmt.Println("REDEEMED", p)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"device_id": res.Device.ID, "kind": res.Kind, "phrase": res.Phrase,
		"server": map[string]string{"peer_id": s.host.ID().String()},
	})
}
