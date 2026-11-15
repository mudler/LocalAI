// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs go through the real route table: "shared" must reach the
// shared-project handlers, not "/projects/:id".
var _ = Describe("Chats shared projects routes", func() {
	var (
		s *studioServer
		f *files.Service
	)
	ctx := context.Background()
	BeforeEach(func() {
		f = newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
	})

	DescribeTable("require authentication",
		func(path string) {
			Expect(s.do("", http.MethodGet, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", "/api/chats/projects/shared"),
		Entry("one", "/api/chats/projects/shared/s:1"),
		Entry("memory", "/api/chats/projects/shared/s:1/memory"),
	)

	It("lists, reads and reads the memory of a shared project, with the colon plain or encoded", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/projects", map[string]any{"name": "Acme"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		pid := decodeJSON(rec)["id"].(string)
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/shared", nil).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/projects/shared", nil))["projects"]).To(BeEmpty())

		abs, err := f.Abs("alice", "Projects/acme")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Store().Grant(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "bob", abs, workspace.ModeRead)).To(Succeed())

		list := decodeJSON(s.do("bob", http.MethodGet, "/api/chats/projects/shared", nil))["projects"].([]any)
		Expect(list).To(HaveLen(1))
		ref := list[0].(map[string]any)["ref"].(string)
		Expect(ref).To(HavePrefix("s:"))
		Expect(list[0]).To(HaveKeyWithValue("mode", "read"))
		for _, r := range []string{ref, strings.Replace(ref, ":", "%3A", 1)} {
			one := s.do("bob", http.MethodGet, "/api/chats/projects/shared/"+r, nil)
			Expect(one.Code).To(Equal(http.StatusOK), r+" "+one.Body.String())
			Expect(decodeJSON(one)).To(HaveKeyWithValue("name", "Acme"))
			Expect(s.do("bob", http.MethodGet, "/api/chats/projects/shared/"+r+"/memory", nil).Code).To(Equal(http.StatusOK), r)
		}
		// The manifest id of the owner finds nothing among the shared projects.
		Expect(s.do("bob", http.MethodGet, "/api/chats/projects/shared/"+pid, nil).Code).To(Equal(http.StatusNotFound))
		// "shared" is no project id of the person's own list.
		Expect(s.do("bob", http.MethodGet, "/api/chats/projects/"+pid, nil).Code).To(Equal(http.StatusNotFound))
	})
})
