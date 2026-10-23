//go:build auth

package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/routes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The add-on routes, the generated API reference and the discovery guide that
// /api/instructions serves are one list. A route that is registered and not
// documented, or documented and not registered, fails here.
var _ = Describe("Add-on API discovery", func() {
	param := regexp.MustCompile(`[:{][A-Za-z0-9_]+}?`)
	// normal turns a route path (":id", "*") or a swagger path ("{id}") into
	// one comparable shape.
	normal := func(p string) string { return param.ReplaceAllString(strings.ReplaceAll(p, "/*", "/{}"), "{}") }

	registered := func() []string {
		e := echo.New()
		routes.RegisterAddonRoutesFor(e, nil, nil, "named-user")
		var out []string
		for _, r := range e.Routes() {
			switch r.Method {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			default:
				continue
			}
			if !strings.HasPrefix(r.Path, "/api/addons/") || strings.HasSuffix(r.Path, "/*") && r.Path == "/api/addons/*" {
				continue
			}
			out = append(out, r.Method+" "+normal(r.Path))
		}
		sort.Strings(out)
		return out
	}

	instruction := func(format string) (int, []byte) {
		e := echo.New()
		e.GET("/api/instructions/:name", localai.GetAPIInstructionEndpoint())
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/instructions/addons"+format, nil))
		return rec.Code, rec.Body.Bytes()
	}

	It("documents exactly the routes the host registers, in the generated reference and in the instruction guide", func() {
		want := registered()
		Expect(want).NotTo(BeEmpty())

		code, raw := instruction("?format=json")
		Expect(code).To(Equal(200), string(raw))
		var doc struct {
			SwaggerFragment struct {
				Paths map[string]map[string]any `json:"paths"`
			} `json:"swagger_fragment"`
		}
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		var documented []string
		for path, methods := range doc.SwaggerFragment.Paths {
			if !strings.HasPrefix(path, "/api/addons/") {
				continue
			}
			for method := range methods {
				documented = append(documented, strings.ToUpper(method)+" "+normal(path))
			}
		}
		sort.Strings(documented)
		Expect(documented).To(Equal(want), "the swagger reference and the registered /api/addons routes differ")

		code, guide := instruction("")
		Expect(code).To(Equal(200))
		text := string(guide)
		for _, route := range want {
			method, path, _ := strings.Cut(route, " ")
			// The guide prints the swagger path with its own parameter names.
			found := false
			for _, line := range strings.Split(text, "\n") {
				if rest, ok := strings.CutPrefix(line, "## "+method+" "); ok && normal(rest) == path {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "the instruction guide has no section for "+route)
		}
	})

	It("states in the guide what a caller needs to know about the route families", func() {
		_, guide := instruction("")
		text := string(guide)
		for _, phrase := range []string{
			"never submit a new mutation", "outcome_unknown", "own browser session", "No MCP tool maps",
			"never an update", "update/check", "recovery", "private artifact IDs",
		} {
			Expect(text).To(ContainSubstring(phrase), phrase)
		}
	})
})
