package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
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

	It("starts a deployment with only PostgreSQL on the tunnel carrier, and opens no NATS connection", func() {
		pg, dsn := testutil.SetupTestDBWithDSN()
		cfg.Auth.DatabaseURL = dsn
		cfg.Distributed.NatsURL = ""

		svc, err := initDistributed(cfg, pg, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)

		store, err := cluster.NewCarrierStore(pg)
		Expect(err).ToNot(HaveOccurred())
		row, err := store.Get(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(row.Active).To(Equal(cluster.CarrierTunnel))
		Expect(row.Epoch).To(Equal(int64(1)))
		Expect(svc.active.Load().Name).To(Equal(cluster.CarrierTunnel))

		// Fan-out goes through the database, and one LISTEN session exists for it.
		var sessions int64
		Expect(pg.Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'localai_pgbus_%'").Scan(&sessions).Error).To(Succeed())
		Expect(sessions).To(Equal(int64(1)))
		got := make(chan []byte, 1)
		_, err = svc.Broadcaster.Subscribe("jobs.carrier-spec", func(b []byte) { got <- b })
		Expect(err).ToNot(HaveOccurred())
		Expect(svc.Broadcaster.Publish("jobs.carrier-spec", "hello")).To(Succeed())
		Eventually(got, "10s").Should(Receive(Equal([]byte(`"hello"`))))
	})

	It("starts on the tunnel carrier that the row names, whatever the flags of this replica say, and does not try NATS", func() {
		pg, dsn := testutil.SetupTestDBWithDSN()
		cfg.Auth.DatabaseURL = dsn
		store, err := cluster.NewCarrierStore(pg)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = store.Seed(context.Background(), cluster.CarrierTunnel, "replica-b")
		Expect(err).ToNot(HaveOccurred())

		// The NATS URL of the flag points at nothing. It is kept for a change back to
		// NATS, and it is not dialled.
		svc, err := initDistributed(cfg, pg, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)
		Expect(svc.active.Load().Name).To(Equal(cluster.CarrierTunnel))
		row, err := store.Get(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(row.Active).To(Equal(cluster.CarrierTunnel), "the row is left as it was")
		Expect(row.ChangedBy).To(Equal("replica-b"))

		url, ok, err := svc.Settings.Get(context.Background(), cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(url).To(Equal("nats://127.0.0.1:1"), "the URL of the flag is stored for a change back to NATS")
	})

	It("claims queued work on the tunnel carrier once the switch has started", func() {
		pg, dsn := testutil.SetupTestDBWithDSN()
		cfg.Auth.DatabaseURL = dsn
		cfg.Distributed.NatsURL = ""
		svc, err := initDistributed(cfg, pg, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)
		svc.StartCarrierSwitch(cfg.Context, pg)

		Expect(svc.WorkQueue.Enqueue(context.Background(), messaging.WorkMCPCI, map[string]string{"job_id": "j1"})).To(Succeed())
		// No agent worker exists, so the claim goes back to the pool with a wait. That
		// the attempt count moves shows that this replica took the row and drove it.
		Eventually(func() int {
			var attempts int
			_ = pg.Raw("SELECT coalesce(max(attempts), 0) FROM work_claims").Scan(&attempts).Error
			return attempts
		}, "30s", "200ms").Should(BeNumerically(">=", 1))
	})

	It("seeds NATS for a deployment that has a NATS URL, and stores the URL for the whole cluster", func() {
		// The server at that address is not there. A frontend may start before its
		// broker, so the start goes on and the client keeps trying.
		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)
		Expect(svc.active.Load().Name).To(Equal(cluster.CarrierNATS))

		row := carrierRow()
		Expect(row.Active).To(Equal(cluster.CarrierNATS))
		store, err := cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())
		url, ok, err := store.Get(context.Background(), cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(url).To(Equal("nats://127.0.0.1:1"))
	})

	It("uses the NATS URL stored for the cluster and not the one of the flag", func() {
		store, err := cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(store.Set(context.Background(), cluster.SettingNATSURL, "nats://[::1", "admin")).To(Succeed())
		cfg.Distributed.NatsURL = "nats://127.0.0.1:2"

		_, err = initDistributed(cfg, db, nil, nil)
		Expect(err).To(MatchError(ContainSubstring("connecting to NATS")), "the stored address, which cannot be parsed, was the one used")
		url, _, err := store.Get(context.Background(), cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(url).To(Equal("nats://[::1"), "a flag does not overwrite a setting of the cluster")
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

		It("publishes the address and the peer credential of the replica in its row", func() {
			cfg.Distributed.PeerAddress = "10.0.0.7:8080"
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(svc.Shutdown)

			inst, err := cluster.NewRegistry(db).Get(context.Background(), "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(inst.AdvertisedAddr).To(Equal("10.0.0.7:8080"))
			Expect(inst.PeerTokenHash).To(HaveLen(64), "only the hash of the credential is published")
			Expect(svc.PeerPool).ToNot(BeNil())
			Expect(svc.PeerSessions).ToNot(BeNil())
			Expect(svc.WorkerDialer).ToNot(BeNil())
		})

		It("starts without an address when the operator gave none and the database route cannot give one", func() {
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(svc.Shutdown)
			inst, err := cluster.NewRegistry(db).Get(context.Background(), "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(inst.AdvertisedAddr).To(BeEmpty())
			Expect(inst.PeerTokenHash).ToNot(BeEmpty())
		})

		It("ends the tunnel that it holds for a node that was removed, and says whether it held one", func() {
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(svc.Shutdown)

			Expect(svc.Disconnect("nobody")).To(BeFalse())

			front, back := tunnelSessionPair()
			_, err = svc.Tunnels.Attach(context.Background(), "w1", tunnel.LaneInference, front)
			Expect(err).ToNot(HaveOccurred())
			Expect(svc.Disconnect("w1")).To(BeTrue())
			Eventually(back.CloseChan()).Should(BeClosed())
			_, _, err = cluster.NewRegistry(db).Owner(context.Background(), "w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("closes the tunnels it holds before it leaves the instances table", func() {
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			front, back := tunnelSessionPair()
			_, err = svc.Tunnels.Attach(context.Background(), "w2", tunnel.LaneInference, front)
			Expect(err).ToNot(HaveOccurred())

			svc.Shutdown()

			Eventually(back.CloseChan()).Should(BeClosed())
			_, _, err = cluster.NewRegistry(db).Owner(context.Background(), "w2")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
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

// tunnelSessionPair returns the two ends of a tunnel session over a real
// websocket.
func tunnelSessionPair() (front, back *tunnel.Session) {
	GinkgoHelper()
	up := tunnel.NewUpgrader()
	got := make(chan *tunnel.Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := tunnel.ServerSession(ws, tunnel.LaneInference)
		if err != nil {
			_ = ws.Close()
			return
		}
		got <- sess
	}))
	DeferCleanup(srv.Close)
	ws, _, err := tunnel.NewDialer(5*time.Second).Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	Expect(err).ToNot(HaveOccurred())
	back, err = tunnel.ClientSession(ws, tunnel.LaneInference)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = back.Close() })
	Eventually(got).Should(Receive(&front))
	return front, back
}

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
