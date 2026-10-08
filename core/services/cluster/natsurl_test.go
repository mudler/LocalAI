package cluster_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the NATS address of the cluster", func() {
	It("accepts a plain address and a list of plain addresses", func() {
		Expect(cluster.CheckNATSURL("nats://broker:4222")).To(Succeed())
		Expect(cluster.CheckNATSURL("tls://a:4222, tls://b:4222")).To(Succeed())
	})

	It("refuses a user name, a password, a token in the query and a fragment", func() {
		for _, bad := range []string{
			"nats://user@broker:4222",
			"nats://user:pass@broker:4222",
			"nats://broker:4222?token=abc",
			"nats://broker:4222?",
			"nats://broker:4222#frag",
			"nats://a:4222,nats://u:p@b:4222",
		} {
			Expect(cluster.CheckNATSURL(bad)).To(MatchError(cluster.ErrCredentialInURL), bad)
		}
	})

	It("shows an address without anything a credential could hide in", func() {
		Expect(cluster.PublicNATSURL("nats://user:pass@broker:4222?token=abc#x")).To(Equal("nats://broker:4222"))
		Expect(cluster.PublicNATSURL("nats://u:p@a:4222, nats://b:4222?t=1")).To(Equal("nats://a:4222,nats://b:4222"))
		Expect(cluster.PublicNATSURL("")).To(Equal(""))
	})

	It("is not stored with a credential, by Set or by SetIfAbsent", func() {
		store, err := cluster.NewSettingsStore(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		ctx := context.Background()
		for _, key := range []string{cluster.SettingNATSURL, cluster.SettingNATSWorkerURL} {
			Expect(store.Set(ctx, key, "nats://u:p@broker:4222", "x")).To(MatchError(cluster.ErrCredentialInURL))
			_, err := store.SetIfAbsent(ctx, key, "nats://broker:4222?token=1", "x")
			Expect(err).To(MatchError(cluster.ErrCredentialInURL))
			_, ok, err := store.Get(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse())
		}
	})
})
