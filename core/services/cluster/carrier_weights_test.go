package cluster_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("routing.carrier_weights in the cluster settings", func() {
	DescribeTable("checks a weight",
		func(value string, ok bool) {
			err := cluster.CheckSetting(cluster.SettingCarrierWeightNATS, value)
			if ok {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("zero means: only when nothing else fits", "0", true),
		Entry("the top of the range", "1000", true),
		Entry("a middle value", "250", true),
		Entry("above the range", "1001", false),
		Entry("below the range", "-1", false),
		Entry("not a number", "high", false),
		Entry("a fraction", "1.5", false),
		Entry("empty", "", false),
	)

	It("defaults to nats 100, websocket 50, p2p 25 when nothing is stored", func() {
		w := cluster.CarrierWeightsFrom(map[string]string{})
		Expect(w).To(Equal(cluster.CarrierWeights{NATS: 100, WebSocket: 50, P2P: 25}))
	})

	It("takes a stored weight and keeps the default for the others", func() {
		w := cluster.CarrierWeightsFrom(map[string]string{cluster.SettingCarrierWeightWebSocket: "0"})
		Expect(w).To(Equal(cluster.CarrierWeights{NATS: 100, WebSocket: 0, P2P: 25}))
	})

	It("ignores a stored value that is out of range, as it ignores a short wait", func() {
		w := cluster.CarrierWeightsFrom(map[string]string{cluster.SettingCarrierWeightNATS: "99999"})
		Expect(w.NATS).To(Equal(100))
	})

	It("names the three carriers and no others", func() {
		Expect(cluster.CarrierWeightKeys).To(HaveLen(3))
		Expect(cluster.CarrierWeightKeys).To(HaveKey("nats"))
		Expect(cluster.CarrierWeightKeys).To(HaveKey("websocket"))
		Expect(cluster.CarrierWeightKeys).To(HaveKey("p2p"))
	})

	Context("with two frontends on one database", func() {
		var (
			ctx  context.Context
			a, b *cluster.SettingsStore
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			ctx = context.Background()
			var err error
			a, err = cluster.NewSettingsStore(db)
			Expect(err).ToNot(HaveOccurred())
			b, err = cluster.NewSettingsStore(db)
			Expect(err).ToNot(HaveOccurred())
		})

		It("is read by the other frontend after one frontend saves it", func() {
			Expect(a.Apply(ctx, []cluster.SettingChange{{Key: cluster.SettingCarrierWeightP2P, Value: "400"}}, "admin")).To(Succeed())
			w, err := b.CarrierWeights(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(w).To(Equal(cluster.CarrierWeights{NATS: 100, WebSocket: 50, P2P: 400}))
		})

		It("keeps the weights that a partial update does not name, even when two updates overlap", func() {
			Expect(a.Apply(ctx, []cluster.SettingChange{{Key: cluster.SettingCarrierWeightNATS, Value: "10"}}, "one")).To(Succeed())
			Expect(b.Apply(ctx, []cluster.SettingChange{{Key: cluster.SettingCarrierWeightWebSocket, Value: "20"}}, "two")).To(Succeed())
			w, err := a.CarrierWeights(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(w).To(Equal(cluster.CarrierWeights{NATS: 10, WebSocket: 20, P2P: 25}))
		})

		It("puts the default back when a weight is cleared", func() {
			Expect(a.Set(ctx, cluster.SettingCarrierWeightNATS, "1", "admin")).To(Succeed())
			Expect(a.Clear(ctx, cluster.SettingCarrierWeightNATS)).To(Succeed())
			w, err := b.CarrierWeights(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(w.NATS).To(Equal(100))
		})

		It("stores nothing when one weight of a batch is out of range", func() {
			err := a.Apply(ctx, []cluster.SettingChange{
				{Key: cluster.SettingCarrierWeightNATS, Value: "5"},
				{Key: cluster.SettingCarrierWeightP2P, Value: "5000"},
			}, "admin")
			Expect(err).To(HaveOccurred())
			w, err := b.CarrierWeights(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(w).To(Equal(cluster.DefaultCarrierWeights))
		})
	})
})
