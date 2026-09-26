package localai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/agentpool"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
)

// DELETE /api/agent/tasks/:id takes the task id straight off the URL, so the
// only thing standing between one tenant and another tenant's task is how the
// service behind the route is scoped.
//
// The assertion is on the SURVIVING ROW rather than on the status code. Before
// the task subject carried its tenant, the handler answered 200 and destroyed
// the row; it now answers 404 because the task is not in this tenant's map. A
// status assertion would have seen nothing wrong in the first case, and reading
// the row keeps the spec honest whichever status a later change picks.
var _ = Describe("DELETE /api/agent/tasks/:id across tenants", func() {
	var (
		e      *echo.Echo
		app    *application.Application
		other  *agentpool.AgentJobService
		cancel context.CancelFunc
		taskID string
	)

	BeforeEach(func() {
		tmp := GinkgoT().TempDir()
		st, err := system.GetSystemState(
			system.WithModelPath(filepath.Join(tmp, "models")),
			system.WithBackendPath(filepath.Join(tmp, "backends")),
		)
		Expect(err).ToNot(HaveOccurred())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		app, err = application.New(config.WithContext(ctx), config.WithSystemState(st))
		Expect(err).ToNot(HaveOccurred())

		// One carrier, two tenants. The service the route reaches belongs to
		// u1; `other` is u2's, living on another replica as far as the bus is
		// concerned.
		bus := testutil.NewFakeBus()

		mine := app.AgentJobService()
		Expect(mine).ToNot(BeNil())
		mine.SetUserID("u1")
		mine.SetTaskSyncBus(bus)
		Expect(mine.LoadTasksFromFile()).To(Succeed())

		otherDir := GinkgoT().TempDir()
		otherCfg := config.NewApplicationConfig(
			config.WithDynamicConfigDir(otherDir),
			config.WithContext(ctx),
		)
		otherCfg.SystemState = st
		other = agentpool.NewAgentJobServiceWithPaths(otherCfg, nil, nil, nil,
			filepath.Join(otherDir, "tasks.json"), filepath.Join(otherDir, "jobs.json"))
		other.SetUserID("u2")
		other.SetTaskSyncBus(bus)
		Expect(other.LoadTasksFromFile()).To(Succeed())

		taskID, err = other.CreateTask(schema.Task{Name: "u2 only", Model: "m", Prompt: "p"})
		Expect(err).ToNot(HaveOccurred())

		e = echo.New()
		// Stand in for the auth middleware the real route runs behind: it is
		// what puts the caller into the echo context, and getJobService reads
		// the tenant from there.
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: "u1", Role: auth.RoleUser})
				return next(c)
			}
		})
		e.DELETE("/api/agent/tasks/:id", DeleteTaskEndpoint(app))
	})

	AfterEach(func() {
		cancel()
	})

	It("leaves the owning tenant's task readable", func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/agent/tasks/"+taskID, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		got, err := other.GetTask(taskID)
		Expect(err).ToNot(HaveOccurred(), "tenant u2's task must survive a delete issued by tenant u1")
		Expect(got.Name).To(Equal("u2 only"))
		Expect(other.ListTasks()).To(HaveLen(1))
	})
})
