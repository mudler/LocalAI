package p2pnet_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var _ = Describe("Identity", func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "p2p", "identity.key")
	})

	It("creates a key with mode 0600 and loads the same key again", func() {
		k1, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		st, err := os.Stat(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))

		k2, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(k2.Equals(k1)).To(BeTrue())
	})

	It("keeps the peer ID of a host across restarts", func() {
		h1, err := p2pnet.New(context.Background(), p2pnet.Config{IdentityPath: path, ListenAddrs: loopback})
		Expect(err).ToNot(HaveOccurred())
		id := h1.ID()
		Expect(h1.Close()).To(Succeed())

		h2 := newHost(p2pnet.Config{IdentityPath: path})
		Expect(h2.ID()).To(Equal(id))
	})

	It("regenerates a new key and keeps the previous one", func() {
		k1, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		k2, err := p2pnet.RegenerateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(k2.Equals(k1)).To(BeFalse())

		loaded, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded.Equals(k2)).To(BeTrue())

		prev, err := p2pnet.LoadOrCreateIdentity(path + ".prev")
		Expect(err).ToNot(HaveOccurred())
		Expect(prev.Equals(k1)).To(BeTrue())
		st, err := os.Stat(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("keeps an older backup when it regenerates a second time", func() {
		k1, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		k2, err := p2pnet.RegenerateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		_, err = p2pnet.RegenerateIdentity(path)
		Expect(err).ToNot(HaveOccurred())

		prev, err := p2pnet.LoadOrCreateIdentity(path + ".prev")
		Expect(err).ToNot(HaveOccurred())
		Expect(prev.Equals(k2)).To(BeTrue(), "the last key before the change")
		older, err := filepath.Glob(path + ".prev.*")
		Expect(err).ToNot(HaveOccurred())
		Expect(older).To(HaveLen(1))
		kept, err := p2pnet.LoadOrCreateIdentity(older[0])
		Expect(err).ToNot(HaveOccurred())
		Expect(kept.Equals(k1)).To(BeTrue(), "the first key is not lost")
	})

	It("refuses a corrupt key and leaves the file untouched", func() {
		Expect(os.MkdirAll(filepath.Dir(path), 0o700)).To(Succeed())
		Expect(os.WriteFile(path, []byte("not a key"), 0o600)).To(Succeed())
		_, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).To(HaveOccurred())
		b, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(Equal("not a key"))
	})

	It("restricts a key file that others can read", func() {
		_, err := p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.Chmod(path, 0o644)).To(Succeed())
		_, err = p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		st, err := os.Stat(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("refuses an empty path", func() {
		_, err := p2pnet.LoadOrCreateIdentity("")
		Expect(err).To(HaveOccurred())
		_, err = p2pnet.New(context.Background(), p2pnet.Config{})
		Expect(err).To(HaveOccurred())
	})
})
