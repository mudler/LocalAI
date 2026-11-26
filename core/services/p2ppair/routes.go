package p2ppair

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// Routes are the ways a paired device reaches the server now: the lists of
// an invite link, without the code and the pass. A device asks for them
// while it is connected, so it can still find the server after a relay
// reservation moves or an address changes.
type Routes struct {
	// Addrs are the direct addresses: public ones, then local network ones,
	// as in a link.
	Addrs []ma.Multiaddr
	// Relays are the relays that carry data for a paired device: the gated
	// relays the server knows. Open relays are left out, as a link minted
	// with relays "auto" leaves them out.
	Relays []peer.AddrInfo
	// Hints are public relays for hole punch coordination, as in a link.
	Hints []peer.AddrInfo
}

// Routes returns the current routes to the server. They reveal nothing a
// paired device did not get in its invite, except the changes since.
func (s *Service) Routes(ctx context.Context) (Routes, error) {
	relays, err := s.selectRelays(ctx, MintRequest{Relays: RelayAuto})
	if err != nil {
		return Routes{}, err
	}
	var r Routes
	r.Addrs, _ = s.directAddrs(false)
	r.Relays, _ = relayInfos(relays)
	r.Hints = s.relayHints(ctx, r.Relays)
	return r, nil
}
