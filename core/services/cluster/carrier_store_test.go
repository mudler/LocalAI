package cluster_test

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("CarrierStore", func() {
	var (
		db  *gorm.DB
		ctx context.Context
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
	})

	newStore := func() *cluster.CarrierStore {
		s, err := cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		return s
	}

	Describe("Get", func() {
		It("reports a database that was never seeded", func() {
			_, err := newStore().Get(ctx)
			Expect(err).To(MatchError(cluster.ErrNotSeeded))
		})
	})

	Describe("Seed", func() {
		It("inserts a stable row at epoch 1", func() {
			row, created, err := newStore().Seed(ctx, cluster.CarrierNATS, "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(created).To(BeTrue())
			Expect(row.Active).To(Equal(cluster.CarrierNATS))
			Expect(row.Epoch).To(Equal(int64(1)))
			Expect(row.State).To(Equal(cluster.StateStable))
			Expect(row.Target).To(BeEmpty())
			Expect(row.ChangedBy).To(Equal("replica-a"))
		})

		It("keeps the existing row when it is called again", func() {
			_, _, err := newStore().Seed(ctx, cluster.CarrierNATS, "replica-a")
			Expect(err).ToNot(HaveOccurred())

			row, created, err := newStore().Seed(ctx, cluster.CarrierTunnel, "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(created).To(BeFalse())
			Expect(row.Active).To(Equal(cluster.CarrierNATS))
			Expect(row.ChangedBy).To(Equal("replica-a"))
			Expect(row.Epoch).To(Equal(int64(1)))
		})

		It("refuses a carrier name it does not know", func() {
			_, _, err := newStore().Seed(ctx, cluster.Carrier("carrier-pigeon"), "replica-a")
			Expect(err).To(MatchError(cluster.ErrInvalidCarrier))
		})

		It("lets exactly one of several racing replicas insert the row", func() {
			const replicas = 8
			stores := make([]*cluster.CarrierStore, replicas)
			for i := range stores {
				stores[i] = newStore()
			}

			var created atomic.Int32
			rows := make([]cluster.CarrierRow, replicas)
			errs := make([]error, replicas)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range stores {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					// Half the replicas have a NATS URL, half do not: the row
					// must still come from one writer only.
					want := cluster.CarrierNATS
					if i%2 == 1 {
						want = cluster.CarrierTunnel
					}
					var made bool
					rows[i], made, errs[i] = stores[i].Seed(ctx, want, "replica")
					if made {
						created.Add(1)
					}
				}()
			}
			close(start)
			wg.Wait()

			Expect(created.Load()).To(Equal(int32(1)))
			for i := range rows {
				Expect(errs[i]).ToNot(HaveOccurred())
				Expect(rows[i].Active).To(Equal(rows[0].Active), "every replica sees the same winner")
				Expect(rows[i].Epoch).To(Equal(int64(1)))
			}
			var count int64
			Expect(db.Table("cluster_carrier").Count(&count).Error).To(Succeed())
			Expect(count).To(Equal(int64(1)))
		})
	})

	Describe("single row", func() {
		It("refuses a second row at the database", func() {
			_, _, err := newStore().Seed(ctx, cluster.CarrierNATS, "replica-a")
			Expect(err).ToNot(HaveOccurred())
			err = db.Exec(`INSERT INTO cluster_carrier (id, active, epoch, state) VALUES (2, 'nats', 1, 'stable')`).Error
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Transition", func() {
		var store *cluster.CarrierStore

		BeforeEach(func() {
			store = newStore()
			_, _, err := store.Seed(ctx, cluster.CarrierNATS, "replica-a")
			Expect(err).ToNot(HaveOccurred())
		})

		It("moves the row and bumps the epoch when the epoch matches", func() {
			row, err := store.Transition(ctx, 1, cluster.Change{
				Active: cluster.CarrierNATS,
				State:  cluster.StatePrepare,
				Target: cluster.CarrierTunnel,
				By:     "admin",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(row.Epoch).To(Equal(int64(2)))
			Expect(row.PrevEpoch).To(Equal(int64(1)))
			Expect(row.State).To(Equal(cluster.StatePrepare))
			Expect(row.Target).To(Equal(cluster.CarrierTunnel))
			Expect(row.ChangedBy).To(Equal("admin"))

			got, err := store.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Epoch).To(Equal(int64(2)))
			Expect(got.Target).To(Equal(cluster.CarrierTunnel))
		})

		It("refuses a stale epoch and leaves the row alone", func() {
			_, err := store.Transition(ctx, 1, cluster.Change{
				Active: cluster.CarrierNATS, State: cluster.StatePrepare, Target: cluster.CarrierTunnel, By: "admin-1",
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = store.Transition(ctx, 1, cluster.Change{
				Active: cluster.CarrierTunnel, State: cluster.StateStable, By: "admin-2",
			})
			Expect(err).To(MatchError(cluster.ErrStaleEpoch))

			got, err := store.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Epoch).To(Equal(int64(2)))
			Expect(got.ChangedBy).To(Equal("admin-1"))
			Expect(got.Active).To(Equal(cluster.CarrierNATS))
		})

		It("lets one of two writers that read the same epoch win", func() {
			other := newStore()
			var wins, stale atomic.Int32
			start := make(chan struct{})
			var wg sync.WaitGroup
			for _, s := range []*cluster.CarrierStore{store, other} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, err := s.Transition(ctx, 1, cluster.Change{
						Active: cluster.CarrierNATS, State: cluster.StatePrepare, Target: cluster.CarrierTunnel, By: "admin",
					})
					switch {
					case err == nil:
						wins.Add(1)
					case err == cluster.ErrStaleEpoch:
						stale.Add(1)
					default:
						Fail(err.Error())
					}
				}()
			}
			close(start)
			wg.Wait()
			Expect(wins.Load()).To(Equal(int32(1)))
			Expect(stale.Load()).To(Equal(int32(1)))

			got, err := store.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Epoch).To(Equal(int64(2)))
		})

		It("keeps the epoch strictly increasing over a chain of transitions", func() {
			epoch := int64(1)
			for range 5 {
				row, err := store.Transition(ctx, epoch, cluster.Change{
					Active: cluster.CarrierNATS, State: cluster.StateStable, By: "admin",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(row.Epoch).To(Equal(epoch + 1))
				Expect(row.PrevEpoch).To(Equal(epoch))
				epoch = row.Epoch
			}
		})

		It("refuses a change that does not describe a valid state", func() {
			for _, bad := range []cluster.Change{
				{Active: cluster.Carrier("x"), State: cluster.StateStable},
				{Active: cluster.CarrierNATS, State: "sideways"},
				{Active: cluster.CarrierNATS, State: cluster.StatePrepare},                               // no target
				{Active: cluster.CarrierNATS, State: cluster.StateStable, Target: cluster.CarrierTunnel}, // target while stable
			} {
				_, err := store.Transition(ctx, 1, bad)
				Expect(err).To(MatchError(cluster.ErrInvalidChange), "%+v", bad)
			}
			got, err := store.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Epoch).To(Equal(int64(1)))
		})

		It("reports a database that was never seeded", func() {
			Expect(db.Exec(`DELETE FROM cluster_carrier`).Error).To(Succeed())
			_, err := store.Transition(ctx, 1, cluster.Change{Active: cluster.CarrierNATS, State: cluster.StateStable})
			Expect(err).To(MatchError(cluster.ErrNotSeeded))
		})
	})
})
