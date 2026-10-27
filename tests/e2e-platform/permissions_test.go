package e2eplatform_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// routeSpec is a route read from the registration code of the repository, so a
// route added later is checked without a change to this file.
type routeSpec struct{ Method, Pattern string }

var routeLine = regexp.MustCompile(`(?:e|cg)\.(GET|POST|PUT|DELETE|PATCH)\((?:base\s*\+\s*)?"([^"]*)"`)

func registeredRoutes(file, prefix string) []routeSpec {
	raw, err := os.ReadFile(filepath.Join(repoRoot, file))
	Expect(err).ToNot(HaveOccurred())
	var out []routeSpec
	for _, m := range routeLine.FindAllStringSubmatch(string(raw), -1) {
		out = append(out, routeSpec{Method: m[1], Pattern: prefix + m[2]})
	}
	Expect(out).ToNot(BeEmpty(), file)
	return out
}

// concrete turns an Echo pattern into a path a request can use.
func concrete(pattern string) string {
	p := regexp.MustCompile(`:[A-Za-z_]+`).ReplaceAllString(pattern, "x1")
	return strings.ReplaceAll(p, "/*", "/x1")
}

func allNewRoutes() []routeSpec {
	r := registeredRoutes("core/http/routes/studio.go", "/api/chats")
	return append(r, registeredRoutes("core/http/routes/collections.go", "/api/collections")...)
}

var _ = Describe("Permissions and the registry of the new routes", Label("platform", "auth"), Ordered, func() {
	var (
		in    *instance
		admin *api
		user  *api
		uid   string
		anon  *api
	)

	BeforeAll(func() {
		in = startInstance(startOpts{Name: "permissions", Auth: true, Models: map[string]string{"mock-embed.yaml": mockEmbedModel},
			Args: []string{"--collections-embedding-model", "mock-embed", "--api-keys", "e2e-static-key"}})
		admin, _ = signUp(in, "admin@e2e.test")
		user, uid = signUp(in, "user@e2e.test")
		anon = newAPI(in.api.base)
	})

	statusOf := func(a *api, r routeSpec) int {
		var body any
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			body = obj{}
		}
		code, _ := a.do(r.Method, concrete(r.Pattern), body)
		return code
	}

	It("answers 401 to every new route without a credential", func() {
		var bad []string
		for _, r := range allNewRoutes() {
			if code := statusOf(anon, r); code != http.StatusUnauthorized {
				bad = append(bad, fmt.Sprintf("%s %s -> %d", r.Method, r.Pattern, code))
			}
		}
		Expect(bad).To(BeEmpty())
	})

	It("answers 403 to every chats route for a user whose chats feature is off, and 401 stays 401", func() {
		admin.mustJSON(http.MethodPut, "/api/auth/admin/users/"+uid+"/permissions", obj{"chats": false}, http.StatusOK)
		DeferCleanup(func() {
			admin.mustJSON(http.MethodPut, "/api/auth/admin/users/"+uid+"/permissions", obj{"chats": true, "mcp": true}, http.StatusOK)
		})
		var bad []string
		for _, r := range registeredRoutes("core/http/routes/studio.go", "/api/chats") {
			if code := statusOf(user, r); code != http.StatusForbidden {
				bad = append(bad, fmt.Sprintf("%s %s -> %d", r.Method, r.Pattern, code))
			}
		}
		Expect(bad).To(BeEmpty(), "a chats route is open to a user without the feature")
	})

	It("answers 403 to the MCP connector and MCP app routes for a user whose mcp feature is off", func() {
		admin.mustJSON(http.MethodPut, "/api/auth/admin/users/"+uid+"/permissions", obj{"chats": true, "mcp": false}, http.StatusOK)
		DeferCleanup(func() {
			admin.mustJSON(http.MethodPut, "/api/auth/admin/users/"+uid+"/permissions", obj{"chats": true, "mcp": true}, http.StatusOK)
		})
		chat := user.newChat(nil)
		var bad []string
		for _, r := range registeredRoutes("core/http/routes/studio.go", "/api/chats") {
			if !strings.Contains(r.Pattern, "mcp-connectors") && !strings.Contains(r.Pattern, "mcp-apps") {
				continue
			}
			if r.Method == http.MethodDelete {
				continue // removing your own connector stays possible, so a user can clean up
			}
			path := strings.Replace(concrete(r.Pattern), "/x1/", "/"+chat+"/", 1)
			var body any
			switch {
			case strings.HasSuffix(path, "/mcp-apps/resource"):
				path += "?server=connector%3Ax1&uri=ui%3A%2F%2Fa%2Fb.html"
			case strings.HasSuffix(path, "/mcp-apps/call"):
				body = obj{"server": "connector:x1", "tool": "t"}
			case r.Method != http.MethodGet:
				body = obj{}
			}
			if code, _ := user.do(r.Method, path, body); code != http.StatusForbidden {
				bad = append(bad, fmt.Sprintf("%s %s -> %d", r.Method, r.Pattern, code))
			}
		}
		Expect(bad).To(BeEmpty(), "an MCP route is open to a user without the mcp feature")
	})

	It("keeps the admin-only parts of the chats routes for an administrator", func() {
		// A shared skill is written by an administrator only.
		code, out := user.do(http.MethodPost, "/api/chats/skills", obj{"layer": "shared", "name": "team-skill", "description": "d", "body": "b"})
		Expect(code).To(Equal(http.StatusForbidden), string(out))
		code, out = admin.do(http.MethodPost, "/api/chats/skills", obj{"layer": "shared", "name": "team-skill", "description": "d", "body": "b"})
		Expect(code).To(BeNumerically("<", 300), string(out))
		// A local command connector is for an administrator only.
		code, out = user.do(http.MethodPost, "/api/chats/mcp-connectors", obj{"name": "c", "transport": "stdio", "command": "ls", "enabled": false})
		Expect(code).To(Equal(http.StatusForbidden), string(out))
		// The runtime setting that allows private addresses is an administrator's.
		code, _ = user.do(http.MethodPost, "/api/settings", obj{"mcp_connectors_allow_private": true})
		Expect(code).To(Equal(http.StatusForbidden))
		// The conversion totals are shown to an administrator only.
		_, out = user.do(http.MethodGet, "/api/chats/conversion", nil)
		Expect(decode(out)).NotTo(HaveKey("totals"))
	})

	It("keeps the collections routes behind their own feature for an ordinary user", func() {
		var bad []string
		for _, r := range registeredRoutes("core/http/routes/collections.go", "/api/collections") {
			if code := statusOf(user, r); code != http.StatusForbidden {
				bad = append(bad, fmt.Sprintf("%s %s -> %d", r.Method, r.Pattern, code))
			}
		}
		Expect(bad).To(BeEmpty())
	})

	It("lets an administrator reach every new route (none answers 401 or 403)", func() {
		chat := admin.newChat(nil)
		var bad []string
		for _, r := range allNewRoutes() {
			if strings.HasSuffix(r.Pattern, "/events") {
				continue // server-sent streams stay open; the 401 and 403 cases above cover them
			}
			r.Pattern = strings.Replace(r.Pattern, ":id/", chat+"/", 1)
			if code := statusOf(admin, r); code == http.StatusUnauthorized || code == http.StatusForbidden {
				bad = append(bad, fmt.Sprintf("%s %s -> %d", r.Method, r.Pattern, code))
			}
		}
		Expect(bad).To(BeEmpty())
	})

	Describe("swagger and /api/instructions", func() {
		It("documents every chats route in the swagger file served by the binary", func() {
			code, out := anon.do(http.MethodGet, "/swagger/doc.json", nil)
			Expect(code).To(Equal(http.StatusOK), string(out))
			var doc struct {
				Paths map[string]map[string]any `json:"paths"`
			}
			Expect(json.Unmarshal(out, &doc)).To(Succeed())
			norm := func(p string) string { return regexp.MustCompile(`\{[^}]+\}|:[A-Za-z_]+|\*`).ReplaceAllString(p, "X") }
			documented := map[string]map[string]bool{}
			for p, methods := range doc.Paths {
				for m := range methods {
					if documented[norm(p)] == nil {
						documented[norm(p)] = map[string]bool{}
					}
					documented[norm(p)][strings.ToUpper(m)] = true
				}
			}
			var missing []string
			for _, r := range registeredRoutes("core/http/routes/studio.go", "/api/chats") {
				if !documented[norm(r.Pattern)][r.Method] {
					missing = append(missing, r.Method+" "+r.Pattern)
				}
			}
			sort.Strings(missing)
			Expect(missing).To(BeEmpty(), "chats routes missing from swagger")
		})

		It("documents the collections routes in swagger", func() {
			_, out := anon.do(http.MethodGet, "/swagger/doc.json", nil)
			Expect(string(out)).To(ContainSubstring(`"/api/collections"`))
		})

		It("lists the chats area in /api/instructions with every new route family", func() {
			list := anon.mustJSON(http.MethodGet, "/api/instructions", nil, http.StatusOK)
			names := []string{}
			for _, x := range list["instructions"].([]any) {
				names = append(names, x.(obj)["name"].(string))
			}
			Expect(names).To(ContainElement("chats"))
			code, out := anon.do(http.MethodGet, "/api/instructions/chats", nil)
			Expect(code).To(Equal(http.StatusOK), string(out))
			for _, frag := range []string{"/api/chats/mcp-connectors", "/mcp-apps/resource", "/mcp-apps/call", "/api/chats/connectors", "/api/chats/triggers", "/api/chats/{id}/tools"} {
				Expect(string(out)).To(ContainSubstring(frag), "/api/instructions/chats does not mention "+frag)
			}
		})
	})
})
