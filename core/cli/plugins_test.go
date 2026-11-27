package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakePluginsAPI is /api/plugins and /api/operations of a server, as far as
// the commands use them. Each test changes its fields.
type fakePluginsAPI struct {
	mu   sync.Mutex
	seen []string
	// codePlugin names the plugin whose inspect says it runs code.
	codePlugin string
	// installStatus and installBody, when set, answer POST installs.
	installStatus int
	installBody   string
	// opsPolls is how many polls of /api/operations still show the job.
	opsPolls int
	opsError string
	// installed is set once the job "ran".
	installed bool
	// meStatus and meBody answer PUT .../me when set.
	meStatus int
	meBody   string
	// previewCode makes the update preview need a code review.
	previewCode bool
}

func (f *fakePluginsAPI) record(r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	line := r.Method + " " + r.URL.RequestURI()
	if len(body) > 0 {
		line += " " + string(body)
	}
	f.mu.Lock()
	f.seen = append(f.seen, line)
	f.mu.Unlock()
	return string(body)
}

func (f *fakePluginsAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func apiErr(w http.ResponseWriter, status int, typ, msg string, param ...string) {
	w.WriteHeader(status)
	e := map[string]any{"code": status, "type": typ, "message": msg}
	if len(param) > 0 {
		e["param"] = param[0]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": e})
}

func (f *fakePluginsAPI) server() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins/marketplaces", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		_, _ = w.Write([]byte(`{"marketplaces":[{"id":"localai","location":"mudler/LocalAI","enabled":true,"active":true,"default":true,"headSha":"0123456789abcdef0123","refreshedAt":"2026-11-27T10:00:00Z"},{"id":"acme","location":"acme/plugins","enabled":true,"active":true,"lastError":"fetch_failed"},{"id":"off","location":"o/p","enabled":false}]}`))
	})
	mux.HandleFunc("POST /api/plugins/marketplaces", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		_, _ = w.Write([]byte(`{"id":"team","location":"team/plugins","headSha":"abcdef0123456789"}`))
	})
	mux.HandleFunc("DELETE /api/plugins/marketplaces/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/plugins/marketplaces/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if r.PathValue("id") == "acme" {
			apiErr(w, http.StatusBadGateway, "fetch_failed", "The plugin or the marketplace could not be read.")
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","headSha":"fedcba9876543210"}`))
	})
	mux.HandleFunc("GET /api/plugins/catalog", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		_, _ = w.Write([]byte(`{"items":[{"name":"fmt","plugin":"fmt@acme","description":"Formats   code","version":"1.0.0","supported":true,"runsCode":false,"installs":[{"id":"i1","scope":"user","state":"installed"}]},{"name":"npmx","plugin":"npmx@acme","supported":false,"reason":"npm"}],"total":3}`))
	})
	mux.HandleFunc("GET /api/plugins/catalog/{mkt}/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if r.PathValue("name") != "fmt" {
			apiErr(w, http.StatusNotFound, "not_found", "plugins: not found")
			return
		}
		_, _ = w.Write([]byte(`{"name":"fmt","plugin":"fmt@acme","description":"Formats code","version":"1.0.0","author":"Ada","supported":true,"installs":[{"id":"i1","scope":"user","state":"installed"}]}`))
	})
	mux.HandleFunc("POST /api/plugins/inspect", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Plugin string `json:"plugin"`
		}
		_ = json.Unmarshal([]byte(f.record(r)), &req)
		code := req.Plugin == f.codePlugin
		reasons := []string{}
		files := []string{}
		if code {
			reasons, files = []string{"hook-command"}, []string{"hooks/hooks.json", "check.sh"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "tok", "plugin": req.Plugin, "version": "1.0.0", "sha": "abcdef0123456789abcdef0123456789abcdef01",
			"digest": "sha256:aa", "sizeBytes": 120, "fileCount": 3, "runsCode": code, "codeReasons": reasons, "codeFiles": files,
			"hosts": []string{"api.example.com"},
			"model": map[string]any{"skills": []any{map[string]any{}}, "commands": []any{map[string]any{}, map[string]any{}}},
		})
	})
	mux.HandleFunc("POST /api/plugins/installs", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if f.installStatus != 0 {
			w.WriteHeader(f.installStatus)
			_, _ = w.Write([]byte(f.installBody))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"jobID":"job-1","plugin":"fmt@acme"}`))
	})
	mux.HandleFunc("GET /api/plugins/installs", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		installed := f.installed
		f.mu.Unlock()
		list := []map[string]any{
			{"id": "i9", "plugin": "lint@acme", "version": "2.0.0", "scope": "instance", "sha": "1111111111111111", "state": "update_available", "updateSha": "2222222222222222", "enabled": false, "needsReenable": true},
			{"id": "i8", "plugin": "hook@acme", "scope": "user", "sha": "3333", "state": "blocked", "enabled": true},
		}
		if installed {
			list = append(list, map[string]any{"id": "i1", "plugin": "fmt@acme", "scope": "user", "sha": "abcdef0123456789abcdef0123456789abcdef01", "state": "installed", "enabled": true})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"installs": list})
	})
	mux.HandleFunc("GET /api/operations", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.opsPolls > 0 {
			f.opsPolls--
			_ = json.NewEncoder(w).Encode(map[string]any{"operations": []map[string]any{
				{"jobID": "job-1", "isPlugin": true, "progress": 40, "message": "fetching", "error": f.opsError},
			}})
			return
		}
		if f.opsError == "" {
			f.installed = true
		}
		_, _ = w.Write([]byte(`{"operations":[]}`))
	})
	mux.HandleFunc("POST /api/operations/{job}/cancel", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("POST /api/plugins/installs/{id}/update/preview", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if r.PathValue("id") == "i8" {
			apiErr(w, http.StatusConflict, "no_update", "plugins: no update available")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "up-tok", "install": "i9", "plugin": "lint@acme", "fromSha": "1111111111111111", "toSha": "2222222222222222",
			"added": []any{map[string]any{"path": "skills/new/SKILL.md"}}, "removed": []any{},
			"changed":        []any{map[string]any{"path": "hooks/hooks.json", "code": f.previewCode}},
			"oldCodeReasons": []string{}, "newCodeReasons": []string{}, "needsCodeReview": f.previewCode,
		})
	})
	mux.HandleFunc("POST /api/plugins/installs/{id}/update", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"jobID":"job-1","plugin":"lint@acme"}`))
	})
	mux.HandleFunc("PUT /api/plugins/installs/{id}/me", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if f.meStatus != 0 {
			w.WriteHeader(f.meStatus)
			_, _ = w.Write([]byte(f.meBody))
			return
		}
		_, _ = w.Write([]byte(`{"install":"` + r.PathValue("id") + `"}`))
	})
	mux.HandleFunc("DELETE /api/plugins/installs/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	return httptest.NewServer(mux)
}

var _ = Describe("local-ai plugins", func() {
	var (
		api *fakePluginsAPI
		srv *httptest.Server
		out *bytes.Buffer
	)
	BeforeEach(func() {
		api = &fakePluginsAPI{}
		srv = api.server()
		DeferCleanup(srv.Close)
		out = &bytes.Buffer{}
	})

	// parse runs kong over the real command tree, then points the chosen
	// command at the fake server.
	parse := func(args ...string) (*PluginsCMD, *kong.Context) {
		var command struct {
			Plugins PluginsCMD `cmd:"" name:"plugins"`
		}
		p, err := kong.New(&command)
		Expect(err).NotTo(HaveOccurred())
		kctx, err := p.Parse(append([]string{"plugins"}, args...))
		Expect(err).NotTo(HaveOccurred())
		return &command.Plugins, kctx
	}
	wire := func(s *PluginsServer, input string) {
		s.Target, s.APIKey = srv.URL, "sk-test"
		s.output, s.input, s.poll = out, strings.NewReader(input), time.Millisecond
	}

	It("parses every subcommand", func() {
		for _, args := range [][]string{
			{"marketplace", "list"}, {"marketplace", "add", "acme/plugins", "--ref", "v1", "--id", "acme"},
			{"marketplace", "remove", "acme"}, {"marketplace", "refresh"}, {"browse", "fmt", "--text-only"},
			{"search", "fmt"}, {"info", "fmt@acme"}, {"install", "fmt@acme", "--scope", "instance", "-y"},
			{"list", "--all"}, {"update"}, {"update", "fmt@acme", "--check"}, {"remove", "i1", "--yes"},
			{"enable", "i1", "--accept-code"}, {"disable", "i1"}, {"validate", "."}, {"validate", "--marketplace", "plugins_test.go"},
		} {
			_, kctx := parse(args...)
			Expect(kctx.Command()).To(HavePrefix("plugins "), strings.Join(args, " "))
		}
	})

	It("lists the marketplaces with their state and sends the key", func() {
		cmd, _ := parse("marketplace", "list")
		wire(&cmd.Marketplace.List.PluginsServer, "")
		Expect(cmd.Marketplace.List.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(
			ContainSubstring("localai"), ContainSubstring("active, curated"), ContainSubstring("0123456789ab"),
			ContainSubstring("last refresh failed: fetch_failed"), ContainSubstring("off"), ContainSubstring("never"),
		))
	})

	It("adds, removes and refreshes marketplaces, and reports each failed refresh", func() {
		cmd, _ := parse("marketplace", "add", "team/plugins", "--ref", "v1")
		wire(&cmd.Marketplace.Add.PluginsServer, "")
		Expect(cmd.Marketplace.Add.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("Added marketplace team (team/plugins) at commit abcdef012345."))

		cmd, _ = parse("marketplace", "remove", "team")
		wire(&cmd.Marketplace.Remove.PluginsServer, "")
		Expect(cmd.Marketplace.Remove.Run(nil)).To(Succeed())

		cmd, _ = parse("marketplace", "refresh")
		wire(&cmd.Marketplace.Refresh.PluginsServer, "")
		err := cmd.Marketplace.Refresh.Run(nil)
		Expect(err).To(MatchError(ContainSubstring("acme: the server answered 502 fetch_failed")))
		Expect(out.String()).To(ContainSubstring("Refreshed localai"))
		Expect(api.calls()).To(ContainElements(
			`POST /api/plugins/marketplaces {"id":"","ref":"v1","source":"team/plugins"}`,
			"DELETE /api/plugins/marketplaces/team",
			"POST /api/plugins/marketplaces/localai/refresh",
			"POST /api/plugins/marketplaces/acme/refresh",
		))
		Expect(api.calls()).NotTo(ContainElement("POST /api/plugins/marketplaces/off/refresh"))
	})

	It("browses with the filters and shows one entry", func() {
		cmd, _ := parse("search", "fmt", "--marketplace", "acme", "--text-only", "--limit", "2")
		wire(&cmd.Browse.PluginsServer, "")
		Expect(cmd.Browse.Run(nil)).To(Succeed())
		Expect(api.calls()).To(ContainElement("GET /api/plugins/catalog?limit=2&marketplace=acme&q=fmt&runs_code=false"))
		Expect(out.String()).To(SatisfyAll(ContainSubstring("fmt@acme"), ContainSubstring("Formats code"), ContainSubstring("unsupported"), ContainSubstring("2 of 3 plugins")))

		out.Reset()
		cmd, _ = parse("info", "fmt@acme")
		wire(&cmd.Info.PluginsServer, "")
		Expect(cmd.Info.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(ContainSubstring("author:      Ada"), ContainSubstring("install:     i1 (user, installed)")))

		cmd, _ = parse("info", "fmt")
		wire(&cmd.Info.PluginsServer, "")
		Expect(cmd.Info.Run(nil)).To(MatchError(ContainSubstring("not name@marketplace")))
	})

	It("reviews a plugin, installs it and follows the job until the install lands", func() {
		api.opsPolls = 2
		cmd, _ := parse("install", "fmt@acme", "--yes")
		wire(&cmd.Install.PluginsServer, "")
		Expect(cmd.Install.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(
			ContainSubstring("commit:      abcdef0123456789abcdef0123456789abcdef01"),
			ContainSubstring("1 skill, 2 commands, 0 agents, 0 MCP servers"),
			ContainSubstring("connects to: api.example.com"),
			ContainSubstring("runs code:   no"),
			ContainSubstring("not signed"),
			ContainSubstring(" 40% fetching"),
			ContainSubstring("Installed fmt@acme as i1 (user)."),
		))
		Expect(api.calls()).To(ContainElement(`POST /api/plugins/installs {"scope":"user","token":"tok"}`))
	})

	It("asks before it installs, and installs nothing without a yes", func() {
		cmd, _ := parse("install", "fmt@acme")
		wire(&cmd.Install.PluginsServer, "n\n")
		Expect(cmd.Install.Run(nil)).To(MatchError(ContainSubstring("not confirmed")))
		Expect(out.String()).To(ContainSubstring("Install fmt@acme for you? [y/N]"))
		for _, c := range api.calls() {
			Expect(c).NotTo(HavePrefix("POST /api/plugins/installs"))
		}

		cmd, _ = parse("install", "fmt@acme", "--scope", "instance")
		wire(&cmd.Install.PluginsServer, "y\n")
		Expect(cmd.Install.Run(nil)).To(Succeed())
		Expect(api.calls()).To(ContainElement(`POST /api/plugins/installs {"scope":"instance","token":"tok"}`))
	})

	It("prints the review page for a plugin that runs code, exits 0 and installs nothing", func() {
		api.codePlugin = "hook@acme"
		cmd, _ := parse("install", "hook@acme", "--scope", "instance", "--yes")
		wire(&cmd.Install.PluginsServer, "")
		Expect(cmd.Install.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(
			ContainSubstring("runs code:   yes (hook-command)"),
			ContainSubstring("code file:   hooks/hooks.json"),
			ContainSubstring(srv.URL+"/app/addons?plugin=hook%40acme&tab=plugins"),
		))
		for _, c := range api.calls() {
			Expect(c).NotTo(HavePrefix("POST /api/plugins/installs"))
		}
	})

	It("prints the review page when the server answers 403 not_human_surface, and exits 0", func() {
		api.installStatus, api.installBody = http.StatusForbidden, `{"error":{"code":403,"type":"not_human_surface","message":"This needs the administrator's own browser session."}}`
		cmd, _ := parse("install", "fmt@acme", "--yes")
		wire(&cmd.Install.PluginsServer, "")
		Expect(cmd.Install.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("/app/addons?plugin=fmt%40acme&tab=plugins"))
	})

	It("reads the install of everyone from error.param on 409 instance_installed", func() {
		api.installStatus, api.installBody = http.StatusConflict, `{"error":{"code":409,"type":"instance_installed","param":"inst-7","message":"installed for everyone"}}`
		cmd, _ := parse("install", "fmt@acme", "--yes")
		wire(&cmd.Install.PluginsServer, "")
		Expect(cmd.Install.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("installed for everyone already (install inst-7). Turn it on for you with: local-ai plugins enable inst-7"))
	})

	It("reports any other refusal and a failed job as errors", func() {
		api.installStatus, api.installBody = http.StatusForbidden, `{"error":{"code":403,"type":"needs_admin","message":"plugins: an administrator must install this plugin"}}`
		cmd, _ := parse("install", "fmt@acme", "--yes")
		wire(&cmd.Install.PluginsServer, "")
		Expect(cmd.Install.Run(nil)).To(MatchError(ContainSubstring("403 needs_admin: plugins: an administrator must install this plugin")))

		api.installStatus, api.opsPolls, api.opsError = 0, 1, "digest mismatch"
		cmd, _ = parse("install", "fmt@acme", "--yes")
		wire(&cmd.Install.PluginsServer, "")
		Expect(cmd.Install.Run(nil)).To(MatchError(ContainSubstring("job job-1 failed: digest mismatch")))
	})

	It("does not follow a redirect, so the API key never reaches another host", func() {
		var leaked sync.Mutex
		gotKey := ""
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			leaked.Lock()
			gotKey = r.Header.Get("Authorization")
			leaked.Unlock()
			_, _ = w.Write([]byte(`{}`))
		}))
		DeferCleanup(other.Close)
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
		}))
		DeferCleanup(redirect.Close)
		s := &PluginsServer{Target: redirect.URL, APIKey: "secret-key"}
		err := s.call(context.Background(), http.MethodGet, "/api/plugins/installs", nil, nil)
		Expect(err).To(HaveOccurred())
		leaked.Lock()
		defer leaked.Unlock()
		Expect(gotKey).To(BeEmpty(), "the key reached the redirect target")
	})

	It("cancels the job on the server when it is interrupted", func() {
		api.opsPolls = 1000
		s := &PluginsServer{}
		wire(s, "")
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			Eventually(func() []string { return api.calls() }).Should(ContainElement("GET /api/operations"))
			cancel()
		}()
		err := s.waitJob(ctx, "job-1", func(context.Context) (bool, error) { return false, nil })
		Expect(err).To(MatchError(ContainSubstring("interrupted: job job-1 cancelled")))
		Expect(api.calls()).To(ContainElement("POST /api/operations/job-1/cancel"))
	})

	It("lists the installs and the switch of the caller", func() {
		cmd, _ := parse("list", "--all")
		wire(&cmd.List.PluginsServer, "")
		Expect(cmd.List.Run(nil)).To(Succeed())
		Expect(api.calls()).To(ContainElement("GET /api/plugins/installs?all=true"))
		Expect(out.String()).To(SatisfyAll(ContainSubstring("lint@acme"), ContainSubstring("update_available"), ContainSubstring("off (runs code now)")))
	})

	It("lists the updates, previews one and applies it", func() {
		cmd, _ := parse("update")
		wire(&cmd.Update.PluginsServer, "")
		Expect(cmd.Update.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(ContainSubstring("i9  lint@acme  111111111111 -> 222222222222"), ContainSubstring("blocked: the new version runs code")))

		out.Reset()
		cmd, _ = parse("update", "lint@acme", "--check")
		wire(&cmd.Update.PluginsServer, "")
		Expect(cmd.Update.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(ContainSubstring("added    skills/new/SKILL.md"), ContainSubstring("changed  hooks/hooks.json")))
		for _, c := range api.calls() {
			Expect(c).NotTo(HavePrefix("POST /api/plugins/installs/i9/update "))
		}

		api.opsPolls = 1
		cmd, _ = parse("update", "i9", "--yes")
		wire(&cmd.Update.PluginsServer, "")
		// The fake never moves i9 to the new commit, so the job ends
		// without the change.
		Expect(cmd.Update.Run(nil)).To(MatchError(ContainSubstring("ended without the change")))
		Expect(api.calls()).To(ContainElement(`POST /api/plugins/installs/i9/update {"token":"up-tok"}`))

		out.Reset()
		cmd, _ = parse("update", "hook@acme")
		wire(&cmd.Update.PluginsServer, "")
		Expect(cmd.Update.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("hook@acme is at its latest version."))
	})

	It("sends an update whose code changed to the browser", func() {
		api.previewCode = true
		cmd, _ := parse("update", "i9", "--yes")
		wire(&cmd.Update.PluginsServer, "")
		Expect(cmd.Update.Run(nil)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(ContainSubstring("changed  hooks/hooks.json  (code)"), ContainSubstring("/app/addons?plugin=lint%40acme&tab=plugins")))
		for _, c := range api.calls() {
			Expect(c).NotTo(HavePrefix("POST /api/plugins/installs/i9/update "))
		}
	})

	It("turns a plugin on and off, and asks for --accept-code when it gained code", func() {
		cmd, _ := parse("disable", "lint@acme")
		wire(&cmd.Disable.PluginsServer, "")
		Expect(cmd.Disable.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("lint@acme is off for you."))

		api.meStatus, api.meBody = http.StatusConflict, `{"error":{"code":409,"type":"needs_reenable","message":"runs code"}}`
		cmd, _ = parse("enable", "i9")
		wire(&cmd.Enable.PluginsServer, "")
		Expect(cmd.Enable.Run(nil)).To(MatchError(ContainSubstring("local-ai plugins enable --accept-code i9")))

		api.meStatus = 0
		cmd, _ = parse("enable", "i9", "--accept-code")
		wire(&cmd.Enable.PluginsServer, "")
		Expect(cmd.Enable.Run(nil)).To(Succeed())
		Expect(api.calls()).To(ContainElements(`PUT /api/plugins/installs/i9/me {"enabled":false}`, `PUT /api/plugins/installs/i9/me {"enabled":true,"reenable":true}`))
	})

	It("removes an install after a confirmation, and names a missing one", func() {
		cmd, _ := parse("remove", "lint@acme", "--yes")
		wire(&cmd.Remove.PluginsServer, "")
		Expect(cmd.Remove.Run(nil)).To(Succeed())
		Expect(api.calls()).To(ContainElement("DELETE /api/plugins/installs/i9"))

		cmd, _ = parse("remove", "nope@acme")
		wire(&cmd.Remove.PluginsServer, "")
		Expect(cmd.Remove.Run(nil)).To(MatchError(ContainSubstring(`no install of "nope@acme"`)))
	})

	It("rejects a target that is not a web address", func() {
		cmd, _ := parse("list")
		wire(&cmd.List.PluginsServer, "")
		cmd.List.Target = "ftp://x"
		Expect(cmd.List.Run(nil)).To(MatchError(ContainSubstring("is not a http or https URL")))
	})
})
