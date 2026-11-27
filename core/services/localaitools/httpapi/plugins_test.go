package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
)

// pluginsAPI fakes /api/plugins. codePlugin answers the install of
// hook@acme with 403 not_human_surface, as the server does for a key.
func pluginsAPI(seen *[]string) *httptest.Server {
	mux := http.NewServeMux()
	note := func(r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*seen = append(*seen, r.Method+" "+r.URL.RequestURI()+" "+string(body))
	}
	mux.HandleFunc("GET /api/plugins/marketplaces", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_, _ = w.Write([]byte(`{"marketplaces":[{"id":"acme","kind":"github","location":"acme/plugins","enabled":true,"active":true,"headSha":"abc","lastError":""}]}`))
	})
	mux.HandleFunc("POST /api/plugins/marketplaces", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_, _ = w.Write([]byte(`{"id":"team","kind":"github","location":"team/plugins","active":true}`))
	})
	mux.HandleFunc("POST /api/plugins/marketplaces/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","refreshedAt":"2026-11-27T10:00:00Z"}`))
	})
	mux.HandleFunc("GET /api/plugins/catalog", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_, _ = w.Write([]byte(`{"items":[{"name":"fmt","plugin":"fmt@acme","marketplace":"acme","supported":true,"runsCode":false,"installs":[{"id":"i1","scope":"user","state":"installed"}]}],"total":1}`))
	})
	mux.HandleFunc("GET /api/plugins/installs", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_, _ = w.Write([]byte(`{"installs":[{"id":"i1","plugin":"fmt@acme","scope":"user","sha":"abc","state":"update_available","updateSha":"def","enabled":true}]}`))
	})
	mux.HandleFunc("POST /api/plugins/inspect", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Plugin string `json:"plugin"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		*seen = append(*seen, "POST /api/plugins/inspect "+req.Plugin)
		code := req.Plugin == "hook@acme"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "tok-" + req.Plugin, "plugin": req.Plugin, "sha": "abc", "digest": "sha256:00",
			"runsCode": code, "codeReasons": map[bool][]string{true: {"hook-command"}, false: {}}[code],
			"model": map[string]any{"name": "x", "skills": []map[string]any{{"name": "a", "dir": "d", "path": "p"}}},
		})
	})
	mux.HandleFunc("POST /api/plugins/installs", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		if (*seen)[len(*seen)-1] == `POST /api/plugins/installs {"scope":"instance","token":"tok-hook@acme"}` {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"type":"not_human_surface","message":"This needs the administrator's own browser session."}}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"jobID":"job-9","plugin":"fmt@acme"}`))
	})
	mux.HandleFunc("POST /api/plugins/installs/{id}/update/preview", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_, _ = w.Write([]byte(`{"token":"t","install":"i1","plugin":"fmt@acme","fromSha":"abc","toSha":"def","added":[{"path":"skills/b/SKILL.md"}],"removed":[],"changed":[{"path":"hooks/hooks.json","code":true}],"oldCodeReasons":[],"newCodeReasons":["hook-command"],"needsCodeReview":true}`))
	})
	mux.HandleFunc("PUT /api/plugins/installs/{id}/me", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		if r.PathValue("id") == "gained" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":409,"type":"needs_reenable","message":"runs code"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"install":"i1","enabled":false,"needsReenable":false}`))
	})
	mux.HandleFunc("DELETE /api/plugins/installs/{id}", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		w.WriteHeader(http.StatusNoContent)
	})
	return httptest.NewServer(mux)
}

var _ = Describe("plugins over the REST API", func() {
	var (
		seen []string
		c    *Client
		ctx  = context.Background()
	)
	BeforeEach(func() {
		seen = nil
		srv := pluginsAPI(&seen)
		DeferCleanup(srv.Close)
		c = New(srv.URL, "key")
	})

	It("lists, adds and refreshes marketplaces", func() {
		list, err := c.ListPluginMarketplaces(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0]).To(SatisfyAll(HaveField("ID", "acme"), HaveField("HeadSHA", "abc"), HaveField("Active", true)))
		m, err := c.AddPluginMarketplace(ctx, "team/plugins", "", "team")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.ID).To(Equal("team"))
		m, err = c.RefreshPluginMarketplace(ctx, "acme")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.RefreshedAt).NotTo(BeNil())
		Expect(seen).To(ContainElement(`POST /api/plugins/marketplaces {"id":"team","source":"team/plugins"}`))
	})

	It("browses and lists installs", func() {
		page, err := c.BrowsePlugins(ctx, localaitools.PluginBrowseQuery{Query: "fmt", Marketplace: "acme"})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Items).To(HaveLen(1))
		Expect(page.Items[0]).To(SatisfyAll(HaveField("Plugin", "fmt@acme"), HaveField("Installs", []string{"i1"})))
		Expect(*page.Items[0].RunsCode).To(BeFalse())
		list, err := c.ListPluginInstalls(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(list[0]).To(SatisfyAll(HaveField("State", "update_available"), HaveField("UpdateSHA", "def")))
		Expect(seen).To(ContainElements("GET /api/plugins/catalog?marketplace=acme&q=fmt ", "GET /api/plugins/installs?all=true "))
	})

	It("inspects, then installs with the preview token", func() {
		res, err := c.InstallPlugin(ctx, "fmt@acme", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(SatisfyAll(HaveField("JobID", "job-9"), HaveField("SHA", "abc"), HaveField("Skills", 1)))
		Expect(seen).To(Equal([]string{"POST /api/plugins/inspect fmt@acme", `POST /api/plugins/installs {"scope":"user","token":"tok-fmt@acme"}`}))
	})

	It("turns 403 not_human_surface into the browser-session error with the review page", func() {
		_, err := c.InstallPlugin(ctx, "hook@acme", "instance")
		Expect(errors.Is(err, localaitools.ErrPluginNeedsBrowser)).To(BeTrue())
		Expect(err.Error()).To(SatisfyAll(ContainSubstring("hook@acme runs code (hook-command)"), ContainSubstring("/app/addons?plugin=hook%40acme&tab=plugins")))
	})

	It("previews an update and lists the changed paths", func() {
		u, err := c.CheckPluginUpdate(ctx, "i1")
		Expect(err).NotTo(HaveOccurred())
		Expect(u).To(SatisfyAll(HaveField("ToSHA", "def"), HaveField("Added", []string{"skills/b/SKILL.md"}), HaveField("Changed", []string{"hooks/hooks.json"}), HaveField("NeedsCodeReview", true)))
		Expect(u.ReviewURL).To(ContainSubstring("tab=plugins"))
	})

	It("switches an install, never with reenable, and removes it", func() {
		st, err := c.SetPluginEnabled(ctx, "i1", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Enabled).To(BeFalse())
		Expect(seen).To(ContainElement(`PUT /api/plugins/installs/i1/me {"enabled":false}`))
		_, err = c.SetPluginEnabled(ctx, "gained", true)
		Expect(err).To(MatchError(localaitools.ErrPluginNeedsReenable))
		Expect(c.RemovePlugin(ctx, "i1")).To(Succeed())
		Expect(seen).To(ContainElement("DELETE /api/plugins/installs/i1 "))
	})
})
