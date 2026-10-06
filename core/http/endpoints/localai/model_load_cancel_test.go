package localai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// opSender is the worker side as the HTTP layer sees it: it can stop one load
// operation, and it records every other call so a spec can tell what was and
// was not sent.
type opSender struct {
	nodes.NodeCommandSender // any call a spec does not expect panics

	mu           sync.Mutex
	hang         bool
	stops        []workerctl.ModelStopRequest
	unloads      []string
	stopBackends []string
}

func (s *opSender) StopLoadOperation(_ context.Context, _ string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops = append(s.stops, req)
	if s.hang {
		return workerctl.ModelStopReply{}, errors.New("nats: timeout")
	}
	return workerctl.ModelStopReply{Matched: true, Terminated: true}, nil
}

func (s *opSender) UnloadModelOnNode(_, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unloads = append(s.unloads, model)
	return nil
}

func (s *opSender) StopBackend(_, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopBackends = append(s.stopBackends, model)
	return nil
}

var _ = Describe("ModelLoadCancelEndpoint", func() {
	var (
		registry *nodes.NodeRegistry
		sender   *opSender
		ctx      context.Context
		node     *nodes.BackendNode
	)

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx = context.Background()
		sender = &opSender{}
		node = &nodes.BackendNode{Name: "worker-1", NodeType: nodes.NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
	})

	service := func() *nodes.LoadCancelService {
		return &nodes.LoadCancelService{Registry: registry, Stopper: sender}
	}
	serve := func(known func(string) bool) *echo.Echo {
		e := echo.New()
		e.POST("/api/models/:id/load-cancel", localai.ModelLoadCancelEndpoint(service, known))
		return e
	}
	post := func(e *echo.Echo, model, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/models/"+model+"/load-cancel", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	cancelBody := func(jobID string) string { return `{"job_id":"` + jobID + `"}` }
	placed := func(model string) *nodes.ModelLoadJob {
		job, claimed, err := registry.ClaimLoadJob(ctx, model, "frontend-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(registry.UpdateLoadJob(ctx, job.Ref(), nodes.LoadJobUpdate{State: nodes.LoadJobStateLoading, NodeID: node.ID, NodeName: node.Name})).To(Succeed())
		return job
	}
	known := func(string) bool { return true }

	It("rejects a missing job id, unknown fields and trailing JSON", func() {
		e := serve(known)
		for _, body := range []string{`{}`, `{"job_id":""}`, `{"job_id":"x","other":true}`, `{"job_id":"x"} {}`, `null`, `nope`} {
			Expect(post(e, "m", body).Code).To(Equal(http.StatusBadRequest), body)
		}
	})

	It("answers the whole matrix: stopped, stopping, gone, unknown, conflict", func() {
		e := serve(func(id string) bool { return id != "unknown" })
		job := placed("m")

		// A worker that confirms: 200 stopped, and the model is released shortly.
		rec := post(e, "m", cancelBody(job.Generation))
		Expect(rec.Code).To(Equal(http.StatusOK))
		var body schema.ModelLoadCancelResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.State).To(Equal("stopped"))
		Expect(sender.stops).To(HaveLen(1))
		Expect(sender.stops[0].OperationID).To(Equal(job.Generation))
		current, err := registry.GetLoadJob(ctx, "m")
		Expect(err).ToNot(HaveOccurred())
		Expect(current.CancelRequested).To(BeTrue())
		Expect(current.State).To(Equal(nodes.LoadJobStateFailed))

		// A different generation is current: 409 with its id.
		rec = post(e, "m", cancelBody("not-the-current-one"))
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.CurrentJobID).To(Equal(job.Generation))

		// No load at all: 200 gone for a known model, 404 for an unknown one.
		rec = post(e, "idle", cancelBody("x"))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.State).To(Equal("gone"))
		Expect(post(e, "unknown", cancelBody("x")).Code).To(Equal(http.StatusNotFound))

		// A server that is not distributed has no loads to cancel.
		plain := echo.New()
		plain.POST("/api/models/:id/load-cancel", localai.ModelLoadCancelEndpoint(func() *nodes.LoadCancelService { return nil }, known))
		Expect(post(plain, "m", cancelBody("x")).Code).To(Equal(http.StatusNotFound))
	})

	It("answers 202 while the worker is silent, and a repeat does not extend the hold", func() {
		sender.hang = true
		e := serve(known)
		job := placed("silent")

		rec := post(e, "silent", cancelBody(job.Generation))
		Expect(rec.Code).To(Equal(http.StatusAccepted))
		Expect(rec.Header().Get("Retry-After")).ToNot(BeEmpty())
		var body schema.ModelLoadCancelResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.State).To(Equal("stopping"))
		Expect(body.RetryAfter).To(BeNumerically(">", 100))

		first, err := registry.GetLoadJob(ctx, "silent")
		Expect(err).ToNot(HaveOccurred())
		Expect(first.StopDeadline).ToNot(BeNil())

		// Repeating retries the stop and keeps the same deadline.
		Expect(post(e, "silent", cancelBody(job.Generation)).Code).To(Equal(http.StatusAccepted))
		second, err := registry.GetLoadJob(ctx, "silent")
		Expect(err).ToNot(HaveOccurred())
		Expect(second.StopDeadline.Equal(*first.StopDeadline)).To(BeTrue(), "a repeated cancel must not extend the hold")
		Expect(sender.stops).To(HaveLen(2))

		// The worker answers on the next repeat: the cancel completes.
		sender.hang = false
		Expect(post(e, "silent", cancelBody(job.Generation)).Code).To(Equal(http.StatusOK))
	})

	It("cancels a staging load that has no node yet, without sending a stop", func() {
		job, _, err := registry.ClaimLoadJob(ctx, "staging", "frontend-a")
		Expect(err).ToNot(HaveOccurred())

		rec := post(serve(known), "staging", cancelBody(job.Generation))

		Expect(rec.Code).To(Equal(http.StatusAccepted))
		Expect(sender.stops).To(BeEmpty())
		current, err := registry.GetLoadJob(ctx, "staging")
		Expect(err).ToNot(HaveOccurred())
		Expect(current.CancelRequested).To(BeTrue())
	})

	It("is admin only, and not in the quota route registry", func() {
		for _, tc := range []struct {
			role string
			code int
		}{{"", http.StatusUnauthorized}, {auth.RoleUser, http.StatusForbidden}, {auth.RoleAdmin, http.StatusBadRequest}} {
			e := echo.New()
			e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					if tc.role != "" {
						c.Set("auth_user", &auth.User{Role: tc.role})
					}
					return next(c)
				}
			})
			e.POST("/api/models/:id/load-cancel", localai.ModelLoadCancelEndpoint(service, known), auth.RequireAdmin())
			Expect(post(e, "m", `{}`).Code).To(Equal(tc.code), tc.role)
		}
		for _, entry := range auth.RouteFeatureRegistry {
			Expect(entry.Pattern).ToNot(ContainSubstring("load-cancel"), "a management route must not be metered as a modality")
		}
	})

	Describe("unloading and deregistering a node", func() {
		It("cancels the in-flight load and still unloads the loaded replica", func() {
			job := placed("both")
			Expect(registry.SetNodeModel(ctx, node.ID, "both", 1, "loaded", "10.0.0.1:9001", 0)).To(Succeed())
			other := placed("elsewhere")
			Expect(registry.UpdateLoadJob(ctx, other.Ref(), nodes.LoadJobUpdate{NodeID: "another-node", NodeName: "n2"})).To(Succeed())

			e := echo.New()
			e.POST("/api/nodes/:id/models/unload", localai.UnloadModelOnNodeEndpoint(sender, registry))
			req := httptest.NewRequest(http.MethodPost, "/api/nodes/"+node.ID+"/models/unload", strings.NewReader(`{"model_name":"both"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(sender.stops).To(HaveLen(1))
			Expect(sender.stops[0].OperationID).To(Equal(job.Generation))
			Expect(sender.unloads).To(ConsistOf("both"), "the loaded replica is still unloaded")
			Expect(sender.stopBackends).To(ConsistOf("both"))
			replicas, err := registry.GetNodeModels(ctx, node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(replicas).To(BeEmpty())
			cancelled, err := registry.GetLoadJob(ctx, "both")
			Expect(err).ToNot(HaveOccurred())
			Expect(cancelled.CancelRequested).To(BeTrue())

			// A load of another model, placed on another node, is untouched.
			untouched, err := registry.GetLoadJob(ctx, "elsewhere")
			Expect(err).ToNot(HaveOccurred())
			Expect(untouched.State).To(Equal(nodes.LoadJobStateLoading))
		})

		It("stops the operations of a node before deregistering it", func() {
			job := placed("on-node")

			e := echo.New()
			e.DELETE("/api/nodes/:id", localai.DeregisterNodeEndpoint(registry, sender))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/nodes/"+node.ID, nil))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(sender.stops).To(HaveLen(1))
			Expect(sender.stops[0].OperationID).To(Equal(job.Generation))
		})
	})
})

var _ = Describe("ModelLoadStatusEndpoint activity", func() {
	It("projects the job id, the lease, and for a failed load the cause and the hold", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx := context.Background()
		e := echo.New()
		e.GET("/api/models/:id/load-status", localai.ModelLoadStatusEndpoint(func() nodes.LoadJobStore { return registry }))
		get := func(model string) (int, schema.ModelLoadingStatus) {
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/"+model+"/load-status", nil))
			var status schema.ModelLoadingStatus
			_ = json.Unmarshal(rec.Body.Bytes(), &status)
			return rec.Code, status
		}

		job, _, err := registry.ClaimLoadJob(ctx, "m", "frontend-a")
		Expect(err).ToNot(HaveOccurred())
		code, status := get("m")
		Expect(code).To(Equal(http.StatusOK))
		Expect(status.JobID).To(Equal(job.Generation))
		Expect(status.LeaseExpiresIn).ToNot(BeNil())
		Expect(*status.LeaseExpiresIn).To(BeNumerically(">", 0))
		Expect(status.Stopping).To(BeFalse())

		Expect(registry.FailLoadJob(ctx, job.Ref(), "context deadline exceeded", true)).To(Succeed())
		code, status = get("m")
		Expect(code).To(Equal(http.StatusOK))
		Expect(status.State).To(Equal(nodes.LoadJobStateFailed))
		Expect(status.LastError).To(Equal("context deadline exceeded"))
		Expect(status.Stopping).To(BeTrue(), "the remote work is not confirmed ended")
		Expect(status.StopDeadline).ToNot(BeNil())
		Expect(status.RetryAfter).To(BeNumerically(">", 100))
		Expect(status.LeaseExpiresIn).To(BeNil())
	})

	It("fails closed with 503 when the job table cannot be read", func() {
		e := echo.New()
		e.GET("/api/models/:id/load-status", localai.ModelLoadStatusEndpoint(func() nodes.LoadJobStore { return brokenStore{} }))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/m/load-status", nil))
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
	})
})

// brokenStore fails every read, like a database that is down.
type brokenStore struct{ nodes.LoadJobStore }

func (brokenStore) GetLoadJob(context.Context, string) (*nodes.ModelLoadJob, error) {
	return nil, errors.New("database unreachable")
}
