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

// Routes returns the current routes to the server for the device of peer p.
// They are those of a link minted now with relays "auto": the direct
// addresses, every gated relay the server knows and the hints. That is more
// than the device's invite named when the invite chose its relays or named
// none: the server's gated relays are not secret from a paired device. The
// local network addresses are left out when the invite of the device left
// them out (MintRequest.OmitLANAddrs).
func (s *Service) Routes(ctx context.Context, p peer.ID) (Routes, error) {
	omitLAN, err := s.omitsLANAddrs(ctx, p)
	if err != nil {
		return Routes{}, err
	}
	relays, err := s.selectRelays(ctx, MintRequest{Relays: RelayAuto})
	if err != nil {
		return Routes{}, err
	}
	var r Routes
	r.Addrs, _ = s.directAddrs(omitLAN)
	r.Relays, _ = relayInfos(relays)
	r.Hints = s.relayHints(ctx, r.Relays)
	return r, nil
}

// omitsLANAddrs reports whether the active device of p was paired with an
// invite that left the local network addresses out.
func (s *Service) omitsLANAddrs(ctx context.Context, p peer.ID) (bool, error) {
	devs, err := s.st.ListDevices(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range devs {
		if d.PeerID == p.String() && d.Status() == DeviceActive {
			return d.OmitLANAddrs, nil
		}
	}
	return false, nil
}
