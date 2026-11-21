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
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("distributed startup", func() {
	var (
		db  *gorm.DB
		cfg *config.ApplicationConfig
	)

	BeforeEach(func() {
		var dsn string
		db, dsn = testutil.SetupTestDBWithDSN()
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		cfg = &config.ApplicationConfig{
			Context:  ctx,
			DataPath: GinkgoT().TempDir(),
			// Every replica opens a LISTEN connection for the PostgreSQL bus.
			Auth: config.AuthConfig{Enabled: true, DatabaseURL: dsn},
			Distributed: config.DistributedConfig{
				Enabled:    true,
				InstanceID: "replica-a",
				NatsURL:    "nats://127.0.0.1:1",
			},
		}
	})

	It("stops the start on a NATS URL it cannot use", func() {
		cfg.Distributed.NatsURL = "nats://[::1"
		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).To(MatchError(ContainSubstring("connecting to NATS")))
		Expect(svc).To(BeNil())
	})

	It("starts a deployment with only PostgreSQL on the tunnel route, broadcasts over PostgreSQL, and opens no NATS connection", func() {
		pg, dsn := testutil.SetupTestDBWithDSN()
		cfg.Auth.DatabaseURL = dsn
		cfg.Distributed.NatsURL = ""

		svc, err := initDistributed(cfg, pg, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)

		Expect(svc.routes.Route(nodes.RouteTunnel)).ToNot(BeNil())
		Expect(svc.routes.Route(nodes.RouteDirect)).To(BeNil(), "no NATS server, no direct route")
		Expect(svc.NATS.Bus()).To(Equal(carrier.BusState{Bus: carrier.BusPostgres}))

		// Fan-out goes through the database, and one LISTEN session exists for it.
		Expect(svc.bus.PublishBus()).To(Equal(carrier.BusPostgres))
		Expect(svc.bus.Unready()).To(Succeed(), "no NATS is configured, so none can be out of reach")
		var sessions int64
		Expect(pg.Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'localai_pgbus_%'").Scan(&sessions).Error).To(Succeed())
		Expect(sessions).To(Equal(int64(1)))
		got := make(chan []byte, 1)
		_, err = svc.Broadcaster.Subscribe("staging.startup-spec", func(b []byte) { got <- b })
		Expect(err).ToNot(HaveOccurred())
		Expect(svc.Broadcaster.Publish("staging.startup-spec", "hello")).To(Succeed())
		Eventually(got, "10s").Should(Receive(Equal([]byte(`"hello"`))))
	})

	It("starts before its NATS server, holds both routes, stores the URL for the whole cluster, and broadcasts over PostgreSQL until it reaches NATS", func() {
		pg, dsn := testutil.SetupTestDBWithDSN()
		cfg.Auth.DatabaseURL = dsn

		// The NATS URL of the flag points at nothing. A frontend may start before
		// its broker: the start goes on, and the replica is not ready while it
		// does not reach the server. It still publishes and hears on PostgreSQL.
		svc, err := initDistributed(cfg, pg, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)
		Expect(svc.routes.Route(nodes.RouteDirect)).ToNot(BeNil())
		Expect(svc.routes.Route(nodes.RouteTunnel)).ToNot(BeNil())
		Expect(svc.bus.Unready()).To(MatchError(carrier.ErrBusUnreachable))
		Expect(svc.bus.PublishBus()).To(Equal(carrier.BusPostgres))
		Expect(svc.NATS.Bus()).To(Equal(carrier.BusState{Bus: carrier.BusPostgres, NATSConfigured: true}))
		got := make(chan []byte, 1)
		_, err = svc.Broadcaster.Subscribe("staging.startup-spec", func(b []byte) { got <- b })
		Expect(err).ToNot(HaveOccurred())
		Expect(svc.Broadcaster.Publish("staging.startup-spec", "floor")).To(Succeed())
		Eventually(got, "10s").Should(Receive(Equal([]byte(`"floor"`))))

		url, ok, err := svc.Settings.Get(context.Background(), cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(url).To(Equal("nats://127.0.0.1:1"))
	})

	It("drops the carrier row and the readiness columns that an older release left", func() {
		Expect(db.Exec(`CREATE TABLE cluster_carrier (id bigint PRIMARY KEY, active varchar(16), epoch bigint)`).Error).To(Succeed())
		Expect(db.Exec(`INSERT INTO cluster_carrier VALUES (1, 'tunnel', 4)`).Error).To(Succeed())
		Expect(db.Exec(`CREATE TABLE instances (id varchar(36) PRIMARY KEY, version varchar(255), last_seen timestamptz,
			ready_epoch bigint NOT NULL DEFAULT 0, ready_reason varchar(512) NOT NULL DEFAULT '')`).Error).To(Succeed())

		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)

		Expect(db.Migrator().HasTable("cluster_carrier")).To(BeFalse())
		Expect(db.Migrator().HasColumn(&cluster.Instance{}, "ready_epoch")).To(BeFalse())
		Expect(svc.routes.Route(nodes.RouteDirect)).ToNot(BeNil(), "the routes follow the configuration, not a row")
	})

	It("stops the start when the NATS URL of the flag differs from the one stored for the cluster, and names both", func() {
		store, err := cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(store.Set(context.Background(), cluster.SettingNATSURL, "nats://old-broker:4222", "admin")).To(Succeed())
		cfg.Distributed.NatsURL = "nats://new-broker:4222"

		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(svc).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("nats://new-broker:4222"))
		Expect(err.Error()).To(ContainSubstring("nats://old-broker:4222"))
		Expect(err.Error()).To(ContainSubstring("/api/cluster/settings"), "it says how to resolve it")
		url, _, err := store.Get(context.Background(), cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(url).To(Equal("nats://old-broker:4222"), "a flag does not overwrite a setting of the cluster")
	})

	It("starts on the stored NATS URL when the flag names the same server, and keeps the credential of the flag out of the database", func() {
		cfg.Distributed.NatsURL = "nats://user:secret@127.0.0.1:1?token=abc"
		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)

		store, err := cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())
		url, ok, err := store.Get(context.Background(), cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(url).To(Equal("nats://127.0.0.1:1"))

		// A second start with the same flag is the same deployment.
		cfg.Distributed.InstanceID = "replica-b"
		second, err := initDistributed(cfg, db, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(second.Shutdown)
	})

	It("starts without a NATS flag on the URL that the cluster stored", func() {
		settings, err := cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(settings.Set(context.Background(), cluster.SettingNATSURL, "nats://127.0.0.1:1", "admin")).To(Succeed())
		cfg.Distributed.NatsURL = ""

		svc, err := initDistributed(cfg, db, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(svc.Shutdown)
		Expect(svc.NATS.Bus().NATSConfigured).To(BeTrue())
		Expect(svc.routes.Route(nodes.RouteDirect)).ToNot(BeNil())
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

		It("wires every seam, and broadcasts over NATS once it reaches the server", func() {
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(svc.Shutdown)

			Expect(svc.routes.Route(nodes.RouteDirect)).ToNot(BeNil())
			Eventually(svc.Unready, "10s").Should(Or(Succeed(), MatchError(ErrNoPeerAddress)), "the bus is ready once it reaches the server")
			Eventually(svc.bus.Unready, "10s").Should(Succeed())
			Expect(svc.bus.PublishBus()).To(Equal(carrier.BusNATS))
			Expect(svc.NATS.Bus()).To(Equal(carrier.BusState{Bus: carrier.BusNATS, NATSConfigured: true, NATSConnected: true}))
			Expect(svc.Broadcaster).ToNot(BeNil())
			Expect(svc.Replies).ToNot(BeNil())
			Expect(svc.AgentControl).ToNot(BeNil())
			Expect(svc.FileStager).ToNot(BeNil())
			Expect(svc.Unloader).ToNot(BeNil())
			Expect(svc.WorkerHTTPDial).ToNot(BeNil())

			// A message published through the holder reaches a subscriber made
			// through the same holder, over the real server.
			got := make(chan []byte, 1)
			sub, err := svc.Broadcaster.Subscribe("staging.startup-spec", func(b []byte) { got <- b })
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })
			Expect(svc.Broadcaster.Publish("staging.startup-spec", "hello")).To(Succeed())
			Eventually(got).Should(Receive(Equal([]byte(`"hello"`))))

			// Request and reply between processes go over the peer link, also on
			// NATS. A request for this replica runs in the process.
			rsub, err := svc.Replies.SubscribeReply("chats.replay.spec", func(data []byte, reply func([]byte)) {
				reply(append([]byte("re:"), data...))
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = rsub.Unsubscribe() })
			out, err := svc.Replies.RequestInstance("replica-a", "chats.replay.spec", []byte("x"), 5*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal("re:x"))
			// No chat worker is live, so a chat start reaches nobody.
			_, err = svc.Replies.RequestQueue(messaging.SubjectChatStart, []byte("{}"), 5*time.Second, nil)
			Expect(messaging.IsNoResponders(err)).To(BeTrue(), "got %v", err)
		})

		It("registers every replica in the instances table and removes it on shutdown", func() {
			live := func() []string {
				GinkgoHelper()
				live, err := cluster.NewRegistry(db).ListLive(context.Background(), cluster.InstanceLiveness)
				Expect(err).ToNot(HaveOccurred())
				ids := make([]string, len(live))
				for i, in := range live {
					ids[i] = in.ID
				}
				return ids
			}

			first, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(first.Shutdown)
			second, err := initDistributed(secondReplica(cfg), db, nil, nil)
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
			Eventually(svc.Unready, "10s").Should(Succeed(), "a replica with a peer address that reaches NATS is ready")

			inst, err := cluster.NewRegistry(db).Get(context.Background(), "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(inst.AdvertisedAddr).To(Equal("10.0.0.7:8080"))
			Expect(inst.PeerTokenHash).To(HaveLen(64), "only the hash of the credential is published")
			Expect(svc.PeerPool).ToNot(BeNil())
			Expect(svc.PeerSessions).ToNot(BeNil())
			Expect(svc.WorkerDialer).ToNot(BeNil())
		})

		It("starts without an address when the operator gave none and the database route cannot give one, and is not ready", func() {
			svc, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(svc.Shutdown)
			inst, err := cluster.NewRegistry(db).Get(context.Background(), "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(inst.AdvertisedAddr).To(BeEmpty())
			Expect(inst.PeerTokenHash).ToNot(BeEmpty())
			Expect(svc.Unready()).To(MatchError(ErrNoPeerAddress))
			Expect(svc.Unready().Error()).To(ContainSubstring("LOCALAI_PEER_ADDRESS"))
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

		It("starts a second replica with the same NATS address, and both list the live replicas", func() {
			first, err := initDistributed(cfg, db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(first.Shutdown)

			second, err := initDistributed(secondReplica(cfg), db, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(second.Shutdown)

			live, err := second.NATS.Replicas(context.Background())
			Expect(err).ToNot(HaveOccurred())
			ids := []string{}
			for _, r := range live {
				ids = append(ids, r.ID)
			}
			Expect(ids).To(ConsistOf("replica-a", "replica-b"))
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

// secondReplica is the configuration of another replica of the deployment of
// cfg. It is built field by field, because the configuration holds atomics that
// must not be copied.
func secondReplica(cfg *config.ApplicationConfig) *config.ApplicationConfig {
	return &config.ApplicationConfig{
		Context:     cfg.Context,
		DataPath:    GinkgoT().TempDir(),
		Auth:        cfg.Auth,
		Distributed: func() config.DistributedConfig { d := cfg.Distributed; d.InstanceID = "replica-b"; return d }(),
	}
}
