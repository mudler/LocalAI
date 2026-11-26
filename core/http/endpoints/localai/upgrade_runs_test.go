package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	addonendpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// runRouter serves the upgrade run routes as core/http/routes/localai.go
// registers them, behind the administrator check, with the role of the caller
// set by the test.
func runRouter(d UpgradeRunDeps, role *string) *echo.Echo {
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("auth_user", &auth.User{ID: "u-" + *role, Role: *role})
			return next(c)
		}
	})
	admin := auth.RequireAdmin()
	e.POST("/api/updates/runs/preview", PreviewUpgradeRunEndpoint(d), admin)
	e.POST("/api/updates/runs", StartUpgradeRunEndpoint(d), admin)
	e.GET("/api/updates/runs", ListUpgradeRunsEndpoint(d), admin)
	e.GET("/api/updates/runs/:id", GetUpgradeRunEndpoint(d), admin)
	e.POST("/api/updates/runs/:id/cancel", CancelUpgradeRunEndpoint(d), admin)
	e.POST("/api/updates/runs/:id/items/:key/cancel", CancelUpgradeRunItemEndpoint(d), admin)
	e.POST("/api/updates/runs/:id/retry", RetryUpgradeRunEndpoint(d), admin)
	e.DELETE("/api/updates/runs/:id", DismissUpgradeRunEndpoint(d), admin)
	return e
}

func doJSON(e *echo.Echo, method, path string, body any) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decodeRun(rec *httptest.ResponseRecorder) upgraderun.Run {
	var r upgraderun.Run
	Expect(json.Unmarshal(rec.Body.Bytes(), &r)).To(Succeed(), rec.Body.String())
	return r
}

func decodeRunError(rec *httptest.ResponseRecorder) UpgradeRunError {
	var e UpgradeRunError
	Expect(json.Unmarshal(rec.Body.Bytes(), &e)).To(Succeed(), rec.Body.String())
	return e
}

var _ = Describe("Upgrade run routes", func() {
	var (
		h    *upgraderuntest.Harness
		e    *echo.Echo
		role string
	)

	BeforeEach(func() {
		var err error
		h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		role = auth.RoleAdmin
		e = runRouter(UpgradeRunDeps{
			Runs:      func() *upgraderun.Service { return h.Service },
			Principal: func(c echo.Context) string { return auth.GetUser(c).ID },
		}, &role)
		kokoro := upgraderun.Candidate{Kind: upgraderun.KindGallery, Name: "kokoro", From: "v1", To: "v2"}
		kokoro.AddRisk(upgraderun.RiskStopsModels, map[string]string{"count": "2", "nodes": "3"})
		h.Backends.Add(kokoro)
		h.Models.Add(upgraderun.Candidate{Kind: upgraderun.KindModel, Name: "qwen", From: "d1", To: "d2"})
	})

	start := func(items ...upgraderun.StartItem) upgraderun.Run {
		rec := doJSON(e, http.MethodPost, "/api/updates/runs", upgraderun.StartRequest{Items: items, Source: upgraderun.SourceManual})
		Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())
		return decodeRun(rec)
	}

	// finish ends every running job and waits for the run to end.
	finish := func(id string) {
		Eventually(func() bool {
			run, err := h.Service.Get(context.Background(), id)
			Expect(err).NotTo(HaveOccurred())
			for _, it := range run.Items {
				if it.JobID != "" && it.Phase == upgraderun.PhaseRunning {
					h.Jobs.Complete(it.JobID)
				}
			}
			return run.Active()
		}, 10*time.Second, 20*time.Millisecond).Should(BeFalse())
	}

	It("previews with the plan fields, default_selected, risk and order", func() {
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/preview", map[string]any{})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var raw map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &raw)).To(Succeed())
		Expect(raw["order"]).To(Equal([]any{"gallery:kokoro", "model:qwen"}))
		items := raw["items"].([]any)
		first := items[0].(map[string]any)
		Expect(first).To(HaveKeyWithValue("key", "gallery:kokoro"))
		Expect(first).To(HaveKeyWithValue("from", "v1"))
		Expect(first).To(HaveKeyWithValue("to", "v2"))
		Expect(first).To(HaveKeyWithValue("expected_to", "v2"))
		Expect(first).To(HaveKeyWithValue("startable", true))
		Expect(first).To(HaveKeyWithValue("default_selected", true))
		Expect(first).To(HaveKeyWithValue("selected", true))
		// The numbers the review words the risk with, from the check.
		Expect(first["risk"]).To(Equal([]any{map[string]any{"code": "stops_models", "params": map[string]any{"count": "2", "nodes": "3"}}}))
		Expect(items[1].(map[string]any)).To(HaveKeyWithValue("default_selected", false))
	})

	It("refuses a preview of an unknown kind with 400", func() {
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/preview", map[string]any{"items": []map[string]string{{"kind": "plugin", "name": "x"}}})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeInvalid))
	})

	It("starts a run with 202 and the actor of the request, lists it and reads it", func() {
		run := start(upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: "kokoro", ExpectedTo: "v2"})
		Expect(run.ID).NotTo(BeEmpty())
		Expect(run.State).To(Equal(upgraderun.RunRunning))
		Expect(run.Actor).To(Equal("u-admin"))
		Eventually(h.Starter.Actors).Should(Equal([]string{"u-admin"}))

		rec := doJSON(e, http.MethodGet, "/api/updates/runs?active=1", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var list UpgradeRunList
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list.Runs).To(HaveLen(1))
		Expect(list.Runs[0].ID).To(Equal(run.ID))

		rec = doJSON(e, http.MethodGet, "/api/updates/runs/"+run.ID, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeRun(rec).Items[0].Key).To(Equal("gallery:kokoro"))

		finish(run.ID)
		rec = doJSON(e, http.MethodGet, "/api/updates/runs?active=1", nil)
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list.Runs).To(BeEmpty())
	})

	DescribeTable("refuses a start with a typed error",
		func(body any, status int, code string) {
			rec := doJSON(e, http.MethodPost, "/api/updates/runs", body)
			Expect(rec.Code).To(Equal(status), rec.Body.String())
			Expect(decodeRunError(rec).Code).To(Equal(code))
		},
		Entry("no item", upgraderun.StartRequest{}, http.StatusBadRequest, upgraderun.CodeInvalid),
		Entry("unknown kind", map[string]any{"items": []map[string]string{{"kind": "plugin", "name": "x"}}}, http.StatusBadRequest, upgraderun.CodeInvalid),
		Entry("source auto", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindGallery, Name: "kokoro"}}, Source: upgraderun.SourceAuto}, http.StatusBadRequest, upgraderun.CodeInvalid),
		Entry("malformed body", "not json", http.StatusBadRequest, upgraderun.CodeInvalid),
		Entry("no update", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindGallery, Name: "missing"}}}, http.StatusConflict, upgraderun.CodeNoUpdate),
		Entry("stale target", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindGallery, Name: "kokoro", ExpectedTo: "v9"}}}, http.StatusConflict, upgraderun.CodeStale),
	)

	It("refuses a second start with 409 run_active", func() {
		start(upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: "kokoro"})
		rec := doJSON(e, http.MethodPost, "/api/updates/runs", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindModel, Name: "qwen"}}})
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeRunActive))
	})

	It("answers 404 not_found for an unknown run on every run route", func() {
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/api/updates/runs/nope"},
			{http.MethodPost, "/api/updates/runs/nope/cancel"},
			{http.MethodPost, "/api/updates/runs/nope/items/gallery:x/cancel"},
			{http.MethodPost, "/api/updates/runs/nope/retry"},
			{http.MethodDelete, "/api/updates/runs/nope"},
		} {
			rec := doJSON(e, r.method, r.path, nil)
			Expect(rec.Code).To(Equal(http.StatusNotFound), r.method+" "+r.path)
			Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeNotFound))
		}
	})

	It("cancels a queued item and the run, then refuses what a finished run cannot do", func() {
		run := start(upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: "kokoro"}, upgraderun.StartItem{Kind: upgraderun.KindModel, Name: "qwen"})
		// The model waits for the backends, so it is queued and cancellable.
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/items/"+url.PathEscape("model:qwen")+"/cancel", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		// The runner applies the cancel on its goroutine.
		Eventually(func() upgraderun.Phase {
			r, _ := h.Service.Get(context.Background(), run.ID)
			return r.Item("model:qwen").Phase
		}, 5*time.Second, 10*time.Millisecond).Should(Equal(upgraderun.PhaseCancelled))

		rec = doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/items/"+url.PathEscape("model:qwen")+"/cancel", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeNotCancellable))

		rec = doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/items/"+url.PathEscape("model:other")+"/cancel", nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound))

		// Dismiss and retry wait for the end of the run.
		rec = doJSON(e, http.MethodDelete, "/api/updates/runs/"+run.ID, nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeRunNotFinished))
		rec = doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/retry", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeRunNotFinished))

		rec = doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/cancel", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Eventually(func() upgraderun.RunState {
			r, _ := h.Service.Get(context.Background(), run.ID)
			if it := r.Item("gallery:kokoro"); it != nil && it.JobID != "" && !it.Phase.Terminal() {
				h.Jobs.Complete(it.JobID)
			}
			return r.State
		}, 10*time.Second, 20*time.Millisecond).ShouldNot(Equal(upgraderun.RunRunning))

		rec = doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/cancel", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeRunFinished))

		rec = doJSON(e, http.MethodDelete, "/api/updates/runs/"+run.ID, nil)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
	})

	It("refuses a retry of a run whose items all updated with 409 nothing_to_retry", func() {
		run := start(upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: "kokoro"})
		Eventually(func() string { return h.Starter.Job("gallery:kokoro") }, 5*time.Second).ShouldNot(BeEmpty())
		h.Backends.Remove("gallery:kokoro")
		finish(run.ID)
		r, err := h.Service.Get(context.Background(), run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Item("gallery:kokoro").Phase).To(Equal(upgraderun.PhaseUpdated))
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/retry", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(decodeRunError(rec).Code).To(Equal(upgraderun.CodeNothingToRetry))
	})

	It("retries a failed item as a new run", func() {
		h.Starter.SetErr(errors.New("queue full"))
		run := start(upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: "kokoro"})
		Eventually(func() bool { r, _ := h.Service.Get(context.Background(), run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())
		h.Starter.SetErr(nil)
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/retry", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeRun(rec).ID).NotTo(Equal(run.ID))
	})

	It("runs a retry as the administrator who retries, not as the one who started the run", func() {
		alice := addonendpoints.EncodeRunActor(contract.Actor{UserID: "alice", CredentialID: "sess-a", Mode: "session"}, true)
		bob := addonendpoints.EncodeRunActor(contract.Actor{UserID: "bob", CredentialID: "sess-b", Mode: "session"}, true)
		who := alice
		e = runRouter(UpgradeRunDeps{
			Runs:      func() *upgraderun.Service { return h.Service },
			Principal: func(echo.Context) string { return who },
		}, &role)
		h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", From: "d1", To: "d2"})

		h.Starter.SetErr(&upgraderun.StartError{ErrorKey: "unavailable", Retryable: true})
		run := start(upgraderun.StartItem{Kind: upgraderun.KindAddon, Name: "notes"})
		Expect(run.Actor).To(Equal("alice"))
		Eventually(func() bool { r, _ := h.Service.Get(context.Background(), run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())
		before := len(h.Starter.Actors())
		Expect(h.Starter.Actors()).To(HaveEach(Equal(alice)))

		h.Starter.SetErr(nil)
		who = bob
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/retry", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		again := decodeRun(rec)
		Expect(again.Actor).To(Equal("bob"))
		stored, err := h.Service.Get(context.Background(), again.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Actor).To(Equal(bob))
		Eventually(func() int { return len(h.Starter.Actors()) }, 5*time.Second).Should(BeNumerically(">", before))
		Expect(h.Starter.Actors()[before:]).To(HaveEach(Equal(bob)), "the starter only sees the administrator who retried")
	})

	It("refuses a retry of add-on items from a caller that is not a browser session, whoever started the run", func() {
		alice := addonendpoints.EncodeRunActor(contract.Actor{UserID: "alice", CredentialID: "sess-a", Mode: "session"}, true)
		who := alice
		e = runRouter(UpgradeRunDeps{
			Runs:      func() *upgraderun.Service { return h.Service },
			Principal: func(echo.Context) string { return who },
		}, &role)
		h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", From: "d1", To: "d2"})
		h.Starter.SetErr(&upgraderun.StartError{ErrorKey: "unavailable", Retryable: true})
		run := start(upgraderun.StartItem{Kind: upgraderun.KindAddon, Name: "notes"})
		Eventually(func() bool { r, _ := h.Service.Get(context.Background(), run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())

		who = addonendpoints.EncodeRunActor(contract.Actor{UserID: "bob", CredentialID: "key-b", Mode: "key"}, false)
		rec := doJSON(e, http.MethodPost, "/api/updates/runs/"+run.ID+"/retry", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(decodeRunError(rec).Code).To(Equal(addonendpoints.RunErrNotHumanSurface))
	})

	It("never answers a credential, a path or raw output that a failing operation carried", func() {
		run := start(upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: "kokoro"})
		var job string
		Eventually(func() string { job = h.Starter.Job("gallery:kokoro"); return job }, 5*time.Second).ShouldNot(BeEmpty())
		h.Jobs.Set(job, galleryop.OpStatus{Phase: "fetching", Processed: true,
			Error: errors.New("pull https://user:pass@host/v2/x failed: Bearer abc\nopen /home/alice/.cache/x/key.pem")})
		Eventually(func() bool { r, _ := h.Service.Get(context.Background(), run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())
		for _, path := range []string{"/api/updates/runs/" + run.ID, "/api/updates/runs"} {
			rec := doJSON(e, http.MethodGet, path, nil)
			Expect(rec.Code).To(Equal(http.StatusOK))
			body := rec.Body.String()
			Expect(body).To(ContainSubstring("key.pem"), path)
			for _, secret := range []string{"user:pass", "https://user", "Bearer abc", "/home/alice", `\n`} {
				Expect(body).NotTo(ContainSubstring(secret), path)
			}
		}
	})

	It("refuses a caller who is not an administrator on every route", func() {
		role = auth.RoleUser
		for _, r := range []struct{ method, path string }{
			{http.MethodPost, "/api/updates/runs/preview"},
			{http.MethodPost, "/api/updates/runs"},
			{http.MethodGet, "/api/updates/runs"},
			{http.MethodGet, "/api/updates/runs/x"},
			{http.MethodPost, "/api/updates/runs/x/cancel"},
			{http.MethodPost, "/api/updates/runs/x/items/gallery:x/cancel"},
			{http.MethodPost, "/api/updates/runs/x/retry"},
			{http.MethodDelete, "/api/updates/runs/x"},
		} {
			rec := doJSON(e, r.method, r.path, nil)
			Expect(rec.Code).To(Equal(http.StatusForbidden), r.method+" "+r.path)
		}
	})

	It("answers 503 when the server keeps no runs", func() {
		e = runRouter(UpgradeRunDeps{Runs: func() *upgraderun.Service { return nil }}, &role)
		rec := doJSON(e, http.MethodGet, "/api/updates/runs", nil)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
	})
})

var _ = Describe("Single upgrade routes as runs of one item", func() {
	var (
		h      *upgraderuntest.Harness
		e      *echo.Echo
		legacy int
	)

	BeforeEach(func() {
		var err error
		h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		legacy = 0
		d := UpgradeRunDeps{Runs: func() *upgraderun.Service { return h.Service }}
		old := func(c echo.Context) error {
			legacy++
			return c.JSON(http.StatusOK, map[string]any{"jobID": "legacy", "uuid": "legacy", "statusUrl": "/api/backends/job/legacy", "message": "Backend upgrade started"})
		}
		e = echo.New()
		e.POST("/api/backends/upgrade/:name", BackendUpgradeViaRun(d, nil, old, UIBackendUpgradeAnswer))
		e.POST("/backends/upgrade/:name", BackendUpgradeViaRun(d, nil, old, APIBackendUpgradeAnswer))
		h.Backends.Add(upgraderun.Candidate{Kind: upgraderun.KindGallery, Name: "kokoro", From: "v1", To: "v2"})
	})

	It("keeps the fields of POST /api/backends/upgrade/{name} and adds the run", func() {
		rec := doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		job := h.Starter.Job("gallery:kokoro")
		Expect(job).NotTo(BeEmpty())
		Expect(body).To(HaveKeyWithValue("jobID", job))
		Expect(body).To(HaveKeyWithValue("uuid", job))
		Expect(body).To(HaveKeyWithValue("statusUrl", "/api/backends/job/"+job))
		Expect(body).To(HaveKeyWithValue("message", "Backend upgrade started"))
		Expect(body["run"]).To(HaveKey("id"))
		Expect(legacy).To(BeZero())
	})

	It("keeps the fields of POST /backends/upgrade/{name} and adds the run", func() {
		rec := doJSON(e, http.MethodPost, "/backends/upgrade/kokoro", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var body BackendUpgradeRunResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.ID).To(Equal(h.Starter.Job("gallery:kokoro")))
		Expect(body.StatusURL).To(HaveSuffix("backends/jobs/" + body.ID))
		Expect(body.Run).NotTo(BeNil())
	})

	It("refuses with 409 run_active while another run runs", func() {
		Expect(doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", nil).Code).To(Equal(http.StatusOK))
		rec := doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body).To(HaveKeyWithValue("code", "run_active"))
		Expect(body["error"]).NotTo(BeEmpty())
	})

	It("refuses an expected_to that moved with 409 stale", func() {
		rec := doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro?expected_to=v9", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring(`"code":"stale"`))
		rec = doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", map[string]string{"expected_to": "v9"})
		Expect(rec.Code).To(Equal(http.StatusConflict))
	})

	It("never takes the old path while a run runs, also for a backend the run's check does not list", func() {
		Expect(doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", nil).Code).To(Equal(http.StatusOK))
		for _, path := range []string{"/api/backends/upgrade/other", "/backends/upgrade/other"} {
			rec := doJSON(e, http.MethodPost, path, nil)
			Expect(rec.Code).To(Equal(http.StatusConflict), path)
			Expect(rec.Body.String()).To(ContainSubstring(`"code":"run_active"`))
		}
		Expect(legacy).To(BeZero(), "no direct upgrade beside the run")
	})

	It("answers through the old path when the run's check does not list the backend", func() {
		rec := doJSON(e, http.MethodPost, "/api/backends/upgrade/other", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(legacy).To(Equal(1))
		Expect(rec.Body.String()).To(ContainSubstring(`"jobID":"legacy"`))
	})

	It("answers through the old path when the server keeps no runs", func() {
		e = echo.New()
		e.POST("/api/backends/upgrade/:name", BackendUpgradeViaRun(UpgradeRunDeps{}, nil, func(c echo.Context) error {
			legacy++
			return c.NoContent(http.StatusOK)
		}, UIBackendUpgradeAnswer))
		Expect(doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", nil).Code).To(Equal(http.StatusOK))
		Expect(legacy).To(Equal(1))
	})

	It("refuses through the backend upgrade guard before a run starts", func() {
		gs := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		gs.SetBackendManager(listingManager{backends: gallery.SystemBackends{
			"kokoro": {Name: "kokoro", Metadata: &gallery.BackendMetadata{Name: "kokoro"}},
			"other":  {Name: "other", Metadata: &gallery.BackendMetadata{Name: "other"}},
		}})
		var legacyBody string
		old := func(c echo.Context) error {
			legacy++
			raw, err := io.ReadAll(c.Request().Body)
			Expect(err).NotTo(HaveOccurred())
			legacyBody = string(raw)
			return c.NoContent(http.StatusOK)
		}
		e = echo.New()
		e.POST("/api/backends/upgrade/:name", BackendUpgradeViaRun(UpgradeRunDeps{Runs: func() *upgraderun.Service { return h.Service }},
			NewBackendUpgradeGuard(gs, nil), old, UIBackendUpgradeAnswer))

		rec := doJSON(e, http.MethodPost, "/api/backends/upgrade/missing", nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Body.String()).To(ContainSubstring(`"code":"not_installed"`))
		Expect(legacy).To(BeZero())
		runs, err := h.Service.List(context.Background(), upgraderun.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(BeEmpty())

		rec = doJSON(e, http.MethodPost, "/api/backends/upgrade/other", map[string]string{"expected_to": "v5"})
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(legacy).To(Equal(1))
		Expect(legacyBody).To(ContainSubstring(`"expected_to":"v5"`), "the guard leaves the body readable for the handler")
	})

	DescribeTable("answers 202 with the run, and no job, when the operation is not accepted within the wait",
		func(path string) {
			gate := make(chan struct{})
			DeferCleanup(func() { close(gate) })
			h.Starter.Block(gate)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())
			var body SingleUpgradePending
			Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Code).To(Equal("pending"))
			Expect(body.RunID).NotTo(BeEmpty())
			Expect(body.Run).NotTo(BeNil())
			Expect(body.Run.ID).To(Equal(body.RunID))
			Expect(rec.Body.String()).NotTo(ContainSubstring("started"))
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"jobID"`))
			Expect(legacy).To(BeZero())
		},
		Entry("POST /api/backends/upgrade/{name}", "/api/backends/upgrade/kokoro"),
		Entry("POST /backends/upgrade/{name}", "/backends/upgrade/kokoro"),
	)

	It("answers 409 with the error key when the item could not start", func() {
		h.Starter.SetErr(&upgraderun.StartError{ErrorKey: "download", Err: errors.New("registry down")})
		rec := doJSON(e, http.MethodPost, "/api/backends/upgrade/kokoro", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring(`"code":"download"`))
		Expect(rec.Body.String()).To(ContainSubstring("registry down"))
	})
})
