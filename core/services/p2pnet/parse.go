package p2pnet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// ParsePeerID parses the text form of a peer ID.
func ParsePeerID(s string) (peer.ID, error) {
	id, err := peer.Decode(s)
	if err != nil {
		return "", fmt.Errorf("p2pnet: %q is not a peer ID: %w", s, err)
	}
	return id, nil
}

// ParseMultiaddr parses a multiaddr.
func ParseMultiaddr(s string) (ma.Multiaddr, error) {
	a, err := ma.NewMultiaddr(s)
	if err != nil {
		return nil, fmt.Errorf("p2pnet: %q is not a multiaddr: %w", s, err)
	}
	return a, nil
}

// ParseAddrInfo parses a multiaddr that ends in /p2p/<peer ID>.
func ParseAddrInfo(s string) (peer.AddrInfo, error) {
	ai, err := peer.AddrInfoFromString(s)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("p2pnet: %q is not a multiaddr that ends in /p2p/<peer ID>: %w", s, err)
	}
	return *ai, nil
}

// IdentityPeerID returns the peer ID of the identity file at path. Unlike
// LoadOrCreateIdentity it never creates the file: a missing file is an error
// that wraps fs.ErrNotExist.
func IdentityPeerID(path string) (peer.ID, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is the identity file the operator chose, not a request value
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("p2pnet: no identity at %s: %w", path, err)
	}
	if err != nil {
		return "", fmt.Errorf("p2pnet: read identity: %w", err)
	}
	k, err := crypto.UnmarshalPrivateKey(b)
	if err != nil {
		return "", fmt.Errorf("p2pnet: parse identity %s: %w", path, err)
	}
	return peer.IDFromPrivateKey(k)
}

// RelaysOption parses relay multiaddrs that end in /p2p/<relay ID> and
// returns the WithRelays dial option for them.
func RelaysOption(relays []string) (DialOption, error) {
	ais, err := parseAddrInfos(relays)
	if err != nil {
		return nil, err
	}
	return WithRelays(ais...), nil
}

// HintsOption parses hint relay multiaddrs that end in /p2p/<relay ID> and
// returns the WithHints dial option for them.
func HintsOption(hints []string) (DialOption, error) {
	ais, err := parseAddrInfos(hints)
	if err != nil {
		return nil, err
	}
	return WithHints(ais...), nil
}

func parseAddrInfos(addrs []string) ([]peer.AddrInfo, error) {
	ais := make([]peer.AddrInfo, 0, len(addrs))
	for _, a := range addrs {
		ai, err := ParseAddrInfo(a)
		if err != nil {
			return nil, err
		}
		ais = append(ais, ai)
	}
	return ais, nil
}

// AddrsOption parses multiaddrs and returns the WithAddrs dial option.
func AddrsOption(addrs []string) (DialOption, error) {
	mas := make([]ma.Multiaddr, 0, len(addrs))
	for _, a := range addrs {
		m, err := ParseMultiaddr(a)
		if err != nil {
			return nil, err
		}
		mas = append(mas, m)
	}
	return WithAddrs(mas...), nil
}
