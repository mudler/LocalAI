package application

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("distributed startup and the cluster carrier", func() {
	var (
		db  *gorm.DB
		cfg *config.ApplicationConfig
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		cfg = &config.ApplicationConfig{
			Context:  ctx,
			DataPath: GinkgoT().TempDir(),
			Auth:     config.AuthConfig{Enabled: true, DatabaseURL: "postgres://unused/db"},
			Distributed: config.DistributedConfig{
				Enabled:    true,
				InstanceID: "replica-a",
				NatsURL:    "nats://127.0.0.1:1",
			},
		}
	})

	carrierRow := func() cluster.CarrierRow {
		store, err := cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		row, err := store.Get(context.Background())
		Expect(err).ToNot(HaveOccurred())
		return row
	}

	It("seeds NATS before it connects, and stops the start on a NATS URL it cannot use", func() {
		cfg.Distributed.NatsURL = "nats://[::1"
		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).To(MatchError(ContainSubstring("connecting to NATS")))
		Expect(svc).To(BeNil())

		// The row was written with NATS, because this deployment has a NATS URL.
		row := carrierRow()
		Expect(row.Active).To(Equal(cluster.CarrierNATS))
		Expect(row.Epoch).To(Equal(int64(1)))
		Expect(row.State).To(Equal(cluster.StateStable))
		Expect(row.ChangedBy).To(Equal("replica-a"))
	})

	It("refuses to start on a carrier this build does not run, and does not try NATS", func() {
		store, err := cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = store.Seed(context.Background(), cluster.CarrierTunnel, "replica-b")
		Expect(err).ToNot(HaveOccurred())

		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).To(MatchError(ContainSubstring(`the cluster carrier is "tunnel"`)))
		Expect(err.Error()).ToNot(ContainSubstring("connecting to NATS"), "no fallback to the carrier the flags name")
		Expect(svc).To(BeNil())
		Expect(carrierRow().Active).To(Equal(cluster.CarrierTunnel), "the row is left as it was")
	})

	It("keeps the existing validation of a missing NATS URL", func() {
		cfg.Distributed.NatsURL = ""
		_, err := initDistributed(cfg, db, nil, nil)
		Expect(err).To(MatchError(ContainSubstring("--nats-url")))

		store, err := cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		_, err = store.Get(context.Background())
		Expect(err).To(MatchError(cluster.ErrNotSeeded), "a replica that cannot start leaves no row behind")
	})

	Context("with a NATS server", func() {
		BeforeEach(func() {
			url, err := startNATS()
			if err != nil {
				// A CI runner that lost Docker must go red, not skip. Local runs
				// without Docker, and macOS CI, skip.
				if os.Getenv("CI") != "" && runtime.GOOS != "darwin" {
					Fail("testcontainers requires Docker and CI is set: " + err.Error())
				}
				Skip("testcontainers requires Docker: " + err.Error())
			}
			cfg.Distributed.NatsURL = url
		})

		It("seeds NATS on first start and wires every seam to it", func() {
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(svc.Shutdown)

			Expect(carrierRow().Active).To(Equal(cluster.CarrierNATS))
			Expect(svc.Broadcaster).ToNot(BeNil())
			Expect(svc.WorkQueue).ToNot(BeNil())
			Expect(svc.AgentControl).ToNot(BeNil())
			Expect(svc.FileStager).ToNot(BeNil())
			Expect(svc.Unloader).ToNot(BeNil())
			Expect(svc.WorkerHTTPDial).ToNot(BeNil())

			// A message published through the holder reaches a subscriber made
			// through the same holder, over the real server.
			got := make(chan []byte, 1)
			sub, err := svc.Broadcaster.Subscribe("jobs.carrier-spec", func(b []byte) { got <- b })
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })
			Expect(svc.Broadcaster.Publish("jobs.carrier-spec", "hello")).To(Succeed())
			Eventually(got).Should(Receive(Equal([]byte(`"hello"`))))
		})

		It("registers every replica in the instances table and removes it on shutdown", func() {
			live := func() []string {
				GinkgoHelper()
				var ids []string
				Expect(db.Raw(cluster.LiveInstanceIDsSQL+" ORDER BY id", cluster.InstanceLiveness.Seconds()).Scan(&ids).Error).To(Succeed())
				return ids
			}

			first, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(first.Shutdown)
			cfg2 := *cfg
			cfg2.Distributed.InstanceID = "replica-b"
			cfg2.DataPath = GinkgoT().TempDir()
			second, err := initDistributed(&cfg2, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(second.Shutdown)
			Expect(live()).To(ConsistOf("replica-a", "replica-b"))

			first.Shutdown()
			Expect(live()).To(ConsistOf("replica-b"))
		})

		It("starts a second replica on the same row without changing it", func() {
			first, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(first.Shutdown)

			cfg2 := *cfg
			cfg2.Distributed.InstanceID = "replica-b"
			cfg2.DataPath = GinkgoT().TempDir()
			second, err := initDistributed(&cfg2, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(second.Shutdown)

			row := carrierRow()
			Expect(row.Epoch).To(Equal(int64(1)))
			Expect(row.ChangedBy).To(Equal("replica-a"))
		})
	})
})

var (
	natsOnce sync.Once
	natsURL  string
	natsCtr  testcontainers.Container
	natsErr  error
)

// startNATS starts one server for the process and returns its URL.
func startNATS() (string, error) {
	natsOnce.Do(func() {
		ctx := context.Background()
		var ctr testcontainers.Container
		ctr, natsErr = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "nats:2.10-alpine",
				ExposedPorts: []string{"4222/tcp"},
				WaitingFor:   wait.ForListeningPort("4222/tcp"),
			},
			Started: true,
		})
		if natsErr != nil {
			return
		}
		natsCtr = ctr
		host, err := ctr.Host(ctx)
		if err != nil {
			natsErr = err
			return
		}
		port, err := ctr.MappedPort(ctx, "4222/tcp")
		if err != nil {
			natsErr = err
			return
		}
		natsURL = fmt.Sprintf("nats://%s:%s", host, port.Port())
	})
	return natsURL, natsErr
}

var _ = AfterSuite(func() {
	if natsCtr != nil {
		_ = natsCtr.Terminate(context.Background())
	}
})
