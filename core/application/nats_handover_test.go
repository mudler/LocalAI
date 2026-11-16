package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("What workers are told about NATS", func() {
	var (
		ctx      context.Context
		settings *cluster.SettingsStore
		cfg      *config.ApplicationConfig
		rt       *carrierRuntime
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		settings, err = cluster.NewSettingsStore(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		cfg = &config.ApplicationConfig{Distributed: config.DistributedConfig{NatsURL: "nats://frontend-flag:4222"}}
		rt = &carrierRuntime{cfg: cfg, settings: settings}
	})

	It("is the address for workers when one is stored", func() {
		Expect(settings.Set(ctx, cluster.SettingNATSURL, "nats://frontends:4222", "spec")).To(Succeed())
		Expect(settings.Set(ctx, cluster.SettingNATSWorkerURL, "nats://workers.example:4222", "spec")).To(Succeed())
		Expect(rt.WorkerURL(ctx)).To(Equal("nats://workers.example:4222"))
	})

	It("falls back to the address of the frontends, then to the flag of this replica", func() {
		Expect(rt.WorkerURL(ctx)).To(Equal("nats://frontend-flag:4222"))
		Expect(settings.Set(ctx, cluster.SettingNATSURL, "nats://frontends:4222", "spec")).To(Succeed())
		Expect(rt.WorkerURL(ctx)).To(Equal("nats://frontends:4222"))
	})

	It("is empty when the deployment has no NATS", func() {
		cfg.Distributed.NatsURL = ""
		Expect(rt.WorkerURL(ctx)).To(BeEmpty())
	})

	It("hands over the CA of the server and says nothing when there is none", func() {
		Expect(rt.CAPEM()).To(BeEmpty())
		path := filepath.Join(GinkgoT().TempDir(), "ca.pem")
		Expect(os.WriteFile(path, []byte("-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"), 0o600)).To(Succeed())
		cfg.Distributed.NatsTLSCA = path
		Expect(rt.CAPEM()).To(ContainSubstring("BEGIN CERTIFICATE"))
	})

	It("fails to hand over a CA file it cannot read, and does not make up one", func() {
		cfg.Distributed.NatsTLSCA = filepath.Join(GinkgoT().TempDir(), "missing.pem")
		_, err := rt.CAPEM()
		Expect(err).To(HaveOccurred())
	})

	It("says that workers need a certificate when this replica holds one", func() {
		Expect(rt.ClientTLS()).To(BeFalse())
		cfg.Distributed.NatsTLSCert = "/some/client.pem"
		cfg.Distributed.NatsTLSKey = "/some/client.key"
		Expect(rt.ClientTLS()).To(BeTrue())
	})
})
