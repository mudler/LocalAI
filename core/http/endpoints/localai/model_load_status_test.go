package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("ModelLoadStatusEndpoint", func() {
	get := func(store func() nodes.LoadJobStore, modelID string) *httptest.ResponseRecorder {
		e := echo.New()
		e.GET("/api/models/:id/load-status", localai.ModelLoadStatusEndpoint(store))
		req := httptest.NewRequest(http.MethodGet, "/api/models/"+modelID+"/load-status", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("404s when the server is not running distributed", func() {
		rec := get(nil, "some-model")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	Context("with a registry", func() {
		var registry *nodes.NodeRegistry

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			registry, err = nodes.NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
		})

		store := func(r *nodes.NodeRegistry) func() nodes.LoadJobStore {
			return func() nodes.LoadJobStore { return r }
		}

		It("404s when no load is running for the model", func() {
			rec := get(store(registry), "idle-model")
			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})

		It("reports the live progress of a running load", func() {
			ctx := context.Background()
			_, claimed, err := registry.ClaimLoadJob(ctx, "big-model", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(registry.UpdateLoadJob(ctx, "big-model", nodes.LoadJobUpdate{
				State: nodes.LoadJobStateStaging, NodeID: "n1", NodeName: "nvidia-thor",
				BytesSent: 1000, TotalBytes: 4000, FileIndex: 1, TotalFiles: 1,
			})).To(Succeed())

			rec := get(store(registry), "big-model")
			Expect(rec.Code).To(Equal(http.StatusOK))

			var status schema.ModelLoadingStatus
			Expect(json.Unmarshal(rec.Body.Bytes(), &status)).To(Succeed())
			Expect(status.Model).To(Equal("big-model"))
			Expect(status.State).To(Equal(nodes.LoadJobStateStaging))
			Expect(status.Node).To(Equal("nvidia-thor"))
			Expect(status.Progress).To(BeNumerically("~", 25, 0.01))
			Expect(status.TotalBytes).To(Equal(int64(4000)))
		})
	})
})
