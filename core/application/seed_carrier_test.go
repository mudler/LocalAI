package application

import (
	"context"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("seeding the carrier of the cluster", func() {
	It("lets a replica without a NATS flag start while replicas with one seed, whatever the order", func() {
		for round := 0; round < 5; round++ {
			db := testutil.SetupTestDB()
			ctx := context.Background()
			store, err := cluster.NewCarrierStore(db)
			Expect(err).ToNot(HaveOccurred())
			settings, err := cluster.NewSettingsStore(db)
			Expect(err).ToNot(HaveOccurred())

			var wg sync.WaitGroup
			errs := make([]error, 12)
			start := make(chan struct{})
			for i := range errs {
				flag := ""
				if i%2 == 0 {
					flag = "nats://broker:4222"
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, errs[i] = seedCarrier(ctx, db, store, settings, flag, fmt.Sprintf("replica-%d", i))
				}()
			}
			close(start)
			wg.Wait()
			for i, err := range errs {
				Expect(err).ToNot(HaveOccurred(), "replica %d", i)
			}

			// Whoever won, the row and the address agree: a row that says NATS
			// always has an address next to it.
			row, err := store.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			if row.Active == cluster.CarrierNATS {
				url, ok, err := settings.Get(ctx, cluster.SettingNATSURL)
				Expect(err).ToNot(HaveOccurred())
				Expect(ok).To(BeTrue())
				Expect(url).To(Equal("nats://broker:4222"))
			}
		}
	})

	It("finds the address stored, and not the row alone, when a flag-less replica reads between the two writes of another", func() {
		db := testutil.SetupTestDB()
		ctx := context.Background()
		store, err := cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		settings, err := cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())

		// The first write of the replica with the flag has happened; the second has not.
		_, err = settings.SetIfAbsent(ctx, cluster.SettingNATSURL, "nats://broker:4222", "replica-a")
		Expect(err).ToNot(HaveOccurred())

		row, err := seedCarrier(ctx, db, store, settings, "", "replica-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(row.Active).To(Equal(cluster.CarrierNATS), "an address is stored, so the cluster is on NATS and not on the tunnel")
		Expect(row.NATSURL).To(Equal("nats://broker:4222"))
	})
})
