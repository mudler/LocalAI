package localaitools_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
)

// projectsStandIn answers the project routes of the studio API and records
// each request as "METHOD /path" with its JSON body.
type projectsStandIn struct {
	*httptest.Server
	mu     sync.Mutex
	calls  []string
	bodies []map[string]any
	// pulled is what GET /api/chats/c1 reports as the chat's pulled projects.
	pulled []string
	// status makes every route answer with it when not zero.
	status int
	detail string
	// shared is the status of the shared project; empty means active.
	shared string
}

func (s *projectsStandIn) sharedStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shared == "" {
		return "active"
	}
	return s.shared
}

func newProjectsStandIn() *projectsStandIn {
	s := &projectsStandIn{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		s.bodies = append(s.bodies, body)
		status, detail, pulled := s.status, s.detail, append([]string(nil), s.pulled...)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": detail}})
			return
		}
		proj := map[string]any{"id": "p1", "name": "Acme", "slug": "acme", "description": "d", "status": "active", "chat_count": 2,
			"defaults": map[string]any{"skills": []string{"secret-detail"}},
			"attached": map[string]any{"skills": []string{"s1"}, "connectors": []string{"mail"}}}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/p1/agents":
			_ = json.NewEncoder(w).Encode(map[string]any{"agents": []any{
				map[string]any{"id": "a1", "name": "Planner", "description": "plans", "model": "m", "unavailable": true, "files_mode": "read", "memory_write": true},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/p1/connections":
			_ = json.NewEncoder(w).Encode(map[string]any{"connections": []any{
				map[string]any{"id": "b1", "kind": "telegram", "name": "Bot", "agent_id": "a1", "agent_name": "Planner", "enabled": true},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/p1/triggers":
			_ = json.NewEncoder(w).Encode(map[string]any{"triggers": []any{
				map[string]any{"id": "t1", "name": "Daily", "kind": "schedule", "agent_id": "a1", "agent_name": "Planner", "schedule": "daily 09:00",
					"enabled": true, "proposed": false, "next": "2026-11-16T09:00:00Z"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/p1/loops":
			_ = json.NewEncoder(w).Encode(map[string]any{"loops": []any{
				map[string]any{"id": "l1", "chat_id": "c1", "chat_title": "Chat", "prompt": "check", "state": "active"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/p1/memory":
			_ = json.NewEncoder(w).Encode(map[string]any{"index": "# Acme", "files": []any{
				map[string]any{"name": "food.md", "size": 12, "modified": "2026-11-01T10:00:00Z"},
			}})
		case r.Method == http.MethodPut && r.URL.Path == "/api/chats/projects/p1/memory/food.md":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/p1/chats":
			_ = json.NewEncoder(w).Encode(map[string]any{"conversations": []any{
				map[string]any{"id": "c1", "title": "Plan", "model": "m", "agent_id": "a1", "updated": "2026-11-02T10:00:00Z", "pulled_projects": []string{"x"}},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/shared/s:12":
			_ = json.NewEncoder(w).Encode(map[string]any{"ref": "s:12", "name": "Acme", "description": "d", "status": s.sharedStatus(), "mode": "read", "owner_name": "Alice Rossi"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/shared/s:12/memory":
			_ = json.NewEncoder(w).Encode(map[string]any{"index": "# Shared", "files": []string{"rules.md"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects/shared":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []any{
				map[string]any{"ref": "s:12", "name": "Acme", "description": "d", "status": "active", "mode": "read", "owner_name": "Alice Rossi", "error": ""},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/chats/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []any{proj}})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/chats/c1" || r.URL.Path == "/api/chats/c1/project" || r.URL.Path == "/api/chats/c1/projects":
			chat := map[string]any{"id": "c1", "project_id": "p1", "pulled_projects": pulled}
			if r.URL.Path == "/api/chats/c1/projects" {
				chat["pulled_projects"] = body["project_ids"]
			}
			_ = json.NewEncoder(w).Encode(chat)
		default:
			if r.Method == http.MethodPost && r.URL.Path == "/api/chats/projects" {
				w.WriteHeader(http.StatusCreated)
			}
			_ = json.NewEncoder(w).Encode(proj)
		}
	}))
	return s
}

func ptr(s string) *string { return &s }

func (s *projectsStandIn) last() (string, map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1], s.bodies[len(s.bodies)-1]
}

var _ = Describe("the projects tool over the REST client", func() {
	var (
		srv *projectsStandIn
		cl  *httpapi.Client
		ctx context.Context
	)
	BeforeEach(func() {
		srv = newProjectsStandIn()
		DeferCleanup(srv.Close)
		cl = httpapi.New(srv.URL, "secret")
		ctx = context.Background()
	})

	DescribeTable("maps each action to its route",
		func(req localaitools.ProjectsRequest, call string, body map[string]any) {
			_, err := cl.Projects(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			got, gotBody := srv.last()
			Expect(got).To(Equal(call))
			if body == nil {
				Expect(gotBody).To(BeEmpty())
			} else {
				Expect(gotBody).To(Equal(body))
			}
		},
		Entry("list", localaitools.ProjectsRequest{Action: "list"}, "GET /api/chats/projects", nil),
		Entry("get", localaitools.ProjectsRequest{Action: "get", Project: "p1"}, "GET /api/chats/projects/p1", nil),
		Entry("agents", localaitools.ProjectsRequest{Action: "agents", Project: "p1"}, "GET /api/chats/projects/p1/agents", nil),
		Entry("connections", localaitools.ProjectsRequest{Action: "connections", Project: "p1"}, "GET /api/chats/projects/p1/connections", nil),
		Entry("triggers", localaitools.ProjectsRequest{Action: "triggers", Project: "p1"}, "GET /api/chats/projects/p1/triggers", nil),
		Entry("loops", localaitools.ProjectsRequest{Action: "loops", Project: "p1"}, "GET /api/chats/projects/p1/loops", nil),
		Entry("shared", localaitools.ProjectsRequest{Action: "shared"}, "GET /api/chats/projects/shared", nil),
		Entry("create", localaitools.ProjectsRequest{Action: "create", Name: "Acme", Description: ptr("d")}, "POST /api/chats/projects",
			map[string]any{"name": "Acme", "description": "d"}),
		Entry("update", localaitools.ProjectsRequest{Action: "update", Project: "p1", Name: "New"}, "PATCH /api/chats/projects/p1",
			map[string]any{"name": "New"}),
		Entry("archive", localaitools.ProjectsRequest{Action: "archive", Project: "p1"}, "POST /api/chats/projects/p1/archive", nil),
		Entry("restore", localaitools.ProjectsRequest{Action: "restore", Project: "p1"}, "POST /api/chats/projects/p1/restore", nil),
		Entry("delete", localaitools.ProjectsRequest{Action: "delete", Project: "p1"}, "DELETE /api/chats/projects/p1", nil),
		Entry("set_chat_project", localaitools.ProjectsRequest{Action: "set_chat_project", ChatID: "c1", Project: "p1"}, "PUT /api/chats/c1/project",
			map[string]any{"project_id": "p1"}),
		Entry("set_chat_project with no project", localaitools.ProjectsRequest{Action: "set_chat_project", ChatID: "c1"}, "PUT /api/chats/c1/project",
			map[string]any{"project_id": ""}),
	)

	It("clears a description with an explicit empty string, and leaves it alone when absent", func() {
		_, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "update", Project: "p1", Description: ptr("")})
		Expect(err).NotTo(HaveOccurred())
		_, body := srv.last()
		Expect(body).To(Equal(map[string]any{"description": ""}))
		_, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "update", Project: "p1", Name: "N"})
		Expect(err).NotTo(HaveOccurred())
		_, body = srv.last()
		Expect(body).To(Equal(map[string]any{"name": "N"}))
	})

	It("pulls a project by reading the chat's list and writing it back with the project added", func() {
		srv.pulled = []string{"p0"}
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "pull", ChatID: "c1", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		call, body := srv.last()
		Expect(call).To(Equal("PUT /api/chats/c1/projects"))
		Expect(body["project_ids"]).To(ConsistOf("p0", "p1"))
		Expect(res.Chat.PulledProjects).To(ConsistOf("p0", "p1"))
	})

	It("keeps a pull idempotent and unpulls by writing the list without the project", func() {
		srv.pulled = []string{"p1"}
		_, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "pull", ChatID: "c1", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		_, body := srv.last()
		Expect(body["project_ids"]).To(ConsistOf("p1"))

		_, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "unpull", ChatID: "c1", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		call, body := srv.last()
		Expect(call).To(Equal("PUT /api/chats/c1/projects"))
		Expect(body["project_ids"]).To(BeEmpty())
	})

	It("lists a short summary and never the defaults", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "list"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Projects).To(HaveLen(1))
		Expect(res.Projects[0]).To(Equal(localaitools.ProjectInfo{ID: "p1", Name: "Acme", Slug: "acme", Description: "d", Status: "active", ChatCount: 2,
			Attached: localaitools.ProjectAttached{Skills: []string{"s1"}, Connectors: []string{"mail"}}}))
		raw, _ := json.Marshal(res)
		Expect(string(raw)).NotTo(ContainSubstring("secret-detail"))
	})

	It("shows what a project attaches on get", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "get", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Project.Attached).To(Equal(localaitools.ProjectAttached{Skills: []string{"s1"}, Connectors: []string{"mail"}}))
	})

	It("rejects an attached list it does not know", func() {
		_, err := localaitools.ProjectAttachedFromMap(map[string][]string{"skils": {"x"}})
		Expect(err).To(MatchError(ContainSubstring(`"skils"`)))
		a, err := localaitools.ProjectAttachedFromMap(map[string][]string{"mcp_servers": {"srv"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.MCPServers).To(Equal([]string{"srv"}))
		Expect(a.HoldsCredentials()).To(BeTrue())
	})

	It("sends attached as the whole block on update and create", func() {
		att := &localaitools.ProjectAttached{Skills: []string{"s2"}, MCPServers: []string{"srv"}}
		_, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "update", Project: "p1", Attached: att})
		Expect(err).NotTo(HaveOccurred())
		call, body := srv.last()
		Expect(call).To(Equal("PATCH /api/chats/projects/p1"))
		Expect(body["attached"]).To(Equal(map[string]any{"skills": []any{"s2"}, "mcp_servers": []any{"srv"}}))
		_, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "create", Name: "N", Attached: att})
		Expect(err).NotTo(HaveOccurred())
		call, body = srv.last()
		Expect(call).To(Equal("POST /api/chats/projects"))
		Expect(body).To(HaveKey("attached"))
		// Without attached the body has no such key: update leaves it alone.
		_, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "update", Project: "p1", Name: "x"})
		Expect(err).NotTo(HaveOccurred())
		_, body = srv.last()
		Expect(body).NotTo(HaveKey("attached"))
	})

	It("lists the projects shared with the person as brief rows with the reference, mode, status and owner name", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "shared"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Shared).To(Equal([]localaitools.SharedProjectInfo{{Ref: "s:12", Name: "Acme", Mode: "read", Status: "active", OwnerName: "Alice Rossi"}}))
		Expect(res.Projects).To(BeEmpty())
	})

	It("reads the memory of a project as the index and the names of the topic files", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "memory", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Memory).To(Equal(&localaitools.ProjectMemoryInfo{Index: "# Acme", Files: []string{"food.md"}}))
	})

	It("writes one memory file as the content given, and leaves an empty content to the caller", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "write_memory", Project: "p1", File: "food.md", Content: ptr("# Food")})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Written).To(Equal("food.md"))
		call, body := srv.last()
		Expect(call).To(Equal("PUT /api/chats/projects/p1/memory/food.md"))
		Expect(body).To(Equal(map[string]any{"content": "# Food"}))
		_, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "write_memory", Project: "p1", File: "food.md", Content: ptr("")})
		Expect(err).NotTo(HaveOccurred())
		_, body = srv.last()
		Expect(body).To(Equal(map[string]any{"content": ""}))
	})

	It("lists the chats of a project as brief rows, with no message and no model", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "chats", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Chats).To(Equal([]localaitools.ProjectChatInfo{{ID: "c1", Title: "Plan", AgentID: "a1", Updated: "2026-11-02T10:00:00Z"}}))
	})

	It("reads a shared project with its memory, and without one when the project is not active", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "shared_get", Project: "s:12"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.SharedOne).To(Equal(&localaitools.SharedProjectInfo{Ref: "s:12", Name: "Acme", Mode: "read", Status: "active", OwnerName: "Alice Rossi"}))
		Expect(res.Memory).To(Equal(&localaitools.ProjectMemoryInfo{Index: "# Shared", Files: []string{"rules.md"}}))
		srv.shared = "archived"
		res, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "shared_get", Project: "s:12"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.SharedOne.Status).To(Equal("archived"))
		Expect(res.Memory).To(BeNil())
	})

	It("lists the agents of a project as brief rows", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "agents", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Agents).To(Equal([]localaitools.ProjectAgentInfo{{ID: "a1", Name: "Planner", Description: "plans", Model: "m",
			Unavailable: true, FilesMode: "read", MemoryWrite: true}}))
	})

	It("lists the connections, triggers and loops of a project as brief rows", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "connections", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Connections).To(Equal([]localaitools.ProjectConnectionInfo{{ID: "b1", Kind: "telegram", Name: "Bot", AgentID: "a1", AgentName: "Planner", Enabled: true}}))
		res, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "triggers", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Triggers).To(HaveLen(1))
		Expect(res.Triggers[0]).To(And(HaveField("ID", "t1"), HaveField("Schedule", "daily 09:00"), HaveField("Next", "2026-11-16T09:00:00Z")))
		res, err = cl.Projects(ctx, localaitools.ProjectsRequest{Action: "loops", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Loops).To(Equal([]localaitools.ProjectLoopInfo{{ID: "l1", ChatID: "c1", ChatTitle: "Chat", Prompt: "check", State: "active"}}))
	})

	It("reports a delete as done", func() {
		res, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "delete", Project: "p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Deleted).To(BeTrue())
	})

	It("words the refusals of the API for the model, naming the tool", func() {
		for _, c := range []struct {
			status int
			want   string
		}{
			{http.StatusNotFound, "no such project or chat"},
			{http.StatusConflict, "running"},
			{http.StatusBadRequest, "archived"},
			{http.StatusForbidden, "may not use"},
		} {
			srv.status, srv.detail = c.status, "the chat is running or the project is archived"
			_, err := cl.Projects(ctx, localaitools.ProjectsRequest{Action: "get", Project: "p1"})
			Expect(err).To(MatchError(ContainSubstring("projects: ")), c.want)
			Expect(err).To(MatchError(ContainSubstring(c.want)))
		}
	})
})
