// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func jsonInto(rec *httptest.ResponseRecorder, v any) error {
	return json.Unmarshal(rec.Body.Bytes(), v)
}

type recordedWebhooks struct {
	mu    sync.Mutex
	calls []studio.WebhookCall
}

func (r *recordedWebhooks) Send(_ context.Context, c studio.WebhookCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	return nil
}

func (r *recordedWebhooks) all() []studio.WebhookCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]studio.WebhookCall(nil), r.calls...)
}

// The agent tasks and jobs API keeps the paths and JSON of the old agent pool.
// A job is a background chat of its owner.
var _ = Describe("Agent tasks and jobs endpoints", func() {
	var (
		s     *studioServer
		hooks *recordedWebhooks
	)

	BeforeEach(func() {
		hooks = &recordedWebhooks{}
		studioWebhooks = hooks
		DeferCleanup(func() { studioWebhooks = nil })
		s = newStudioServer()
	})

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/agent/tasks"},
		{http.MethodPost, "/api/agent/tasks"},
		{http.MethodGet, "/api/agent/tasks/t1"},
		{http.MethodPut, "/api/agent/tasks/t1"},
		{http.MethodDelete, "/api/agent/tasks/t1"},
		{http.MethodPost, "/api/agent/tasks/name/execute"},
		{http.MethodPost, "/api/agent/jobs/execute"},
		{http.MethodGet, "/api/agent/jobs"},
		{http.MethodGet, "/api/agent/jobs/j1"},
		{http.MethodPost, "/api/agent/jobs/j1/cancel"},
		{http.MethodPost, "/api/agent/jobs/j1/rerun"},
		{http.MethodDelete, "/api/agent/jobs/j1"},
	}

	It("answers 401 without credentials and 403 without the mcp_jobs feature", func() {
		for _, r := range routes {
			Expect(s.do("", r.method, r.path, nil).Code).To(Equal(http.StatusUnauthorized), "no credentials: "+r.method+" "+r.path)
			Expect(s.do("dave", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), "dave: "+r.method+" "+r.path)
			Expect(s.do("carol", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), "carol: "+r.method+" "+r.path)
		}
		Expect(s.do("alice", http.MethodGet, "/api/agent/tasks", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/agent/jobs", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("root", http.MethodGet, "/api/agent/tasks", nil).Code).To(Equal(http.StatusOK))
	})

	task := map[string]any{"name": "Summary", "model": "test-model", "prompt": "Summarise {{.topic}}",
		"webhooks": []map[string]any{{"url": "https://hooks.example.test/done", "headers": map[string]string{"Authorization": "Bearer top-secret"}}}}

	createTask := func(user string, body map[string]any) string {
		rec := s.do(user, http.MethodPost, "/api/agent/tasks", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}

	waitJob := func(user, id string) map[string]any {
		var got map[string]any
		Eventually(func() string {
			rec := s.do(user, http.MethodGet, "/api/agent/jobs/"+id, nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			got = decodeJSON(rec)
			status, _ := got["status"].(string)
			return status
		}, "20s", "20ms").Should(Equal("completed"))
		return got
	}

	It("creates, reads, lists, updates and deletes a task, and never returns a header value", func() {
		id := createTask("alice", task)
		rec := s.do("alice", http.MethodGet, "/api/agent/tasks/"+id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		got := decodeJSON(rec)
		Expect(got).To(And(HaveKeyWithValue("name", "Summary"), HaveKeyWithValue("model", "test-model"), HaveKeyWithValue("enabled", true)))
		Expect(rec.Body.String()).NotTo(ContainSubstring("top-secret"))
		Expect(rec.Body.String()).To(ContainSubstring("[hidden]"))
		list := s.do("alice", http.MethodGet, "/api/agent/tasks", nil)
		var tasks []map[string]any
		Expect(jsonInto(list, &tasks)).To(Succeed())
		Expect(tasks).To(HaveLen(1))

		upd := map[string]any{"name": "Summary 2", "model": "test-model", "prompt": "Short {{.topic}}",
			"webhooks": []map[string]any{{"url": "https://hooks.example.test/done", "headers": map[string]string{"Authorization": "[hidden]"}}}}
		Expect(s.do("alice", http.MethodPut, "/api/agent/tasks/"+id, upd).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/agent/tasks/"+id, nil))).To(HaveKeyWithValue("name", "Summary 2"))

		Expect(s.do("alice", http.MethodDelete, "/api/agent/tasks/"+id, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/agent/tasks/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses a bad task with the reason", func() {
		for want, body := range map[string]map[string]any{
			"name is required":       {"model": "test-model", "prompt": "p"},
			"prompt is required":     {"name": "x", "model": "test-model"},
			"model is required":      {"name": "x", "prompt": "p"},
			"not a valid template":   {"name": "x", "model": "test-model", "prompt": "{{.a"},
			"unknown model":          {"name": "x", "model": "nope", "prompt": "p"},
			"schedule a saved agent": {"name": "x", "model": "test-model", "prompt": "p", "cron": "* * * * *"},
			"private":                {"name": "x", "model": "test-model", "prompt": "p", "webhooks": []map[string]any{{"url": "http://127.0.0.1:1/h"}}},
		} {
			rec := s.do("alice", http.MethodPost, "/api/agent/tasks", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), want)
			Expect(rec.Body.String()).To(ContainSubstring(want))
			Expect(strings.Count(rec.Body.String(), `"error"`)).To(Equal(1), "one answer, not two: "+rec.Body.String())
		}
		var none []map[string]any
		Expect(jsonInto(s.do("alice", http.MethodGet, "/api/agent/tasks", nil), &none)).To(Succeed())
		Expect(none).To(BeEmpty(), "a refused task must not be created")
		id := createTask("alice", task)
		Expect(s.do("alice", http.MethodPost, "/api/agent/tasks", task).Code).To(Equal(http.StatusConflict), "the same name twice")
		Expect(id).NotTo(BeEmpty())
	})

	It("runs a job in the background and shows its status, result and webhook", func() {
		id := createTask("alice", task)
		rec := s.do("alice", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": id, "parameters": map[string]string{"topic": "the week"}})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		resp := decodeJSON(rec)
		jobID := resp["job_id"].(string)
		Expect(resp).To(HaveKeyWithValue("status", "pending"))
		Expect(resp["url"]).To(HaveSuffix("/api/agent/jobs/" + jobID))

		s.harness.Gate <- struct{}{} // let the model's one request through
		job := waitJob("alice", jobID)
		Expect(job).To(And(HaveKeyWithValue("task_id", id), HaveKeyWithValue("triggered_by", "api")))
		Expect(job["result"]).To(ContainSubstring("Summarise the week"))
		Expect(job["parameters"]).To(HaveKeyWithValue("topic", "the week"))
		Expect(s.harness.Sent()).To(ContainElement("Summarise the week"))

		Eventually(func() int { return len(hooks.all()) }, "10s", "20ms").Should(Equal(1))
		call := hooks.all()[0]
		Expect(call.Headers).To(HaveKeyWithValue("Authorization", "Bearer top-secret"))
		Expect(string(call.Body)).To(And(ContainSubstring(`"status":"completed"`), ContainSubstring(`"job_id":"`+jobID+`"`)))
		Eventually(func() any {
			return decodeJSON(s.do("alice", http.MethodGet, "/api/agent/jobs/"+jobID, nil))["webhook_sent"]
		}, "10s", "20ms").Should(BeTrue())

		var jobs []map[string]any
		Expect(jsonInto(s.do("alice", http.MethodGet, "/api/agent/jobs?task_id="+id+"&status=completed&limit=5", nil), &jobs)).To(Succeed())
		Expect(jobs).To(HaveLen(1))

		// The job is an ordinary chat of its owner, marked with its origin.
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+jobID, nil))).To(And(HaveKeyWithValue("origin", "job"), HaveKeyWithValue("origin_id", id)))
	})

	It("runs a task by name with a JSON object of parameters and runs a job again", func() {
		createTask("alice", task)
		rec := s.do("alice", http.MethodPost, "/api/agent/tasks/summary/execute", map[string]any{"topic": "Monday", "n": 3})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		first := decodeJSON(rec)["job_id"].(string)
		s.harness.Gate <- struct{}{}
		waitJob("alice", first)

		again := s.do("alice", http.MethodPost, "/api/agent/jobs/"+first+"/rerun", nil)
		Expect(again.Code).To(Equal(http.StatusCreated), again.Body.String())
		second := decodeJSON(again)["job_id"].(string)
		Expect(second).NotTo(Equal(first))
		s.harness.Gate <- struct{}{}
		Expect(waitJob("alice", second)).To(HaveKeyWithValue("triggered_by", "rerun"))
		Expect(s.do("alice", http.MethodPost, "/api/agent/tasks/nope/execute", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses a missing parameter and an attachment that is an address", func() {
		id := createTask("alice", task)
		rec := s.do("alice", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": id})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("parameter"))
		rec = s.do("alice", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": id, "parameters": map[string]string{"topic": "x"}, "images": []string{"https://example.test/a.png"}})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("not fetched"))
		Expect(s.do("alice", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": "nope"}).Code).To(Equal(http.StatusNotFound))
	})

	It("cancels a running job and deletes a job with its chat", func() {
		id := createTask("alice", map[string]any{"name": "Slow", "model": "test-model", "prompt": "Wait"})
		jobID := decodeJSON(s.do("alice", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": id}))["job_id"].(string)
		Eventually(func() any {
			return decodeJSON(s.do("alice", http.MethodGet, "/api/agent/jobs/"+jobID, nil))["status"]
		}, "10s", "20ms").Should(Equal("running"))
		Expect(s.do("alice", http.MethodPost, "/api/agent/jobs/"+jobID+"/cancel", nil).Code).To(Equal(http.StatusOK))
		Eventually(func() any {
			return decodeJSON(s.do("alice", http.MethodGet, "/api/agent/jobs/"+jobID, nil))["status"]
		}, "10s", "20ms").Should(Equal("cancelled"))
		Expect(s.do("alice", http.MethodPost, "/api/agent/jobs/"+jobID+"/cancel", nil).Code).To(Equal(http.StatusConflict), "an ended job cannot be cancelled")

		Expect(s.do("alice", http.MethodDelete, "/api/agent/jobs/"+jobID, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/agent/jobs/"+jobID, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+jobID, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("keeps tasks and jobs private to their owner, with a read-only view for an administrator", func() {
		id := createTask("alice", task)
		jobID := decodeJSON(s.do("alice", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": id, "parameters": map[string]string{"topic": "x"}}))["job_id"].(string)
		s.harness.Gate <- struct{}{}
		waitJob("alice", jobID)

		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/agent/tasks/" + id},
			{http.MethodPut, "/api/agent/tasks/" + id},
			{http.MethodDelete, "/api/agent/tasks/" + id},
			{http.MethodGet, "/api/agent/jobs/" + jobID},
			{http.MethodPost, "/api/agent/jobs/" + jobID + "/cancel"},
			{http.MethodPost, "/api/agent/jobs/" + jobID + "/rerun"},
			{http.MethodDelete, "/api/agent/jobs/" + jobID},
		} {
			Expect(s.do("bob", c.method, c.path, task).Code).To(Equal(http.StatusNotFound), "bob: "+c.method+" "+c.path)
		}
		Expect(s.do("bob", http.MethodPost, "/api/agent/jobs/execute", map[string]any{"task_id": id}).Code).To(Equal(http.StatusNotFound))
		var bobs []map[string]any
		Expect(jsonInto(s.do("bob", http.MethodGet, "/api/agent/jobs?all_users=true", nil), &bobs)).To(Succeed(), "all_users is for administrators")
		Expect(bobs).To(BeEmpty())

		// The administrator reads any user's job, lists them with all_users, and changes nothing.
		Expect(decodeJSON(s.do("root", http.MethodGet, "/api/agent/jobs/"+jobID, nil))).To(HaveKeyWithValue("id", jobID))
		all := decodeJSON(s.do("root", http.MethodGet, "/api/agent/jobs?all_users=true", nil))
		Expect(all).To(HaveKey("user_groups"))
		Expect(all["user_groups"]).To(HaveKey("alice"))
		allTasks := decodeJSON(s.do("root", http.MethodGet, "/api/agent/tasks?all_users=true", nil))
		Expect(allTasks["user_groups"]).To(HaveKey("alice"))
		Expect(strings.Contains(s.do("root", http.MethodGet, "/api/agent/tasks?all_users=true", nil).Body.String(), "top-secret")).To(BeFalse())
		Expect(s.do("root", http.MethodDelete, "/api/agent/jobs/"+jobID, nil).Code).To(Equal(http.StatusNotFound), "the administrator's view is read-only")
	})
})
