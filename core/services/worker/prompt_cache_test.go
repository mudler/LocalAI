package worker

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/promptcache"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// noModels is the registry view the senders need for the verbs that name a
// model; the prompt cache verbs name none.
type noModels struct{}

func (noModels) FindNodesWithModel(context.Context, string) ([]nodes.BackendNode, error) {
	return nil, nil
}
func (noModels) RemoveNodeModel(context.Context, string, string, int) error       { return nil }
func (noModels) RemoveAllNodeModelReplicas(context.Context, string, string) error { return nil }

type cacheStats struct{}

func (cacheStats) Stat(string) (pc.FSStats, error) {
	return pc.FSStats{Free: 500 << 30, Total: 1000 << 30, Device: 1}, nil
}

// promptCacheCarrier is one way the frontend reaches the worker: the sender
// of the frontend half and the control server of the worker half.
type promptCacheCarrier struct {
	sender nodes.PromptCacheControl
	serve  controlServer
}

var promptCacheCarriers = map[string]func(nodeID string) promptCacheCarrier{
	"NATS": func(nodeID string) promptCacheCarrier {
		bus := testutil.NewFakeBus()
		return promptCacheCarrier{
			sender: nodes.NewRemoteUnloaderAdapter(noModels{}, bus, time.Minute, time.Minute),
			serve:  newNATSControlServer(bus, nodeID),
		}
	},
	"the HTTP control plane of the tunnel": func(string) promptCacheCarrier {
		ctl := newHTTPControlServer()
		srv := httptest.NewServer(ctl)
		DeferCleanup(srv.Close)
		dialFor := func(string) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
			}
		}
		return promptCacheCarrier{
			sender: nodes.NewTunnelControl(noModels{}, nodes.NewControlClient(dialFor, ""), time.Minute, time.Minute),
			serve:  ctl,
		}
	},
}

var _ = Describe("the prompt cache verbs between a frontend and a worker", func() {
	for name, newCarrier := range promptCacheCarriers {
		Context("over "+name, func() {
			var (
				ctx      context.Context
				root     string
				half     *promptcachesvc.Worker
				node     *promptcachesvc.Node
				carrier  promptCacheCarrier
				front    *promptcachesvc.Distributed
				settings config.PromptCacheConfig
			)

			BeforeEach(func() {
				ctx = context.Background()
				root = filepath.Join(GinkgoT().TempDir(), "prompt-cache")
				half = promptcachesvc.NewWorker(false, "incarnation-1")
				node = promptcachesvc.NewNode(root, "", half.Settings,
					promptcachesvc.WithUsers(half.Users), promptcachesvc.WithStats(cacheStats{}))
				half.Attach(node)
				carrier = newCarrier("w1")
				Expect(registerPromptCacheVerbs(carrier.serve, half)).To(Succeed())

				db, err := sqlitedb.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				Expect(err).ToNot(HaveOccurred())
				// Close the database before the temporary folder is removed:
				// Windows does not delete a file that is still open. Cleanups
				// run in reverse order, so this one runs first.
				sqlDB, err := db.DB()
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(sqlDB.Close)
				ledger, err := promptcachesvc.NewDBLedger(db)
				Expect(err).ToNot(HaveOccurred())
				settings = config.DefaultPromptCacheConfig()
				front = &promptcachesvc.Distributed{
					Ledger: ledger,
					Sender: carrier.sender,
					Peers: func(context.Context) ([]promptcachesvc.Peer, error) {
						return []promptcachesvc.Peer{{ID: "w1", Name: "worker-1", Incarnation: "incarnation-1", Online: true}}, nil
					},
					Settings: func() workerctl.PromptCacheSettings { return promptcachesvc.WireSettings(settings, false, 0) },
				}
			})

			entry := func(model, ns string, at time.Time) string {
				dir := filepath.Join(root, "llama-cpp", "1111111111111111-"+promptcache.ModelID(model))
				Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
				p := filepath.Join(dir, "cccccccccccccccc-"+promptcache.NSID(promptcache.Salt(ns))+"-"+strings.Repeat("a", 32)+".cache")
				Expect(os.WriteFile(p, []byte("kv"), 0o600)).To(Succeed())
				Expect(os.Chtimes(p, at, at)).To(Succeed())
				return p
			}

			It("brings a settings change to the worker", func() {
				settings.TTL = time.Hour
				settings.DiskDisabled = true
				Expect(front.Tick(ctx)).To(Succeed())
				Expect(half.Settings().TTL).To(Equal(time.Hour))
				Expect(node.Enabled()).To(BeFalse())
				Expect(node.BackendEnv()).To(HaveKeyWithValue(config.EnvPromptCacheRoot, ""))
			})

			It("carries a purge, and the usage back", func() {
				hourAgo := time.Now().Add(-time.Hour)
				mine := entry("m.gguf", "user:1", hourAgo)
				other := entry("m.gguf", "user:2", hourAgo)
				_, err := node.Sweep(ctx)
				Expect(err).ToNot(HaveOccurred())

				rep, err := promptcachesvc.Report(ctx, front, true, false, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(rep.Nodes).To(HaveLen(1))
				Expect(rep.Nodes[0].Node).To(Equal("worker-1"))
				Expect(rep.Nodes[0].Files).To(Equal(2))
				Expect(rep.Nodes[0].Status.State).To(Equal(pc.StateOn))

				res, err := front.Purge(ctx, promptcachesvc.NamespaceScope("user:1"))
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Files).To(Equal(1))
				Expect(mine).ToNot(BeAnExistingFile())
				Expect(other).To(BeAnExistingFile())
			})

			It("answers with the incarnation of the worker, so a restart is seen", func() {
				reply, err := carrier.sender.PromptCacheSync(ctx, "w1", workerctl.PromptCacheSyncRequest{}, time.Second)
				Expect(err).ToNot(HaveOccurred())
				Expect(reply.Success).To(BeTrue())
				Expect(reply.Incarnation).To(Equal("incarnation-1"))
			})
		})
	}

	It("refuses a malformed sync on NATS with the typed refusal", func() {
		bus := testutil.NewFakeBus()
		Expect(registerPromptCacheVerbs(newNATSControlServer(bus, "w1"), promptcachesvc.NewWorker(false, "i"))).To(Succeed())
		raw, err := bus.Request(messaging.SubjectNodePromptCacheSync("w1"), []byte("not json"), time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("invalid request"))
	})

	It("reads a worker that predates the verbs as no route, and reports it as unreachable", func() {
		ctl := newHTTPControlServer() // serves nothing of the prompt cache
		srv := httptest.NewServer(ctl)
		DeferCleanup(srv.Close)
		dialFor := func(string) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
			}
		}
		sender := nodes.NewTunnelControl(noModels{}, nodes.NewControlClient(dialFor, ""), time.Minute, time.Minute)
		_, err := sender.PromptCacheSync(context.Background(), "old", workerctl.PromptCacheSyncRequest{}, time.Second)
		Expect(err).To(MatchError(nodes.ErrNoRoute))
		_, err = sender.PromptCacheUsage(context.Background(), "old", time.Second)
		Expect(err).To(MatchError(nodes.ErrNoRoute))
	})
})
