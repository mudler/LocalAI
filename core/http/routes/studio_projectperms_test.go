// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The three lists of a project show objects of another feature, so each route
// needs that feature as well as the chats feature of the project page.
var _ = Describe("Chats project lists and their permissions", func() {
	var s *studioServer
	BeforeEach(func() {
		f := newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
	})

	lists := []struct{ kind, feature string }{
		{"agents", auth.FeatureAgents},
		{"connections", auth.FeatureConnectors},
		{"triggers", auth.FeatureTriggers},
	}
	// project makes a project of the user, so a list that passes the
	// permission answers 200 and not 404.
	project := func(user string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/projects", map[string]any{"name": "Kitchen " + user})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	list := func(user, pid, kind string) (int, string) {
		rec := s.do(user, http.MethodGet, "/api/chats/projects/"+pid+"/"+kind, nil)
		return rec.Code, rec.Body.String()
	}

	It("lists for a person who holds the feature of the objects and the chats feature", func() {
		pid := project("alice")
		for _, l := range lists {
			code, body := list("alice", pid, l.kind)
			Expect(code).To(Equal(http.StatusOK), l.kind+": "+body)
		}
		code, _ := list("alice", pid, "loops")
		Expect(code).To(Equal(http.StatusOK), "loops are chat objects")
	})

	It("refuses a person whose account lacks the feature of the objects, and names it", func() {
		// dave may chat but was never granted agents, triggers or connectors.
		pid := project("dave")
		for _, l := range lists {
			code, body := list("dave", pid, l.kind)
			Expect(code).To(Equal(http.StatusForbidden), l.kind)
			Expect(body).To(ContainSubstring(l.feature), l.kind)
		}
		// The project itself, its chats, memory and loops stay open to him.
		for _, tail := range []string{"", "/chats", "/loops", "/memory"} {
			Expect(s.do("dave", http.MethodGet, "/api/chats/projects/"+pid+tail, nil).Code).To(Equal(http.StatusOK), tail)
		}
	})

	DescribeTable("refuses a person who turned off one permission, and only that list",
		func(off string) {
			pid := project("bob")
			Expect(auth.UpdateUserPermissions(s.db, "bob", auth.PermissionMap{
				auth.FeatureAgents: true, auth.FeatureTriggers: true, auth.FeatureConnectors: true, off: false})).To(Succeed())
			for _, l := range lists {
				code, body := list("bob", pid, l.kind)
				if l.feature == off {
					Expect(code).To(Equal(http.StatusForbidden), l.kind)
				} else {
					Expect(code).To(Equal(http.StatusOK), l.kind+": "+body)
				}
			}
		},
		Entry("agents", auth.FeatureAgents),
		Entry("connectors", auth.FeatureConnectors),
		Entry("triggers", auth.FeatureTriggers),
	)

	It("refuses a person whose chats permission is off, even with the three features", func() {
		pid := project("alice")
		// carol had the chats feature revoked; give her the other three.
		Expect(auth.UpdateUserPermissions(s.db, "carol", auth.PermissionMap{auth.FeatureChats: false,
			auth.FeatureAgents: true, auth.FeatureTriggers: true, auth.FeatureConnectors: true})).To(Succeed())
		for _, l := range lists {
			code, _ := list("carol", pid, l.kind)
			Expect(code).To(Equal(http.StatusForbidden), l.kind)
		}
	})

	It("lets an administrator through", func() {
		pid := project("root")
		for _, l := range lists {
			code, body := list("root", pid, l.kind)
			Expect(code).To(Equal(http.StatusOK), l.kind+": "+body)
		}
	})
})
