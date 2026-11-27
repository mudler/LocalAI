package inproc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/system"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var _ = Describe("inproc.Client ListNodes", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		c        *Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		c = New(config.NewApplicationConfig(config.WithSystemState(systemState)), systemState, config.NewModelConfigLoader(GinkgoT().TempDir()), nil, nil, registry)
	})

	It("lists the nodes of the registry with the drain, like the HTTP client", func() {
		for _, name := range []string{"busy", "spare"} {
			node := &nodes.BackendNode{Name: name, NodeType: nodes.NodeTypeBackend, Address: name + ":50051", HTTPAddress: name + ":8080"}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
		}
		busy, err := registry.GetByName(ctx, "busy")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.SetNodeModel(ctx, busy.ID, "m", 0, "loaded", "busy:6000", 0)).To(Succeed())
		_, err = registry.DrainNode(ctx, busy.ID, nodes.DrainOptions{Timeout: time.Hour})
		Expect(err).ToNot(HaveOccurred())

		got, err := c.ListNodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(2))
		byID := map[string]int{}
		for i, n := range got {
			byID[n.ID] = i
		}
		Expect(byID).To(HaveKey(busy.ID))
		drained := got[byID[busy.ID]]
		Expect(drained.Status).To(Equal(nodes.StatusDraining))
		Expect(drained.LeavingReplicas).To(Equal(1))
		Expect(drained.DrainDeadline).ToNot(BeEmpty())

		// The HTTP client reads the same registry through GET /api/nodes.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			list, err := registry.ListWithExtras(r.Context())
			Expect(err).ToNot(HaveOccurred())
			_ = json.NewEncoder(w).Encode(list)
		}))
		defer srv.Close()
		viaHTTP, err := httpapi.New(srv.URL, "").ListNodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(viaHTTP))
	})

	It("lists nothing without a registry", func() {
		got, err := (&Client{}).ListNodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
})
