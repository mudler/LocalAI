package pairclient_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

var _ = Describe("Redeem", func() {
	// A link with no address and no relay cannot be dialled by a host that
	// runs no discovery. The error must say so, so that the caller does not
	// advise a relay when the link has nothing to dial.
	It("says so when the link names no address and no relay", func() {
		_, pub, err := crypto.GenerateEd25519Key(nil)
		Expect(err).ToNot(HaveOccurred())
		server, err := peer.IDFromPublicKey(pub)
		Expect(err).ToNot(HaveOccurred())
		h, err := p2pnet.New(context.Background(), p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"), ListenAddrs: []string{},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(h.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		l := &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: server, Code: "ABCDE23456", Expires: time.Now().Add(time.Hour)}
		_, err = pairclient.Redeem(ctx, h, l, pairclient.Request{Name: "x"})
		Expect(err).To(MatchError(pairclient.ErrUnreachable))
		Expect(err).To(MatchError(pairclient.ErrNoRoute))
	})
})
