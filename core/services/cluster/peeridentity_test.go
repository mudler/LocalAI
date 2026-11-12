package cluster_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Peer credential", func() {
	It("mints a secret of at least 128 bits and publishes only its hash", func() {
		cred := cluster.NewPeerCredential()
		Expect(len(cred.Token())).To(BeNumerically(">=", 26), "crypto/rand.Text gives 26 characters of base32")
		Expect(cred.Hash()).To(HaveLen(64))
		Expect(cred.Hash()).ToNot(ContainSubstring(cred.Token()))
		Expect(cluster.HashPeerToken(cred.Token())).To(Equal(cred.Hash()))
	})

	It("mints a different secret each time", func() {
		Expect(cluster.NewPeerCredential().Token()).ToNot(Equal(cluster.NewPeerCredential().Token()))
	})

	It("matches the secret behind a hash and no other", func() {
		cred := cluster.NewPeerCredential()
		Expect(cluster.PeerTokenMatches(cred.Token(), cred.Hash())).To(BeTrue())
		Expect(cluster.PeerTokenMatches(cluster.NewPeerCredential().Token(), cred.Hash())).To(BeFalse())
		Expect(cluster.PeerTokenMatches(strings.ToLower(cred.Token()), cred.Hash())).To(BeFalse())
	})

	It("authorises nobody when the hash is empty, or when the token is", func() {
		Expect(cluster.PeerTokenMatches("", "")).To(BeFalse(), "an empty credential is not a credential that matches everything")
		Expect(cluster.PeerTokenMatches("anything", "")).To(BeFalse())
		Expect(cluster.PeerTokenMatches("", cluster.NewPeerCredential().Hash())).To(BeFalse())
	})

	It("is unusable in its zero value", func() {
		var zero cluster.PeerCredential
		Expect(zero.Token()).To(BeEmpty())
		Expect(zero.Hash()).To(BeEmpty())
		Expect(cluster.PeerTokenMatches(zero.Token(), zero.Hash())).To(BeFalse())
	})
})

var _ = Describe("Registering a replica that peers dial", func() {
	var (
		ctx context.Context
		reg *cluster.Registry
	)

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
	})

	It("stores the address and the hash with the rest of the row, and reads them back", func() {
		cred := cluster.NewPeerCredential()
		Expect(reg.RegisterPeer(ctx, "a", "v1", 0, "", "10.0.0.7:8080", cred.Hash())).To(Succeed())
		got, err := reg.Get(ctx, "a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.AdvertisedAddr).To(Equal("10.0.0.7:8080"))
		Expect(got.PeerTokenHash).To(Equal(cred.Hash()))
	})

	It("replaces both when the replica registers again with new ones", func() {
		Expect(reg.RegisterPeer(ctx, "a", "v1", 0, "", "10.0.0.7:8080", cluster.NewPeerCredential().Hash())).To(Succeed())
		again := cluster.NewPeerCredential()
		Expect(reg.RegisterPeer(ctx, "a", "v1", 0, "", "10.0.0.8:8080", again.Hash())).To(Succeed())
		got, err := reg.Get(ctx, "a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.AdvertisedAddr).To(Equal("10.0.0.8:8080"))
		Expect(got.PeerTokenHash).To(Equal(again.Hash()))
	})

	It("does not wipe the address or the hash when a registration carries none", func() {
		cred := cluster.NewPeerCredential()
		Expect(reg.RegisterPeer(ctx, "a", "v1", 0, "", "10.0.0.7:8080", cred.Hash())).To(Succeed())
		Expect(reg.Register(ctx, "a", "v2", 3, "reason")).To(Succeed())
		got, err := reg.Get(ctx, "a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Version).To(Equal("v2"))
		Expect(got.AdvertisedAddr).To(Equal("10.0.0.7:8080"))
		Expect(got.PeerTokenHash).To(Equal(cred.Hash()))
	})

	It("writes them again when the membership loop registers a swept replica again", func() {
		cred := cluster.NewPeerCredential()
		m := cluster.NewMembership(reg, "swept", "v1")
		m.SetPeer("10.0.0.9:8080", cred)
		Expect(m.Start(ctx)).To(Succeed())
		DeferCleanup(m.Stop)

		got, err := reg.Get(ctx, "swept")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.AdvertisedAddr).To(Equal("10.0.0.9:8080"))
		Expect(got.PeerTokenHash).To(Equal(cred.Hash()))
	})

	It("reports a replica that is not registered as such, and never as unreachable", func() {
		_, err := reg.Get(ctx, "ghost")
		Expect(err).To(MatchError(cluster.ErrInstanceNotFound))
	})
})
