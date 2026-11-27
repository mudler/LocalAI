package application

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

var _ = Describe("the plugins service wiring", func() {
	ids := func(s *plugins.Service) []string {
		list, err := s.ListMarketplaces(context.Background())
		Expect(err).NotTo(HaveOccurred())
		out := []string{}
		for _, m := range list {
			out = append(out, m.ID)
		}
		return out
	}

	It("starts with the file store under the data path when no database is set", func() {
		dp := GinkgoT().TempDir()
		cfg := config.NewApplicationConfig(config.WithDataPath(dp))
		svc, _, err := newPluginsService(cfg, nil, nil, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.Store()).To(BeAssignableToTypeOf(&store.FileStore{}))
		Expect(svc.Seed(context.Background())).To(Succeed())
		Expect(ids(svc)).To(Equal([]string{"localai"}))
		Expect(filepath.Join(dp, "plugins", "store.json")).To(BeAnExistingFile())
		Expect(filepath.Join(dp, "plugins", "scratch")).To(BeADirectory())
	})

	It("seeds the flag's marketplaces on a fresh store only", func() {
		dp := GinkgoT().TempDir()
		cfg := config.NewApplicationConfig(config.WithDataPath(dp), config.WithPluginMarketplaces("team=owner/repo"))
		svc, _, err := newPluginsService(cfg, nil, nil, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.Seed(context.Background())).To(Succeed())
		Expect(ids(svc)).To(Equal([]string{"team"}))

		cfg = config.NewApplicationConfig(config.WithDataPath(dp), config.WithPluginMarketplaces("other=owner/other"))
		svc, _, err = newPluginsService(cfg, nil, nil, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.Seed(context.Background())).To(Succeed())
		Expect(ids(svc)).To(Equal([]string{"team"}))
	})

	It("refuses a bad flag", func() {
		cfg := config.NewApplicationConfig(config.WithDataPath(GinkgoT().TempDir()), config.WithPluginMarketplaces("ssh://example.test/a.git"))
		_, _, err := newPluginsService(cfg, nil, nil, nil, nil, nil)
		Expect(err).To(MatchError(ContainSubstring("--plugin-marketplaces")))
	})

	It("stays off without a data path, and says why", func() {
		a := &Application{applicationConfig: config.NewApplicationConfig()}
		Expect(a.initPlugins()).To(Succeed())
		Expect(a.Plugins()).To(BeNil())
		Expect(a.PluginsUnavailable()).To(ContainSubstring("data path"))
	})
})
