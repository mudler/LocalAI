package remotes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/remotes"
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
	svc      remotes.Service
	unloader *fakeUnloader
	bus      *fakeBroadcaster
}

func newEnv(store remotes.Store) *env { return newEnvWith(store, nil) }

// newEnvWith is newEnv with the model admin seen by the service wrapped,
// so a spec can make it fail on demand.
func newEnvWith(store remotes.Store, wrap func(remotes.ModelAdmin) remotes.ModelAdmin) *env {
	dir := GinkgoT().TempDir()
	loader := config.NewModelConfigLoader(dir)
	app := &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
	e := &env{dir: dir, loader: loader, admin: modeladmin.NewConfigService(loader, app), unloader: &fakeUnloader{}, bus: &fakeBroadcaster{}}
	var admin remotes.ModelAdmin = e.admin
	if wrap != nil {
		admin = wrap(admin)
	}
	svc, err := remotes.New(remotes.Options{
		Store: store, Admin: admin, Configs: loader, Unloader: e.unloader, Broadcaster: e.bus, ModelsPath: dir,
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

func chat(ids ...string) []remotes.UpstreamModel {
	var out []remotes.UpstreamModel
	for _, id := range ids {
		out = append(out, remotes.UpstreamModel{ID: id, Usecases: []string{"chat"}})
	}
	return out
}

func memStore() remotes.Store {
	s, err := remotes.NewJSONStore("")
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

	create := func(models ...remotes.UpstreamModel) remotes.Result {
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret, Models: models})
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	Describe("Test", func() {
		It("lists models and does not save anything", func() {
			res, err := e.svc.Test(ctx, remotes.KindLocalAI, remote.URL, secret)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.OK).To(BeTrue())
			Expect(ids(res.Models)).To(Equal([]string{"alpha", "beta", "gamma"}))
			all, _ := e.svc.List(ctx)
			Expect(all).To(BeEmpty())
		})
		It("returns a failed test as a result, not an error", func() {
			res, err := e.svc.Test(ctx, remotes.KindLocalAI, remote.URL, "wrong")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.OK).To(BeFalse())
			Expect(res.Step).To(Equal(remotes.StepAuth))
			res, err = e.svc.Test(ctx, remotes.KindLocalAI, "nonsense", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Step).To(Equal(remotes.StepInvalidURL))
			remote.set()
			res, _ = e.svc.Test(ctx, remotes.KindLocalAI, remote.URL, secret)
			Expect(res.Step).To(Equal(remotes.StepNoModels))
		})
		It("rejects an unknown kind", func() {
			_, err := e.svc.Test(ctx, "ftp", remote.URL, "")
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		})
	})

	Describe("Create", func() {
		It("writes entries that the loader sees at once, with no key in them", func() {
			res := create(chat("alpha", "beta")...)
			Expect(res.Created).To(ConsistOf("alpha", "beta"))
			Expect(res.Skipped).To(BeEmpty())
			c := res.Remote
			Expect(c.Entries).To(ConsistOf("alpha", "beta"))
			Expect(c.KeySet).To(BeTrue())
			Expect(c.Status).To(Equal(remotes.StatusOK))
			Expect(c.LastRefresh).NotTo(BeNil())
			Expect(ids(c.New)).To(Equal([]string{"gamma"}))
			Expect(c.Unavailable).To(BeEmpty())

			cfg := e.config("alpha")
			Expect(cfg.Backend).To(Equal("localai-proxy"))
			Expect(cfg.Proxy.Remote).To(Equal(c.ID))
			Expect(cfg.Proxy.UpstreamModel).To(Equal("alpha"))
			Expect(cfg.Proxy.UpstreamURL).To(Equal(remote.URL))
			Expect(cfg.Proxy.APIKey + cfg.Proxy.APIKeyEnv + cfg.Proxy.APIKeyFile).To(BeEmpty())
			Expect(e.read("alpha")).NotTo(ContainSubstring(secret))
			Expect(e.bus.ops("alpha")).To(Equal([]string{"install"}))

			key, err := resolveKey(ctx, e.svc, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(secret))
		})
		It("with no models lists them as new and writes no entry", func() {
			res := create()
			Expect(res.Created).To(BeEmpty())
			Expect(ids(res.Remote.New)).To(Equal([]string{"alpha", "beta", "gamma"}))
			Expect(res.Remote.Entries).To(BeEmpty())
		})
		It("records a failing remote instead of failing", func() {
			res, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Bad", URL: remote.URL, APIKey: "wrong"})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Remote.Status).To(Equal(remotes.StatusAuthFailed))
			Expect(res.Remote.LastError).NotTo(BeEmpty())
		})
		It("refuses input that is not valid with a message", func() {
			for name, r := range map[string]remotes.CreateRequest{
				"no name":       {Kind: remotes.KindLocalAI, URL: remote.URL},
				"bad kind":      {Kind: "x", Name: "n", URL: remote.URL},
				"bad url":       {Kind: remotes.KindLocalAI, Name: "n", URL: "remote"},
				"no url":        {Kind: remotes.KindOpenAI, Name: "n"},
				"anthropic key": {Kind: remotes.KindAnthropic, Name: "n"},
				"multiline key": {Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: "a\nb"},
				"credentials":   {Kind: remotes.KindLocalAI, Name: "n", URL: "https://u:p@remote.example.com"},
			} {
				_, err := e.svc.Create(ctx, r)
				Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue(), name)
				Expect(err.Error()).NotTo(BeEmpty(), name)
			}
		})
		It("refuses a name that is taken", func() {
			create()
			_, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "home", URL: remote.URL, APIKey: secret})
			Expect(errors.Is(err, remotes.ErrConflict)).To(BeTrue())
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
			oa, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindOpenAI, Name: "OpenAI", URL: srv.URL + "/v1", APIKey: "k", Models: []remotes.UpstreamModel{{ID: "gpt-x"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(oa.Created).To(Equal([]string{"gpt-x"}))
			Expect(e.config("gpt-x").Proxy.UpstreamURL).To(Equal(srv.URL + "/v1/chat/completions"))
			Expect(oa.Remote.Status).To(Equal(remotes.StatusOK))
			an, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindAnthropic, Name: "Claude", URL: srv.URL, APIKey: "k", Models: []remotes.UpstreamModel{{ID: "claude-x"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(an.Created).To(Equal([]string{"claude-x"}))
			cfg := e.config("claude-x")
			Expect(cfg.Proxy.Provider).To(Equal("anthropic"))
			Expect(cfg.Proxy.UpstreamURL).To(Equal(srv.URL + "/v1/messages"))
		})
	})

	Describe("Adopt", func() {
		It("adds entries, removes them from new and skips what is adopted", func() {
			c := create(chat("alpha")...).Remote
			res, err := e.svc.Adopt(ctx, c.ID, chat("alpha", "beta"))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"beta"}))
			Expect(res.Skipped).To(Equal([]remotes.Skip{{ID: "alpha", Reason: "already adopted"}}))
			Expect(res.Remote.Entries).To(ConsistOf("alpha", "beta"))
			Expect(ids(res.Remote.New)).To(Equal([]string{"gamma"}))
		})
		It("prefixes the name when a model of that name exists, and skips when both are taken", func() {
			Expect(os.WriteFile(e.file("beta"), []byte("name: beta\nbackend: llama-cpp\nparameters:\n  model: x.gguf\n"), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			c := create().Remote
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
		It("does not take a name that another remote owns", func() {
			other := newFakeRemote("alpha")
			defer other.Close()
			first := create(chat("alpha")...)
			Expect(first.Created).To(Equal([]string{"alpha"}))
			res, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Work", URL: other.URL, APIKey: secret, Models: chat("alpha")})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"work-alpha"}))
		})
		It("sanitises ids that hold slashes and colons", func() {
			c := create().Remote
			res, err := e.svc.Adopt(ctx, c.ID, chat("org/model:8b"))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Created).To(Equal([]string{"org-model-8b"}))
			Expect(e.config("org-model-8b").Proxy.UpstreamModel).To(Equal("org/model:8b"))
		})
		It("needs at least one model and a known remote", func() {
			c := create().Remote
			_, err := e.svc.Adopt(ctx, c.ID, nil)
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
			_, err = e.svc.Adopt(ctx, "missing", chat("a"))
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("Refresh", func() {
		It("flags new and removed models and never writes an entry file", func() {
			c := create(chat("alpha", "beta")...).Remote
			beforeA, beforeB := e.read("alpha"), e.read("beta")
			infoA, _ := os.Stat(e.file("alpha"))
			remote.set("beta", "delta")
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(remotes.StatusOK))
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
			c := create(chat("alpha")...).Remote
			edited := e.read("alpha") + "description: my own words\n"
			Expect(os.WriteFile(e.file("alpha"), []byte(edited), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			remote.set("alpha", "zeta")
			_, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(e.read("alpha")).To(Equal(edited))
		})
		It("records auth and unreachable failures and keeps the earlier list", func() {
			c := create(chat("alpha")...).Remote
			remote.fail(http.StatusUnauthorized)
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(remotes.StatusAuthFailed))
			Expect(got.LastError).NotTo(BeEmpty())
			Expect(got.LastError).NotTo(ContainSubstring("SECRET"))
			Expect(ids(got.New)).To(Equal([]string{"beta", "gamma"}))
			remote.fail(http.StatusBadGateway)
			got, _ = e.svc.Refresh(ctx, c.ID)
			Expect(got.Status).To(Equal(remotes.StatusUnreachable))
			remote.fail(0)
			got, _ = e.svc.Refresh(ctx, c.ID)
			Expect(got.Status).To(Equal(remotes.StatusOK))
			Expect(got.LastError).To(BeEmpty())
		})
		It("records a remote that lists nothing usable and shows every entry as unavailable", func() {
			c := create(chat("alpha")...).Remote
			remote.set()
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(remotes.StatusOK))
			Expect(got.Unavailable).To(Equal([]string{"alpha"}))
			Expect(got.LastError).NotTo(BeEmpty())
		})
		It("forgets an entry whose model config was deleted, so the model is new again", func() {
			c := create(chat("alpha")...).Remote
			_, err := e.admin.DeleteConfig(ctx, "alpha")
			Expect(err).NotTo(HaveOccurred())
			got, err := e.svc.Refresh(ctx, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Entries).To(BeEmpty())
			Expect(ids(got.New)).To(ContainElement("alpha"))
		})
		It("reports an unknown remote", func() {
			_, err := e.svc.Refresh(ctx, "missing")
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("Update", func() {
		It("renames and refuses a name that is taken", func() {
			c := create().Remote
			other, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Other", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			name := "Renamed"
			got, err := e.svc.Update(ctx, c.ID, remotes.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("Renamed"))
			taken := "other"
			_, err = e.svc.Update(ctx, c.ID, remotes.UpdateRequest{Name: &taken})
			Expect(errors.Is(err, remotes.ErrConflict)).To(BeTrue())
			Expect(other.Remote.Name).To(Equal("Other"))
		})
		It("rotates the key: the new key resolves and loaded entries are stopped", func() {
			c := create(chat("alpha", "beta")...).Remote
			key := "sk-NEW-key"
			got, err := e.svc.Update(ctx, c.ID, remotes.UpdateRequest{APIKey: &key})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.KeySet).To(BeTrue())
			Expect(e.unloader.seen()).To(ConsistOf("alpha", "beta"))
			resolved, err := resolveKey(ctx, e.svc, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved).To(Equal(key))
			Expect(e.read("alpha")).NotTo(ContainSubstring(key))
		})
		It("does not stop entries when only the name changes", func() {
			c := create(chat("alpha")...).Remote
			name := "Else"
			_, err := e.svc.Update(ctx, c.ID, remotes.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			Expect(e.unloader.seen()).To(BeEmpty())
		})
		It("removes the key with an empty value, except for anthropic", func() {
			c := create().Remote
			empty := ""
			got, err := e.svc.Update(ctx, c.ID, remotes.UpdateRequest{APIKey: &empty})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.KeySet).To(BeFalse())
			key, err := resolveKey(ctx, e.svc, c.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(BeEmpty())
			an, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindAnthropic, Name: "Claude", APIKey: "k"})
			Expect(err).NotTo(HaveOccurred())
			_, err = e.svc.Update(ctx, an.Remote.ID, remotes.UpdateRequest{APIKey: &empty})
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		})
		It("patches proxy.upstream_url on the entries when the address changes", func() {
			c := create(chat("alpha", "beta")...).Remote
			// A person pointed beta somewhere else on purpose.
			custom := e.read("beta")
			custom = replaceOnce(custom, remote.URL, "https://elsewhere.example.com")
			Expect(os.WriteFile(e.file("beta"), []byte(custom), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())

			newURL := "https://new.example.com/v1"
			got, err := e.svc.Update(ctx, c.ID, remotes.UpdateRequest{URL: &newURL})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.URL).To(Equal(newURL))
			Expect(e.config("alpha").Proxy.UpstreamURL).To(Equal("https://new.example.com"))
			Expect(e.config("beta").Proxy.UpstreamURL).To(Equal("https://elsewhere.example.com"))
			Expect(e.config("alpha").Proxy.Remote).To(Equal(c.ID))
			Expect(e.bus.ops("alpha")).To(Equal([]string{"install", "install"}))
		})
		It("refuses a bad address", func() {
			c := create().Remote
			bad := "not a url"
			_, err := e.svc.Update(ctx, c.ID, remotes.UpdateRequest{URL: &bad})
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		})
	})

	Describe("Delete", func() {
		It("removes the entries and the remote", func() {
			c := create(chat("alpha", "beta")...).Remote
			Expect(e.svc.Delete(ctx, c.ID, true)).To(Succeed())
			Expect(e.file("alpha")).NotTo(BeAnExistingFile())
			Expect(e.file("beta")).NotTo(BeAnExistingFile())
			_, ok := e.loader.GetModelConfig("alpha")
			Expect(ok).To(BeFalse())
			_, err := e.svc.Get(ctx, c.ID)
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
			Expect(e.bus.ops("alpha")).To(Equal([]string{"install", "delete"}))
			Expect(e.unloader.seen()).To(ConsistOf("alpha", "beta"))
			_, err = resolveKey(ctx, e.svc, c.ID)
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		})
		It("detaches the entries without copying the key", func() {
			c := create(chat("alpha")...).Remote
			Expect(e.svc.Delete(ctx, c.ID, false)).To(Succeed())
			cfg := e.config("alpha")
			Expect(cfg.Proxy.Remote).To(BeEmpty())
			Expect(cfg.Proxy.UpstreamURL).To(Equal(remote.URL))
			Expect(cfg.Proxy.UpstreamModel).To(Equal("alpha"))
			Expect(cfg.Backend).To(Equal("localai-proxy"))
			body := e.read("alpha")
			Expect(body).NotTo(ContainSubstring(secret))
			Expect(body).NotTo(ContainSubstring("api_key"))
			// The patch leaves an empty value, which the loader reads as
			// no remote.
			Expect(body).To(ContainSubstring(`remote: ""`))
			Expect(body).NotTo(ContainSubstring(c.ID))
			_, err := e.svc.Get(ctx, c.ID)
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		})
		It("leaves a model alone that a person detached", func() {
			c := create(chat("alpha")...).Remote
			detached := replaceOnce(e.read("alpha"), "remote: "+c.ID, "description: mine")
			Expect(os.WriteFile(e.file("alpha"), []byte(detached), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			Expect(e.svc.Delete(ctx, c.ID, true)).To(Succeed())
			Expect(e.file("alpha")).To(BeAnExistingFile())
		})
		It("reports an unknown remote", func() {
			Expect(errors.Is(e.svc.Delete(ctx, "missing", true), remotes.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("the key", func() {
		It("is sealed on disk and never appears in a view or an error", func() {
			dir := filepath.Join(GinkgoT().TempDir(), "remotes")
			store, err := remotes.NewJSONStore(dir)
			Expect(err).NotTo(HaveOccurred())
			e2 := newEnv(store)
			res, err := e2.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret, Models: chat("alpha")})
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
			got, _ := e2.svc.Get(ctx, res.Remote.ID)
			addJSON(got)
			remote.fail(http.StatusUnauthorized)
			got, _ = e2.svc.Refresh(ctx, res.Remote.ID)
			addJSON(got)
			_, err = e2.svc.Adopt(ctx, "missing", chat("x"))
			seen = append(seen, err.Error())
			tr, _ := e2.svc.Test(ctx, remotes.KindLocalAI, remote.URL, secret)
			addJSON(tr)
			_, err = e2.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "home", URL: remote.URL, APIKey: secret})
			seen = append(seen, err.Error())
			bad := "nope"
			_, err = e2.svc.Update(ctx, res.Remote.ID, remotes.UpdateRequest{URL: &bad})
			seen = append(seen, err.Error())
			for _, s := range seen {
				Expect(s).NotTo(ContainSubstring("SECRET"))
			}

			store2, err := os.ReadFile(filepath.Join(dir, remotes.JSONFileName))
			Expect(err).NotTo(HaveOccurred())
			keyFile, err := os.ReadFile(filepath.Join(dir, remotes.JSONKeyName))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(store2)).NotTo(ContainSubstring("SECRET"))
			Expect(string(store2)).NotTo(ContainSubstring(secret))
			Expect(string(keyFile)).NotTo(ContainSubstring("SECRET"))
			Expect(len(keyFile)).To(Equal(32))
		})
		It("does not open when moved to another remote", func() {
			store := memStore()
			e2 := newEnv(store)
			a, err := e2.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "A", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			b, err := e2.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "B", URL: remote.URL})
			Expect(err).NotTo(HaveOccurred())
			recA, _ := store.Get(ctx, a.Remote.ID)
			_, err = store.Update(ctx, b.Remote.ID, func(r *remotes.Record) error { r.Sealed = recA.Sealed; return nil })
			Expect(err).NotTo(HaveOccurred())
			_, err = resolveKey(ctx, e2.svc, b.Remote.ID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		})
	})

	Describe("the model load hook", func() {
		AfterEach(func() { config.SetProxyKeyResolver(nil) })
		It("serves the key to the proxy key resolver", func() {
			c := create().Remote
			config.SetProxyKeyResolver(e.svc)
			Expect(config.ResolveProxyRemoteKey(ctx, c.ID, remote.URL)).To(Equal(secret))
			_, err := config.ResolveProxyRemoteKey(ctx, "missing", remote.URL)
			Expect(err).To(MatchError(ContainSubstring("key unavailable")))
		})
		It("gives the key to every generated entry and to no other address", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-api-key") != "" {
					writeJSON(w, map[string]any{"data": []map[string]any{{"id": "claude-x"}}, "has_more": false})
					return
				}
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": "gpt-x"}}})
			}))
			defer srv.Close()
			local := create(chat("alpha")...)
			oa, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindOpenAI, Name: "OpenAI", URL: srv.URL + "/v1/", APIKey: "oa-key", Models: []remotes.UpstreamModel{{ID: "gpt-x"}}})
			Expect(err).NotTo(HaveOccurred())
			an, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindAnthropic, Name: "Claude", URL: srv.URL, APIKey: "an-key", Models: []remotes.UpstreamModel{{ID: "claude-x"}}})
			Expect(err).NotTo(HaveOccurred())
			config.SetProxyKeyResolver(e.svc)
			for entry, want := range map[string]string{"alpha": secret, "gpt-x": "oa-key", "claude-x": "an-key"} {
				cfg := e.config(entry)
				Expect(config.ResolveProxyRemoteKey(ctx, cfg.Proxy.Remote, cfg.Proxy.UpstreamURL)).To(Equal(want), entry)
			}
			// The probe of the security review: an entry that names a remote
			// but points at another server gets no key.
			for _, id := range []string{local.Remote.ID, oa.Remote.ID, an.Remote.ID} {
				key, err := config.ResolveProxyRemoteKey(ctx, id, "https://attacker.example.net/v1/chat/completions")
				Expect(err).To(HaveOccurred())
				Expect(key).To(BeEmpty())
				Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
			}
			// The same host on another port is another server.
			other := replaceOnce(srv.URL, "127.0.0.1:", "127.0.0.1:1")
			_, err = config.ResolveProxyRemoteKey(ctx, oa.Remote.ID, other+"/v1/chat/completions")
			Expect(err).To(HaveOccurred())
		})
		It("binds every generated address to the base the key resolves with", func() {
			for _, c := range []struct {
				kind remotes.Kind
				url  string
			}{
				{remotes.KindAnthropic, ""},
				{remotes.KindAnthropic, "https://gw.example.com/anthropic/v1/"},
				{remotes.KindOpenAI, "https://api.example.com:443/v1/"},
				{remotes.KindLocalAI, "http://10.0.0.2:8080/ai/v1"},
			} {
				entry, err := remotes.EntryUpstreamURL(c.kind, c.url)
				Expect(err).NotTo(HaveOccurred())
				base, err := remotes.TargetBase(c.kind, c.url)
				Expect(err).NotTo(HaveOccurred())
				Expect(config.UpstreamWithinBase(entry, base)).To(BeTrue(), "%s %s", c.kind, c.url)
			}
			base, _ := remotes.TargetBase(remotes.KindAnthropic, "")
			Expect(base).To(Equal("https://api.anthropic.com"))
		})
	})

	Describe("two frontends on one store", func() {
		It("sees what the other wrote, including the key", func() {
			store := memStore()
			a, b := newEnv(store), newEnv(store)
			res, err := a.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			got, err := b.svc.Get(ctx, res.Remote.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("Home"))
			key, err := resolveKey(ctx, b.svc, res.Remote.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(secret))
			name := "Renamed"
			_, err = b.svc.Update(ctx, res.Remote.ID, remotes.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			got, _ = a.svc.Get(ctx, res.Remote.ID)
			Expect(got.Name).To(Equal("Renamed"))
		})
		It("shares one sealing key through a database", func() {
			db := openSQLite()
			s1, err := remotes.NewGORMStore(db)
			Expect(err).NotTo(HaveOccurred())
			s2, err := remotes.NewGORMStore(db)
			Expect(err).NotTo(HaveOccurred())
			a, b := newEnv(s1), newEnv(s2)
			res, err := a.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			key, err := resolveKey(ctx, b.svc, res.Remote.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(secret))
		})
	})

	Describe("user edits", func() {
		It("keeps the edit flag right when the service patches the address", func() {
			store := memStore()
			e2 := newEnv(store)
			c, err := e2.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret, Models: chat("alpha", "beta")})
			Expect(err).NotTo(HaveOccurred())
			// beta is edited by a person: a field the service does not own.
			edited := e2.read("beta") + "description: mine\n"
			Expect(os.WriteFile(e2.file("beta"), []byte(edited), 0o644)).To(Succeed())
			Expect(e2.loader.LoadModelConfigsFromPath(e2.dir)).To(Succeed())
			newURL := "https://moved.example.com"
			_, err = e2.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{URL: &newURL})
			Expect(err).NotTo(HaveOccurred())
			rec, _ := store.Get(ctx, c.Remote.ID)
			byName := map[string]remotes.OwnedEntry{}
			for _, o := range rec.Owned {
				byName[o.Name] = o
			}
			Expect(byName["alpha"].Hash).To(Equal(remotes.HashBytes([]byte(e2.read("alpha")))), "an entry the service alone changed is still its own")
			Expect(byName["beta"].Hash).NotTo(Equal(remotes.HashBytes([]byte(e2.read("beta")))), "an edited entry stays flagged as edited")
			Expect(e2.read("beta")).To(ContainSubstring("description: mine"))
			Expect(e2.config("beta").Proxy.UpstreamURL).To(Equal("https://moved.example.com"))
		})
	})
})

// flakyAdmin fails the calls whose model name is in failFor.
type flakyAdmin struct {
	remotes.ModelAdmin
	mu      sync.Mutex
	failFor map[string]bool
}

var errInjected = errors.New("injected failure")

func (f *flakyAdmin) fails(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failFor[name]
}

func (f *flakyAdmin) set(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failFor = map[string]bool{}
	for _, n := range names {
		f.failFor[n] = true
	}
}

func (f *flakyAdmin) PatchConfig(ctx context.Context, name string, patch map[string]any) (*modeladmin.PatchResult, error) {
	if f.fails(name) {
		return nil, errInjected
	}
	return f.ModelAdmin.PatchConfig(ctx, name, patch)
}

func (f *flakyAdmin) DeleteConfig(ctx context.Context, name string) (*modeladmin.DeleteResult, error) {
	if f.fails(name) {
		return nil, errInjected
	}
	return f.ModelAdmin.DeleteConfig(ctx, name)
}

// flakyStore fails Update after failAfter successful calls when armed.
type flakyStore struct {
	remotes.Store
	mu        sync.Mutex
	armed     bool
	failAfter int
}

func (f *flakyStore) arm(after int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed, f.failAfter = true, after
}

func (f *flakyStore) Update(ctx context.Context, id string, fn func(*remotes.Record) error) (remotes.Record, error) {
	f.mu.Lock()
	fail := f.armed && f.failAfter == 0
	if f.armed && f.failAfter > 0 {
		f.failAfter--
	}
	f.mu.Unlock()
	if fail {
		return remotes.Record{}, errInjected
	}
	return f.Store.Update(ctx, id, fn)
}

var _ = Describe("Service hardening", func() {
	var (
		ctx    = context.Background()
		remote *fakeRemote
	)
	BeforeEach(func() {
		remote = newFakeRemote("alpha", "beta", "gamma")
		DeferCleanup(remote.Close)
	})

	It("refuses a key with characters an HTTP header cannot carry", func() {
		e := newEnv(memStore())
		for _, k := range []string{"a\x7fb", "a b", "ключ", "a\tb"} {
			_, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: k})
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue(), "%q", k)
			Expect(err.Error()).To(ContainSubstring("printable ASCII"))
		}
		_, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: " " + secret + " "})
		Expect(err).NotTo(HaveOccurred(), "spaces around the key are trimmed")
	})

	It("refuses to adopt more than the cap in one call", func() {
		e := newEnv(memStore())
		many := make([]remotes.UpstreamModel, remotes.MaxAdoptModels+1)
		for i := range many {
			many[i] = remotes.UpstreamModel{ID: fmt.Sprintf("m%04d", i)}
		}
		_, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret, Models: many})
		Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		_, err = e.svc.Adopt(ctx, res.Remote.ID, many)
		Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("500"))
	})

	It("cuts a listing above the cap and says so in the test result", func() {
		e := newEnv(memStore())
		ids := make([]string, remotes.MaxListedModels+5)
		for i := range ids {
			ids[i] = fmt.Sprintf("m%05d", i)
		}
		remote.set(ids...)
		res, err := e.svc.Test(ctx, remotes.KindLocalAI, remote.URL, secret)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.OK).To(BeTrue())
		Expect(res.Models).To(HaveLen(remotes.MaxListedModels))
		Expect(res.Models[0].ID).To(Equal("m00000"))
		Expect(res.Message).To(ContainSubstring("2005"))
		c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Remote.New).To(HaveLen(remotes.MaxListedModels))
		Expect(c.Remote.LastError).To(BeEmpty())
	})

	Describe("Update", func() {
		It("refreshes when the key changes, so the status is current", func() {
			e := newEnv(memStore())
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: "wrong"})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Remote.Status).To(Equal(remotes.StatusAuthFailed))
			key := secret
			got, err := e.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{APIKey: &key})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(remotes.StatusOK))
			Expect(got.LastError).To(BeEmpty())
			Expect(ids(got.New)).To(Equal([]string{"alpha", "beta", "gamma"}))
		})
		It("refreshes when the address changes and not when only the name does", func() {
			e := newEnv(memStore())
			other := newFakeRemote("delta")
			defer other.Close()
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			callsBefore := other.calls
			name := "renamed"
			_, err = e.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			remote.mu.Lock()
			before := remote.calls
			remote.mu.Unlock()
			_, err = e.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			remote.mu.Lock()
			Expect(remote.calls).To(Equal(before), "a rename does not call the server")
			remote.mu.Unlock()
			got, err := e.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{URL: &other.URL})
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(got.New)).To(Equal([]string{"delta"}))
			Expect(other.calls).To(BeNumerically(">", callsBefore))
		})
		It("stops every loaded model that names the remote on a key change", func() {
			e := newEnv(memStore())
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret, Models: chat("alpha")})
			Expect(err).NotTo(HaveOccurred())
			// A copy made by hand that names the same remote.
			copied := replaceOnce(e.read("alpha"), "name: alpha", "name: my-copy")
			Expect(os.WriteFile(e.file("my-copy"), []byte(copied), 0o644)).To(Succeed())
			Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
			key := "sk-OTHER"
			_, err = e.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{APIKey: &key})
			Expect(err).NotTo(HaveOccurred())
			Expect(e.unloader.seen()).To(ConsistOf("alpha", "my-copy"))
		})
		It("returns ErrPartial and the saved remote when an entry patch fails", func() {
			var flaky *flakyAdmin
			e := newEnvWith(memStore(), func(a remotes.ModelAdmin) remotes.ModelAdmin {
				flaky = &flakyAdmin{ModelAdmin: a}
				return flaky
			})
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret, Models: chat("alpha", "beta")})
			Expect(err).NotTo(HaveOccurred())
			flaky.set("beta")
			moved := "https://moved.example.com"
			got, err := e.svc.Update(ctx, c.Remote.ID, remotes.UpdateRequest{URL: &moved})
			Expect(errors.Is(err, remotes.ErrPartial)).To(BeTrue())
			Expect(got.ID).To(Equal(c.Remote.ID))
			Expect(got.URL).To(Equal(moved))
			Expect(e.config("alpha").Proxy.UpstreamURL).To(Equal(moved))
			Expect(e.config("beta").Proxy.UpstreamURL).To(Equal(remote.URL))
		})
	})

	Describe("Delete", func() {
		It("stops half way with ErrPartial and finishes on a retry", func() {
			var flaky *flakyAdmin
			e := newEnvWith(memStore(), func(a remotes.ModelAdmin) remotes.ModelAdmin {
				flaky = &flakyAdmin{ModelAdmin: a}
				return flaky
			})
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret, Models: chat("alpha", "beta")})
			Expect(err).NotTo(HaveOccurred())
			flaky.set("beta")
			err = e.svc.Delete(ctx, c.Remote.ID, true)
			Expect(errors.Is(err, remotes.ErrPartial)).To(BeTrue())
			got, err := e.svc.Get(ctx, c.Remote.ID)
			Expect(err).NotTo(HaveOccurred(), "the remote stays")
			Expect(got.Entries).To(Equal([]string{"beta"}))
			Expect(e.file("alpha")).NotTo(BeAnExistingFile())
			flaky.set()
			Expect(e.svc.Delete(ctx, c.Remote.ID, true)).To(Succeed())
			Expect(e.file("beta")).NotTo(BeAnExistingFile())
			_, err = e.svc.Get(ctx, c.Remote.ID)
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
		})
		It("does not delete a remote that another frontend gave a model meanwhile", func() {
			store := memStore()
			a := newEnv(store)
			c, err := a.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret, Models: chat("alpha")})
			Expect(err).NotTo(HaveOccurred())
			// Frontend b adopts while frontend a deletes: b's entry is
			// recorded after a handled alpha and before a removes the record.
			b := newEnv(store)
			racing := &raceStore{Store: store, before: func() {
				_, err := b.svc.Adopt(ctx, c.Remote.ID, chat("beta"))
				Expect(err).NotTo(HaveOccurred())
			}}
			a2 := newEnv(racing)
			Expect(a2.svc.Delete(ctx, c.Remote.ID, false)).To(MatchError(remotes.ErrPartial))
			got, err := a.svc.Get(ctx, c.Remote.ID)
			Expect(err).NotTo(HaveOccurred(), "the record stays, so beta does not name a deleted remote")
			Expect(got.Entries).To(Equal([]string{"beta"}))
		})
		It("serialises Adopt and Delete of one remote in a process", func() {
			e := newEnv(memStore())
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			var wg sync.WaitGroup
			var adoptErr, deleteErr error
			wg.Add(2)
			go func() { defer wg.Done(); _, adoptErr = e.svc.Adopt(ctx, c.Remote.ID, chat("alpha", "beta")) }()
			go func() { defer wg.Done(); deleteErr = e.svc.Delete(ctx, c.Remote.ID, false) }()
			wg.Wait()
			_, getErr := e.svc.Get(ctx, c.Remote.ID)
			switch {
			case deleteErr == nil:
				// Delete ran last or first; either way no entry names a gone remote.
				Expect(errors.Is(getErr, remotes.ErrNotFound)).To(BeTrue())
				for _, n := range []string{"alpha", "beta"} {
					if cfg, ok := e.loader.GetModelConfig(n); ok {
						Expect(cfg.Proxy.Remote).To(BeEmpty(), n)
					}
				}
				if adoptErr != nil {
					Expect(errors.Is(adoptErr, remotes.ErrNotFound)).To(BeTrue())
				}
			default:
				Fail("delete failed: " + deleteErr.Error())
			}
		})
	})

	Describe("partial results", func() {
		It("returns the remote and ErrPartial when adopting fails after it was stored", func() {
			store := &flakyStore{Store: memStore()}
			e := newEnv(store)
			// The first Update records alpha; the second fails.
			store.arm(1)
			res, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret, Models: chat("alpha", "beta")})
			Expect(errors.Is(err, remotes.ErrPartial)).To(BeTrue())
			Expect(res.Remote.ID).NotTo(BeEmpty())
			Expect(res.Remote.Entries).To(Equal([]string{"alpha"}))
			Expect(res.Created).To(Equal([]string{"alpha"}))
			Expect(e.file("beta")).To(BeAnExistingFile(), "the entry was written, only the record failed")
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		})
		It("returns ErrPartial from Adopt when the store write fails after an entry was written", func() {
			store := &flakyStore{Store: memStore()}
			e := newEnv(store)
			c, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "n", URL: remote.URL, APIKey: secret})
			Expect(err).NotTo(HaveOccurred())
			store.arm(0)
			res, err := e.svc.Adopt(ctx, c.Remote.ID, chat("alpha"))
			Expect(errors.Is(err, remotes.ErrPartial)).To(BeTrue())
			Expect(res.Remote.ID).To(Equal(c.Remote.ID))
			Expect(res.Created).To(BeEmpty())
		})
		It("does not mark a plain failure of Adopt as partial", func() {
			e := newEnv(memStore())
			_, err := e.svc.Adopt(ctx, "missing", chat("alpha"))
			Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
			Expect(errors.Is(err, remotes.ErrPartial)).To(BeFalse())
		})
	})
})

// raceStore runs before once, right before the first conditional Delete.
type raceStore struct {
	remotes.Store
	once   sync.Once
	before func()
}

func (r *raceStore) Delete(ctx context.Context, id string, check func(remotes.Record) error) error {
	r.once.Do(r.before)
	return r.Store.Delete(ctx, id, check)
}

var _ = Describe("startup refresh", func() {
	ctx := context.Background()

	It("refreshes every remote and records a failing remote", func() {
		good, bad := newFakeRemote("a"), newFakeRemote("b")
		defer good.Close()
		defer bad.Close()
		bad.fail(http.StatusBadGateway)
		e := newEnv(memStore())
		g, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Good", URL: good.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		b, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Bad", URL: bad.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		good.set("a", "c")
		before := g.Remote.LastRefresh
		later := before.Add(time.Minute)
		remotes.SetNow(e.svc, func() time.Time { return later })
		remotes.StartupRefresh(ctx, e.svc, nil)
		got, _ := e.svc.Get(ctx, g.Remote.ID)
		Expect(ids(got.New)).To(Equal([]string{"a", "c"}))
		Expect(*got.LastRefresh).To(Equal(later))
		gotBad, _ := e.svc.Get(ctx, b.Remote.ID)
		Expect(gotBad.Status).To(Equal(remotes.StatusUnreachable))
	})

	It("runs on one frontend at a time when there is a database", func() {
		db := openSQLite()
		remote := newFakeRemote("a")
		defer remote.Close()
		store, err := remotes.NewGORMStore(db)
		Expect(err).NotTo(HaveOccurred())
		e := newEnv(store)
		_, err = e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Home", URL: remote.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())

		// The second frontend arrives while the first one is still inside its
		// pass, so it must do nothing.
		entered, release := make(chan struct{}), make(chan struct{})
		slow := &blockingService{Service: e.svc, entered: entered, release: release}
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			remotes.StartupRefresh(ctx, slow, db)
		}()
		Eventually(entered).Should(BeClosed())
		counting := &countingService{Service: e.svc}
		remotes.StartupRefresh(ctx, counting, db)
		Expect(counting.refreshes()).To(BeZero(), "the second frontend found the lock taken")
		close(release)
		Eventually(done).Should(BeClosed())

		counting2 := &countingService{Service: e.svc}
		remotes.StartupRefresh(ctx, counting2, db)
		Expect(counting2.refreshes()).To(Equal(1), "the lock is free again after the pass")
	})
})

type blockingService struct {
	remotes.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingService) Refresh(ctx context.Context, id string) (remotes.Remote, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.Service.Refresh(ctx, id)
}

type countingService struct {
	remotes.Service
	mu sync.Mutex
	n  int
}

func (c *countingService) Refresh(ctx context.Context, id string) (remotes.Remote, error) {
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

func resolveKey(ctx context.Context, svc remotes.Service, id string) (string, error) {
	rk, err := svc.ResolveKey(ctx, id)
	return rk.Key, err
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
