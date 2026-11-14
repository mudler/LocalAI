package connections_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/connections"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/pkg/system"
)

const secret = "sk-SECRET-0123456789"

// fakeRemote is a LocalAI that lists a changeable set of models.
type fakeRemote struct {
	*httptest.Server
	mu     sync.Mutex
	models []string
	status int // when not zero, every call answers it
	key    string
	calls  int
}

func newFakeRemote(models ...string) *fakeRemote {
	f := &fakeRemote{models: models, key: secret}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte("rejected " + r.Header.Get("Authorization")))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("rejected " + r.Header.Get("Authorization")))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			data := []map[string]any{}
			for _, m := range f.models {
				data = append(data, map[string]any{"id": m})
			}
			writeJSON(w, map[string]any{"data": data})
		case "/v1/models/capabilities":
			data := []map[string]any{}
			for _, m := range f.models {
				data = append(data, map[string]any{"id": m, "capabilities": []string{"chat", "tools"}})
			}
			writeJSON(w, map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakeRemote) set(models ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.models = models
}

func (f *fakeRemote) fail(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

type fakeUnloader struct {
	mu    sync.Mutex
	names []string
}

func (u *fakeUnloader) ShutdownModel(name string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.names = append(u.names, name)
	return nil
}

func (u *fakeUnloader) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.names...)
}

type broadcastEvent struct{ Name, Op, Revision string }

type fakeBroadcaster struct {
	mu     sync.Mutex
	events []broadcastEvent
}

func (b *fakeBroadcaster) BroadcastModelsChangedRevision(name, op, rev string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, broadcastEvent{name, op, rev})
}

func (b *fakeBroadcaster) ops(name string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, e := range b.events {
		if e.Name == name {
			out = append(out, e.Op)
		}
	}
	return out
}

// env is one frontend: a models directory with the real loader and model
// admin, and a service on a store that tests may share between frontends.
type env struct {
	dir      string
	loader   *config.ModelConfigLoader
	admin    *modeladmin.ConfigService
	svc      connections.Service
	unloader *fakeUnloader
	bus      *fakeBroadcaster
}

func newEnv(store connections.Store) *env {
	dir := GinkgoT().TempDir()
	loader := config.NewModelConfigLoader(dir)
	app := &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
	e := &env{dir: dir, loader: loader, admin: modeladmin.NewConfigService(loader, app), unloader: &fakeUnloader{}, bus: &fakeBroadcaster{}}
	svc, err := connections.New(connections.Options{
		Store: store, Admin: e.admin, Configs: loader, Unloader: e.unloader, Broadcaster: e.bus, ModelsPath: dir,
	})
	Expect(err).NotTo(HaveOccurred())
	e.svc = svc
	return e
}

func (e *env) file(name string) string { return filepath.Join(e.dir, name+".yaml") }

func (e *env) read(name string) string {
	raw, err := os.ReadFile(e.file(name))
	Expect(err).NotTo(HaveOccurred())
	return string(raw)
}

func (e *env) config(name string) config.ModelConfig {
	cfg, ok := e.loader.GetModelConfig(name)
	Expect(ok).To(BeTrue(), name)
	return cfg
}

func chat(ids ...string) []connections.UpstreamModel {
	var out []connections.UpstreamModel
	for _, id := range ids {
		out = append(out, connections.UpstreamModel{ID: id, Usecases: []string{"chat"}})
	}
	return out
}

func memStore() connections.Store {
	s, err := connections.NewJSONStore("")
	Expect(err).NotTo(HaveOccurred())
	return s
}

var _ = Describe("Service", func() {
	var (
		ctx    = context.Background()
		remote *fakeRemote
		e      *env
	)
	BeforeEach(func() {
		remote = newFakeRemote("alpha", "beta", "gamma")
		DeferCleanup(remote.Close)
		e = newEnv(memStore())
	})

	create := func(models ...connections.UpstreamModel) connections.Result {
		res, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret, Models: models})
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	Describe("Test", func() {
		It("lists models and does not save anything", func() {
			res, err := e.svc.Test(ctx, connections.KindLocalAI, remote.URL, secret)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.OK).To(BeTrue())
			Expect(ids(res.Models)).To(Equal([]string{"alpha", "beta", "gamma"}))
			all, _ := e.svc.List(ctx)
			Expect(all).To(BeEmpty())
		})
		It("returns a failed test as a result, not an error", func() {
			res, err := e.svc.Test(ctx, connections.KindLocalAI, remote.URL, "wrong")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.OK).To(BeFalse())
			Expect(res.Step).To(Equal(connections.StepAuth))
			res, err = e.svc.Test(ctx, connections.KindLocalAI, "nonsense", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Step).To(Equal(connections.StepInvalidURL))
			remote.set()
			res, _ = e.svc.Test(ctx, connections.KindLocalAI, remote.URL, secret)
			Expect(res.Step).To(Equal(connections.StepNoModels))
		})
		It("rejects an unknown kind", func() {
			_, err := e.svc.Test(ctx, "ftp", remote.URL, "")
			Expect(errors.Is(err, connections.ErrInvalid)).To(BeTrue())
		})
	})

	Describe("Create", func() {
		It("writes entries that the loader sees at once, with no key in them", func() {
			res := create(chat("alpha", "beta")...)
			Expect(res.Created).To(ConsistOf("alpha", "beta"))
			Expect(res.Skipped).To(BeEmpty())
			c := res.Connection
			Expect(c.Entries).To(ConsistOf("alpha", "beta"))
			Expect(c.KeySet).To(BeTrue())
			Expect(c.Status).To(Equal(connections.StatusOK))
			Expect(c.LastRefresh).NotTo(BeNil())
			Expect(ids(c.New)).To(Equal([]string{"gamma"}))
			Expect(c.Unavailable).To(BeEmpty())

			cfg := e.config("alpha")
			Expect(cfg.Backend).To(Equal("localai-proxy"))
			Expect(cfg.Proxy.Connection).To(Equal(c.ID))
			Expect(cfg.Proxy.UpstreamModel).To(Equal("alpha"))
			Expect(cfg.Proxy.UpstreamURL).To(Equal(remote.URL))
			Expect(cfg.Proxy.APIKey + cfg.Proxy.APIKeyEnv + cfg.Proxy.APIKeyFile).To(BeEmpty())
			Expect(e.read("alpha")).NotTo(ContainSubstring(secret))
			Expect(e.bus.ops("alpha")).To(Equal([]string{"install"}))

			key, err := e.svc.ResolveKey(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(secret))
		})
		It("with no models lists them as new and writes no entry", func() {
			res := create()
			Expect(res.Created).To(BeEmpty())
			Expect(ids(res.Connection.New)).To(Equal([]string{"alpha", "beta", "gamma"}))
			Expect(res.Connection.Entries).To(BeEmpty())
		})
		It("records a failing remote instead of failing", func() {
			res, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Bad", URL: remote.URL, APIKey: "wrong"})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Connection.Status).To(Equal(connections.StatusAuthFailed))
			Expect(res.Connection.LastError).NotTo(BeEmpty())
		})
		It("refuses input that is not valid with a message", func() {
			for name, r := range map[string]connections.CreateRequest{
				"no name":       {Kind: connections.KindLocalAI, URL: remote.URL},
				"bad kind":      {Kind: "x", Name: "n", URL: remote.URL},
				"bad url":       {Kind: connections.KindLocalAI, Name: "n", URL: "remote"},
				"no url":        {Kind: connections.KindOpenAI, Name: "n"},
				"anthropic key": {Kind: connections.KindAnthropic, Name: "n"},
				"multiline key": {Kind: connections.KindLocalAI, Name: "n", URL: remote.URL, APIKey: "a\nb"},
				"credentials":   {Kind: connections.KindLocalAI, Name: "n", URL: "https://u:p@remote.example.com"},
			} {
				_, err := e.svc.Create(ctx, r)
				Expect(errors.Is(err, connections.ErrInvalid)).To(BeTrue(), name)
				Expect(err.Error()).NotTo(BeEmpty(), name)
			}
		})
		It("refuses a name that is taken", func() {
			create()
			_, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "home", URL: remote.URL, APIKey: secret})
			Expect(errors.Is(err, connections.ErrConflict)).To(BeTrue())
		})
		It("creates cloud entries for openai and anthropic", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					if r.Header.Get("x-api-key") != "" {
						writeJSON(w, map[string]any{"data": []map[string]any{{"id": "claude-x"}}, "has_more": false})
						return
					}
					writeJSON(w, map[string]any{"data": []map[string]any{{"id": "gpt-x"}, {"id": "whisper-1"}}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			oa, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindOpenAI, Name: "OpenAI", URL: srv.URL + "/v1", APIKey: "k", Models: []connections.UpstreamModel{{ID: "gpt-x"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(oa.Created).To(Equal([]string{"gpt-x"}))
			Expect(e.config("gpt-x").Proxy.UpstreamURL).To(Equal(srv.URL + "/v1/chat/completions"))
			Expect(oa.Connection.Status).To(Equal(connections.StatusOK))
			an, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindAnthropic, Name: "Claude", URL: srv.URL, APIKey: "k", Models: []connections.UpstreamModel{{ID: "claude-x"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(an.Created).To(Equal([]string{"claude-x"}))
			cfg := e.config("claude-x")
			Expect(cfg.Proxy.Provider).To(Equal("anthropic"))
			Expect(cfg.Proxy.UpstreamURL).To(Equal(srv.URL + "/v1/messages"))
		})
	})

	Describe("Adopt", func() {
		It("adds entries, removes them from new and skips what is adopted", func() {
			c := create(chat("alpha")...).Connection
			res, err := e.svc.Adopt(ctx, c.ID, chat("alpha", "beta"))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"beta"}))
			Expect(res.Skipped).To(Equal([]connections.Skip{{ID: "alpha", Reason: "already adopted"}}))
			Expect(res.Connection.Entries).To(ConsistOf("alpha", "beta"))
			Expect(ids(res.Connection.New)).To(Equal([]string{"gamma"}))
		})
		It("prefixes the name when a model of that name exists, and skips when both are taken", func() {
			Expect(os.WriteFile(e.file("beta"), []byte("name: beta\nbackend: llama-cpp\nparameters:\n  model: x.gguf\n"), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			c := create().Connection
			res, err := e.svc.Adopt(ctx, c.ID, chat("beta"))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"home-beta"}))
			Expect(e.config("home-beta").Proxy.UpstreamModel).To(Equal("beta"))
			Expect(e.config("beta").Backend).To(Equal("llama-cpp"), "the other model is untouched")

			Expect(os.WriteFile(e.file("home-gamma"), []byte("name: home-gamma\nbackend: llama-cpp\nparameters:\n  model: x.gguf\n"), 0o644)).To(Succeed())
			Expect(os.WriteFile(e.file("gamma"), []byte("name: gamma\nbackend: llama-cpp\nparameters:\n  model: x.gguf\n"), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			res, err = e.svc.Adopt(ctx, c.ID, chat("gamma"))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(BeEmpty())
			Expect(res.Skipped).To(HaveLen(1))
			Expect(res.Skipped[0].ID).To(Equal("gamma"))
			Expect(res.Skipped[0].Reason).NotTo(BeEmpty())
		})
		It("does not take a name that another connection owns", func() {
			other := newFakeRemote("alpha")
			defer other.Close()
			first := create(chat("alpha")...)
			Expect(first.Created).To(Equal([]string{"alpha"}))
			res, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Work", URL: other.URL, APIKey: secret, Models: chat("alpha")})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"work-alpha"}))
		})
		It("sanitises ids that hold slashes and colons", func() {
			c := create().Connection
			res, err := e.svc.Adopt(ctx, c.ID, chat("org/model:8b"))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"org-model-8b"}))
			Expect(e.config("org-model-8b").Proxy.UpstreamModel).To(Equal("org/model:8b"))
		})
		It("needs at least one model and a known connection", func() {
			c := create().Connection
			_, err := e.svc.Adopt(ctx, c.ID, nil)
			Expect(errors.Is(err, connections.ErrInvalid)).To(BeTrue())
			_, err = e.svc.Adopt(ctx, "missing", chat("a"))
			Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("Refresh", func() {
		It("flags new and removed models and never writes an entry file", func() {
			c := create(chat("alpha", "beta")...).Connection
			beforeA, beforeB := e.read("alpha"), e.read("beta")
			infoA, _ := os.Stat(e.file("alpha"))
			remote.set("beta", "delta")
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(connections.StatusOK))
			Expect(got.Unavailable).To(Equal([]string{"alpha"}))
			Expect(ids(got.New)).To(Equal([]string{"delta"}))
			Expect(got.Entries).To(ConsistOf("alpha", "beta"), "nothing is deleted")
			Expect(e.read("alpha")).To(Equal(beforeA))
			Expect(e.read("beta")).To(Equal(beforeB))
			infoA2, _ := os.Stat(e.file("alpha"))
			Expect(infoA2.ModTime()).To(Equal(infoA.ModTime()))
			_ = e.config("alpha")

			remote.set("alpha", "beta")
			got, _ = e.svc.Refresh(ctx, c.ID)
			Expect(got.Unavailable).To(BeEmpty(), "a model that returns is available again")
		})
		It("leaves an entry that a person edited untouched", func() {
			c := create(chat("alpha")...).Connection
			edited := e.read("alpha") + "description: my own words\n"
			Expect(os.WriteFile(e.file("alpha"), []byte(edited), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			remote.set("alpha", "zeta")
			_, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(e.read("alpha")).To(Equal(edited))
		})
		It("records auth and unreachable failures and keeps the earlier list", func() {
			c := create(chat("alpha")...).Connection
			remote.fail(http.StatusUnauthorized)
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(connections.StatusAuthFailed))
			Expect(got.LastError).NotTo(BeEmpty())
			Expect(got.LastError).NotTo(ContainSubstring("SECRET"))
			Expect(ids(got.New)).To(Equal([]string{"beta", "gamma"}))
			remote.fail(http.StatusBadGateway)
			got, _ = e.svc.Refresh(ctx, c.ID)
			Expect(got.Status).To(Equal(connections.StatusUnreachable))
			remote.fail(0)
			got, _ = e.svc.Refresh(ctx, c.ID)
			Expect(got.Status).To(Equal(connections.StatusOK))
			Expect(got.LastError).To(BeEmpty())
		})
		It("records a remote that lists nothing usable and shows every entry as unavailable", func() {
			c := create(chat("alpha")...).Connection
			remote.set()
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(connections.StatusOK))
			Expect(got.Unavailable).To(Equal([]string{"alpha"}))
			Expect(got.LastError).NotTo(BeEmpty())
		})
		It("forgets an entry whose model config was deleted, so the model is new again", func() {
			c := create(chat("alpha")...).Connection
			_, err := e.admin.DeleteConfig(ctx, "alpha")
			Expect(err).NotTo(HaveOccurred())
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Entries).To(BeEmpty())
			Expect(ids(got.New)).To(ContainElement("alpha"))
		})
		It("reports an unknown connection", func() {
			_, err := e.svc.Refresh(ctx, "missing")
			Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("Update", func() {
		It("renames and refuses a name that is taken", func() {
			c := create().Connection
			other, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Other", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			name := "Renamed"
			got, err := e.svc.Update(ctx, c.ID, connections.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("Renamed"))
			taken := "other"
			_, err = e.svc.Update(ctx, c.ID, connections.UpdateRequest{Name: &taken})
			Expect(errors.Is(err, connections.ErrConflict)).To(BeTrue())
			Expect(other.Connection.Name).To(Equal("Other"))
		})
		It("rotates the key: the new key resolves and loaded entries are stopped", func() {
			c := create(chat("alpha", "beta")...).Connection
			key := "sk-NEW-key"
			got, err := e.svc.Update(ctx, c.ID, connections.UpdateRequest{APIKey: &key})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.KeySet).To(BeTrue())
			Expect(e.unloader.seen()).To(ConsistOf("alpha", "beta"))
			resolved, err := e.svc.ResolveKey(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved).To(Equal(key))
			Expect(e.read("alpha")).NotTo(ContainSubstring(key))
		})
		It("does not stop entries when only the name changes", func() {
			c := create(chat("alpha")...).Connection
			name := "Else"
			_, err := e.svc.Update(ctx, c.ID, connections.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			Expect(e.unloader.seen()).To(BeEmpty())
		})
		It("removes the key with an empty value, except for anthropic", func() {
			c := create().Connection
			empty := ""
			got, err := e.svc.Update(ctx, c.ID, connections.UpdateRequest{APIKey: &empty})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.KeySet).To(BeFalse())
			key, err := e.svc.ResolveKey(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(BeEmpty())
			an, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindAnthropic, Name: "Claude", APIKey: "k"})
			Expect(err).NotTo(HaveOccurred())
			_, err = e.svc.Update(ctx, an.Connection.ID, connections.UpdateRequest{APIKey: &empty})
			Expect(errors.Is(err, connections.ErrInvalid)).To(BeTrue())
		})
		It("patches proxy.upstream_url on the entries when the address changes", func() {
			c := create(chat("alpha", "beta")...).Connection
			// A person pointed beta somewhere else on purpose.
			custom := e.read("beta")
			custom = replaceOnce(custom, remote.URL, "https://elsewhere.example.com")
			Expect(os.WriteFile(e.file("beta"), []byte(custom), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())

			newURL := "https://new.example.com/v1"
			got, err := e.svc.Update(ctx, c.ID, connections.UpdateRequest{URL: &newURL})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.URL).To(Equal(newURL))
			Expect(e.config("alpha").Proxy.UpstreamURL).To(Equal("https://new.example.com"))
			Expect(e.config("beta").Proxy.UpstreamURL).To(Equal("https://elsewhere.example.com"))
			Expect(e.config("alpha").Proxy.Connection).To(Equal(c.ID))
			Expect(e.bus.ops("alpha")).To(Equal([]string{"install", "install"}))
		})
		It("refuses a bad address", func() {
			c := create().Connection
			bad := "not a url"
			_, err := e.svc.Update(ctx, c.ID, connections.UpdateRequest{URL: &bad})
			Expect(errors.Is(err, connections.ErrInvalid)).To(BeTrue())
		})
	})

	Describe("Delete", func() {
		It("removes the entries and the connection", func() {
			c := create(chat("alpha", "beta")...).Connection
			Expect(e.svc.Delete(ctx, c.ID, true)).To(Succeed())
			Expect(e.file("alpha")).NotTo(BeAnExistingFile())
			Expect(e.file("beta")).NotTo(BeAnExistingFile())
			_, ok := e.loader.GetModelConfig("alpha")
			Expect(ok).To(BeFalse())
			_, err := e.svc.Get(ctx, c.ID)
			Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
			Expect(e.bus.ops("alpha")).To(Equal([]string{"install", "delete"}))
			Expect(e.unloader.seen()).To(ConsistOf("alpha", "beta"))
			_, err = e.svc.ResolveKey(ctx, c.ID)
			Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
		})
		It("detaches the entries without copying the key", func() {
			c := create(chat("alpha")...).Connection
			Expect(e.svc.Delete(ctx, c.ID, false)).To(Succeed())
			cfg := e.config("alpha")
			Expect(cfg.Proxy.Connection).To(BeEmpty())
			Expect(cfg.Proxy.UpstreamURL).To(Equal(remote.URL))
			Expect(cfg.Proxy.UpstreamModel).To(Equal("alpha"))
			Expect(cfg.Backend).To(Equal("localai-proxy"))
			body := e.read("alpha")
			Expect(body).NotTo(ContainSubstring(secret))
			Expect(body).NotTo(ContainSubstring("api_key"))
			Expect(body).NotTo(ContainSubstring("connection"))
			_, err := e.svc.Get(ctx, c.ID)
			Expect(errors.Is(err, connections.ErrNotFound)).To(BeTrue())
		})
		It("leaves a model alone that a person detached", func() {
			c := create(chat("alpha")...).Connection
			detached := replaceOnce(e.read("alpha"), "connection: "+c.ID, "description: mine")
			Expect(os.WriteFile(e.file("alpha"), []byte(detached), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			Expect(e.svc.Delete(ctx, c.ID, true)).To(Succeed())
			Expect(e.file("alpha")).To(BeAnExistingFile())
		})
		It("reports an unknown connection", func() {
			Expect(errors.Is(e.svc.Delete(ctx, "missing", true), connections.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("the key", func() {
		It("is sealed on disk and never appears in a view or an error", func() {
			dir := filepath.Join(GinkgoT().TempDir(), "connections")
			store, err := connections.NewJSONStore(dir)
			Expect(err).NotTo(HaveOccurred())
			e2 := newEnv(store)
			res, err := e2.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret, Models: chat("alpha")})
			Expect(err).NotTo(HaveOccurred())

			var seen []string
			addJSON := func(v any) {
				raw, err := json.Marshal(v)
				Expect(err).NotTo(HaveOccurred())
				seen = append(seen, string(raw))
			}
			addJSON(res)
			list, _ := e2.svc.List(ctx)
			addJSON(list)
			got, _ := e2.svc.Get(ctx, res.Connection.ID)
			addJSON(got)
			remote.fail(http.StatusUnauthorized)
			got, _ = e2.svc.Refresh(ctx, res.Connection.ID)
			addJSON(got)
			_, err = e2.svc.Adopt(ctx, "missing", chat("x"))
			seen = append(seen, err.Error())
			tr, _ := e2.svc.Test(ctx, connections.KindLocalAI, remote.URL, secret)
			addJSON(tr)
			_, err = e2.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "home", URL: remote.URL, APIKey: secret})
			seen = append(seen, err.Error())
			bad := "nope"
			_, err = e2.svc.Update(ctx, res.Connection.ID, connections.UpdateRequest{URL: &bad})
			seen = append(seen, err.Error())
			for _, s := range seen {
				Expect(s).NotTo(ContainSubstring("SECRET"))
			}

			store2, err := os.ReadFile(filepath.Join(dir, connections.JSONFileName))
			Expect(err).NotTo(HaveOccurred())
			keyFile, err := os.ReadFile(filepath.Join(dir, connections.JSONKeyName))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(store2)).NotTo(ContainSubstring("SECRET"))
			Expect(string(store2)).NotTo(ContainSubstring(secret))
			Expect(string(keyFile)).NotTo(ContainSubstring("SECRET"))
			Expect(len(keyFile)).To(Equal(32))
		})
		It("does not open when moved to another connection", func() {
			store := memStore()
			e2 := newEnv(store)
			a, err := e2.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "A", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			b, err := e2.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "B", URL: remote.URL})
			Expect(err).NotTo(HaveOccurred())
			recA, _ := store.Get(ctx, a.Connection.ID)
			_, err = store.Update(ctx, b.Connection.ID, func(r *connections.Record) error { r.Sealed = recA.Sealed; return nil })
			Expect(err).NotTo(HaveOccurred())
			_, err = e2.svc.ResolveKey(ctx, b.Connection.ID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		})
	})

	Describe("the model load hook", func() {
		AfterEach(func() { config.SetProxyKeyResolver(nil) })
		It("serves the key to the proxy key resolver", func() {
			c := create().Connection
			config.SetProxyKeyResolver(e.svc)
			Expect(config.ResolveProxyConnectionKey(ctx, c.ID)).To(Equal(secret))
			_, err := config.ResolveProxyConnectionKey(ctx, "missing")
			Expect(err).To(MatchError(ContainSubstring("key unavailable")))
		})
	})

	Describe("two frontends on one store", func() {
		It("sees what the other wrote, including the key", func() {
			store := memStore()
			a, b := newEnv(store), newEnv(store)
			res, err := a.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			got, err := b.svc.Get(ctx, res.Connection.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("Home"))
			key, err := b.svc.ResolveKey(ctx, res.Connection.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(secret))
			name := "Renamed"
			_, err = b.svc.Update(ctx, res.Connection.ID, connections.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			got, _ = a.svc.Get(ctx, res.Connection.ID)
			Expect(got.Name).To(Equal("Renamed"))
		})
		It("shares one sealing key through a database", func() {
			db := openSQLite()
			s1, err := connections.NewGORMStore(db)
			Expect(err).NotTo(HaveOccurred())
			s2, err := connections.NewGORMStore(db)
			Expect(err).NotTo(HaveOccurred())
			a, b := newEnv(s1), newEnv(s2)
			res, err := a.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			key, err := b.svc.ResolveKey(ctx, res.Connection.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(secret))
		})
	})

	Describe("user edits", func() {
		It("keeps the edit flag right when the service patches the address", func() {
			store := memStore()
			e2 := newEnv(store)
			c, err := e2.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret, Models: chat("alpha", "beta")})
			Expect(err).NotTo(HaveOccurred())
			// beta is edited by a person: a field the service does not own.
			edited := e2.read("beta") + "description: mine\n"
			Expect(os.WriteFile(e2.file("beta"), []byte(edited), 0o644)).To(Succeed())
			Expect(e2.loader.LoadModelConfigsFromPath(e2.dir)).To(Succeed())
			newURL := "https://moved.example.com"
			_, err = e2.svc.Update(ctx, c.Connection.ID, connections.UpdateRequest{URL: &newURL})
			Expect(err).NotTo(HaveOccurred())
			rec, _ := store.Get(ctx, c.Connection.ID)
			byName := map[string]connections.OwnedEntry{}
			for _, o := range rec.Owned {
				byName[o.Name] = o
			}
			Expect(byName["alpha"].Hash).To(Equal(connections.HashBytes([]byte(e2.read("alpha")))), "an entry the service alone changed is still its own")
			Expect(byName["beta"].Hash).NotTo(Equal(connections.HashBytes([]byte(e2.read("beta")))), "an edited entry stays flagged as edited")
			Expect(e2.read("beta")).To(ContainSubstring("description: mine"))
			Expect(e2.config("beta").Proxy.UpstreamURL).To(Equal("https://moved.example.com"))
		})
	})
})

var _ = Describe("startup refresh", func() {
	ctx := context.Background()

	It("refreshes every connection and records a failing remote", func() {
		good, bad := newFakeRemote("a"), newFakeRemote("b")
		defer good.Close()
		defer bad.Close()
		bad.fail(http.StatusBadGateway)
		e := newEnv(memStore())
		g, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Good", URL: good.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		b, err := e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Bad", URL: bad.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		good.set("a", "c")
		before := g.Connection.LastRefresh
		time.Sleep(5 * time.Millisecond)
		connections.StartupRefresh(ctx, e.svc, nil)
		got, _ := e.svc.Get(ctx, g.Connection.ID)
		Expect(ids(got.New)).To(Equal([]string{"a", "c"}))
		Expect(got.LastRefresh.After(*before)).To(BeTrue())
		gotBad, _ := e.svc.Get(ctx, b.Connection.ID)
		Expect(gotBad.Status).To(Equal(connections.StatusUnreachable))
	})

	It("runs on one frontend at a time when there is a database", func() {
		db := openSQLite()
		remote := newFakeRemote("a")
		defer remote.Close()
		store, err := connections.NewGORMStore(db)
		Expect(err).NotTo(HaveOccurred())
		e := newEnv(store)
		_, err = e.svc.Create(ctx, connections.CreateRequest{Kind: connections.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())

		// The second frontend arrives while the first one is still inside its
		// pass, so it must do nothing.
		entered, release := make(chan struct{}), make(chan struct{})
		slow := &blockingService{Service: e.svc, entered: entered, release: release}
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			connections.StartupRefresh(ctx, slow, db)
		}()
		Eventually(entered).Should(BeClosed())
		counting := &countingService{Service: e.svc}
		connections.StartupRefresh(ctx, counting, db)
		Expect(counting.refreshes()).To(BeZero(), "the second frontend found the lock taken")
		close(release)
		Eventually(done).Should(BeClosed())

		counting2 := &countingService{Service: e.svc}
		connections.StartupRefresh(ctx, counting2, db)
		Expect(counting2.refreshes()).To(Equal(1), "the lock is free again after the pass")
	})
})

type blockingService struct {
	connections.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingService) Refresh(ctx context.Context, id string) (connections.Connection, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.Service.Refresh(ctx, id)
}

type countingService struct {
	connections.Service
	mu sync.Mutex
	n  int
}

func (c *countingService) Refresh(ctx context.Context, id string) (connections.Connection, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.Service.Refresh(ctx, id)
}

func (c *countingService) refreshes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func replaceOnce(s, old, repl string) string {
	i := indexOf(s, old)
	Expect(i).To(BeNumerically(">=", 0), "%q not in %q", old, s)
	return s[:i] + repl + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
