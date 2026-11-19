package p2prelay

import (
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

func newID() peer.ID {
	_, pub, err := crypto.GenerateEd25519Key(nil)
	Expect(err).ToNot(HaveOccurred())
	id, err := peer.IDFromPublicKey(pub)
	Expect(err).ToNot(HaveOccurred())
	return id
}

func peers(ids ...peer.ID) []relayproto.Peer {
	out := make([]relayproto.Peer, 0, len(ids))
	for _, id := range ids {
		out = append(out, relayproto.Peer{ID: id.String()})
	}
	return out
}

var _ = Describe("gate", func() {
	var (
		cfg   Config
		g     *gate
		admin peer.ID
		now   time.Time
	)

	BeforeEach(func() {
		admin = newID()
		cfg = DefaultConfig(false)
		cfg.DataDir = GinkgoT().TempDir()
		cfg.Admins = []peer.ID{admin}
		var err error
		g, err = newGate(&cfg)
		Expect(err).ToNot(HaveOccurred())
		now = time.Now()
	})

	op := func(o string, s int64, ids ...peer.ID) relayproto.Response {
		resp, _ := g.apply(admin, relayproto.Request{Op: o, Seq: s, Peers: peers(ids...)}, now)
		return resp
	}

	It("refuses a mutation without seq and an older seq, and accepts an equal one", func() {
		m := newID()
		Expect(op(relayproto.OpAdd, 0, m).Err).To(Equal("seq required"))
		Expect(op(relayproto.OpAdd, 10, m).OK).To(BeTrue())
		resp := op(relayproto.OpRemove, 9, m)
		Expect(resp.OK).To(BeFalse())
		Expect(resp.Err).To(ContainSubstring("stale seq"))
		Expect(resp.Seq).To(BeEquivalentTo(10))
		Expect(op(relayproto.OpAdd, 10, m).OK).To(BeTrue())
		Expect(g.admitted(m)).To(BeTrue())
	})

	It("reports the digest of the permanent members", func() {
		a, b := newID(), newID()
		resp := op(relayproto.OpReplace, 1, a, b)
		Expect(resp.Count).To(Equal(2))
		Expect(resp.Digest).To(Equal(relayproto.Digest([]peer.ID{b, a})))
	})

	It("returns the removed members for disconnection, never an admin", func() {
		a := newID()
		op(relayproto.OpAdd, 1, a, admin)
		_, drop := g.apply(admin, relayproto.Request{Op: relayproto.OpReplace, Seq: 2}, now)
		Expect(drop).To(ConsistOf(a))
		Expect(g.admitted(a)).To(BeFalse())
	})

	It("caps the size of a set", func() {
		g.maxPerSet = 2
		resp := op(relayproto.OpAdd, 1, newID(), newID(), newID())
		Expect(resp.Err).To(ContainSubstring("too many peers"))
		Expect(resp.MaxPeers).To(Equal(2), "the admin learns the size it must fit")
		Expect(op(relayproto.OpStatus, 0).MaxPeers).To(Equal(2))
	})

	It("refuses operations of a peer that is not an admin", func() {
		resp, _ := g.apply(newID(), relayproto.Request{Op: relayproto.OpList}, now)
		Expect(resp.Err).To(Equal("not an admin"))
	})

	It("persists the state with mode 0600 and loads it again", func() {
		m := newID()
		op(relayproto.OpAdd, 5, m)
		fi, err := os.Stat(filepath.Join(cfg.DataDir, stateFile))
		Expect(err).ToNot(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		g2, err := newGate(&cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(g2.admitted(m)).To(BeTrue())
		resp, _ := g2.apply(admin, relayproto.Request{Op: relayproto.OpStatus}, now)
		Expect(resp.Seq).To(BeEquivalentTo(5))
	})

	It("keeps the memory as the disk is when the state cannot be saved", func() {
		m, n := newID(), newID()
		Expect(op(relayproto.OpAdd, 1, m).OK).To(BeTrue())
		g.path = filepath.Join(cfg.DataDir, "missing", stateFile)
		resp, drop := g.apply(admin, relayproto.Request{Op: relayproto.OpReplace, Seq: 2, Peers: peers(n)}, now)
		Expect(resp.OK).To(BeFalse())
		Expect(drop).To(BeEmpty())
		Expect(g.admitted(m)).To(BeTrue())
		Expect(g.admitted(n)).To(BeFalse())
		Expect(g.st.Admins[admin.String()].Seq).To(BeEquivalentTo(1))
	})

	It("lets only an operator change the limits", func() {
		claimed := newID()
		g.admins[claimed] = true
		Expect(g.canSetLimits(admin)).To(BeTrue())
		Expect(g.canSetLimits(claimed)).To(BeFalse())
		Expect(g.canSetLimits(newID())).To(BeFalse())
	})

	It("drops the set of a peer that is no longer an admin at load", func() {
		m := newID()
		op(relayproto.OpAdd, 1, m)
		cfg.Admins = []peer.ID{newID()}
		g2, err := newGate(&cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(g2.admitted(m)).To(BeFalse())
	})

	It("refuses a state file of another version", func() {
		Expect(os.WriteFile(filepath.Join(cfg.DataDir, stateFile), []byte(`{"version":9}`), 0o600)).To(Succeed())
		_, err := newGate(&cfg)
		Expect(err).To(MatchError(ContainSubstring("version 9")))
	})

	Describe("relay passes", func() {
		pass := func(code string, ttl int) relayproto.Response {
			resp, _ := g.apply(admin, relayproto.Request{Op: relayproto.OpPass,
				Pass: &relayproto.Pass{Code: code, TTLSeconds: ttl, Label: "phone"}}, now)
			return resp
		}

		It("validates the code and the TTL", func() {
			Expect(pass("short", 60).Err).To(ContainSubstring("8 to 256"))
			Expect(pass("long-enough-code", 0).Err).To(ContainSubstring("ttl"))
			Expect(pass("long-enough-code", int(MaxPassTTL/time.Second)+1).Err).To(ContainSubstring("ttl"))
		})

		It("stores only the hash of the code", func() {
			Expect(pass("secret-code-123", 60).OK).To(BeTrue())
			b, err := os.ReadFile(filepath.Join(cfg.DataDir, stateFile))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(b)).ToNot(ContainSubstring("secret-code-123"))
			Expect(string(b)).To(ContainSubstring(passHash("secret-code-123")))
		})

		It("keeps a pending member out of the digest and through a replace", func() {
			Expect(pass("code-0123456789", 60).OK).To(BeTrue())
			d := newID()
			_, _, ok := g.usePass(d, "code-0123456789", now)
			Expect(ok).To(BeTrue())
			resp := op(relayproto.OpReplace, 1)
			Expect(resp.Count).To(Equal(0))
			Expect(g.admitted(d)).To(BeTrue())
			list := op(relayproto.OpList, 0)
			Expect(list.Peers).To(ConsistOf(relayproto.Peer{ID: d.String(), Label: "phone", Pending: true}))
			Expect(op(relayproto.OpAdd, 2, d).Count).To(Equal(1))
			Expect(op(relayproto.OpList, 0).Peers[0].Pending).To(BeFalse())
		})

		It("expires a pending member in the sweep", func() {
			Expect(pass("code-0123456789", 60).OK).To(BeTrue())
			d := newID()
			_, _, ok := g.usePass(d, "code-0123456789", now)
			Expect(ok).To(BeTrue())
			Expect(g.sweep(now.Add(time.Minute))).To(BeEmpty())
			Expect(g.sweep(now.Add(cfg.PendingTTL + time.Second))).To(ConsistOf(d))
			Expect(g.admitted(d)).To(BeFalse())
		})

		It("caps the open passes of an admin", func() {
			g.maxPasses = 1
			Expect(pass("code-0000000001", 60).OK).To(BeTrue())
			Expect(pass("code-0000000002", 60).Err).To(ContainSubstring("too many open passes"))
		})

		It("refuses a code another admin already uses", func() {
			other := newID()
			g.admins[other] = true
			Expect(pass("code-0123456789", 60).OK).To(BeTrue())
			resp, _ := g.apply(other, relayproto.Request{Op: relayproto.OpPass,
				Pass: &relayproto.Pass{Code: "code-0123456789", TTLSeconds: 60}}, now)
			Expect(resp.Err).To(Equal("pass code in use"))
			// and the other admin cannot cancel it
			g.apply(other, relayproto.Request{Op: relayproto.OpPassCancel, Pass: &relayproto.Pass{Code: "code-0123456789"}}, now)
			_, _, ok := g.usePass(newID(), "code-0123456789", now)
			Expect(ok).To(BeTrue())
		})
	})
})
