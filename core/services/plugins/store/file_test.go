package store_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/plugins/store/storetest"
)

var _ = Describe("the file store", func() {
	Context("with a file", func() {
		storetest.Run(func() store.Store {
			s, err := store.NewFileStore(filepath.Join(GinkgoT().TempDir(), "plugins.json"))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Context("in memory only", func() {
		storetest.Run(func() store.Store {
			s, err := store.NewFileStore("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("keeps the records and the tombstone of a default across a reopen", func() {
		ctx := context.Background()
		path := filepath.Join(GinkgoT().TempDir(), "plugins.json")
		a, err := store.NewFileStore(path)
		Expect(err).NotTo(HaveOccurred())
		def := store.Marketplace{ID: "localai", Kind: store.KindURL, Location: "https://example.test/m.json", Enabled: true, Default: true}
		Expect(a.SaveMarketplace(ctx, def)).To(Succeed())
		Expect(a.DeleteMarketplace(ctx, "localai")).To(Succeed())
		Expect(a.InstallTx(ctx, func(tx store.Tx) error {
			return tx.CreateInstall(store.Install{ID: "i1", PluginID: "fmt@tools", Scope: store.ScopeInstance, State: store.StateInstalled})
		})).To(Succeed())

		b, err := store.NewFileStore(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.SaveMarketplace(ctx, def)).To(Succeed())
		got, _ := b.Marketplaces(ctx)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Removed).To(BeTrue(), "the tombstone did not survive a restart")
		ins, _ := b.Installs(ctx, store.InstallFilter{})
		Expect(ins).To(HaveLen(1))
	})

	It("refuses a file it cannot read", func() {
		path := filepath.Join(GinkgoT().TempDir(), "plugins.json")
		Expect(writeFile(path, "not json")).To(Succeed())
		_, err := store.NewFileStore(path)
		Expect(err).To(HaveOccurred())
	})
})
