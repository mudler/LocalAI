package p2ppair_test

import (
	"crypto/rand"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestP2PPair(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "P2P pairing test suite")
}

// newPeer returns a fresh peer ID.
func newPeer() peer.ID {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	id, err := peer.IDFromPrivateKey(priv)
	Expect(err).NotTo(HaveOccurred())
	return id
}

func mustDecode(s string) peer.ID {
	id, err := peer.Decode(s)
	Expect(err).NotTo(HaveOccurred())
	return id
}
