//go:build auth

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/remotes"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Two servers on one machine, each with its own P2P host on loopback: the
// "studio" shares a model with an invite of kind remote, the "laptop" adds
// it as a remote by invite and reaches it through its loopback tunnel, as
// the localai-proxy backend of an entry does.
var _ = Describe("Remote over P2P end to end", func() {
	const secret = "test-secret-for-p2p-remote"

	type server struct {
		app *application.Application
		e   *echo.Echo
	}

	newServer := func(withAuth bool, models map[string]string) server {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		modelsDir, backends, cfgDir := filepath.Join(dir, "models"), filepath.Join(dir, "backends"), filepath.Join(dir, "config")
		for _, d := range []string{modelsDir, backends, cfgDir} {
			Expect(os.Mkdir(d, 0o750)).To(Succeed())
		}
		for name, body := range models {
			Expect(os.WriteFile(filepath.Join(modelsDir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		state, err := system.GetSystemState(system.WithModelPath(modelsDir), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		opts := []config.AppOption{
			config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(dir, "data")),
			config.WithDynamicConfigDir(cfgDir),
		}
		if withAuth {
			opts = append(opts, config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(secret))
		}
		app, err := application.New(opts...)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })
		e, err := API(app)
		Expect(err).NotTo(HaveOccurred())
		return server{app: app, e: e}
	}

	do := func(s server, method, path, key string, body any) (int, []byte) {
		var rd io.Reader = http.NoBody
		if body != nil {
			b, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	p2pOn := func(s server, mode string) {
		// The settings endpoint applies p2p_mode the same way; see the P2P
		// sharing spec for why the config is set directly.
		s.app.ApplicationConfig().P2P = config.P2PSettings{Mode: mode, DisablePublicDHT: true, DisableMDNS: true,
			ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}}
		Expect(s.app.RestartP2PNet()).To(Succeed())
	}

	It("adds a remote by invite, sends a request through the tunnel, and ends when the device is revoked", func() {
		studio := newServer(true, map[string]string{"shared-model": "name: shared-model\nbackend: llama-cpp\nparameters:\n  model: none.gguf\n"})
		laptop := newServer(false, nil)

		db := studio.app.AuthDB()
		Expect(db.Create(&auth.User{ID: "root", Email: "root@example.test", Name: "root", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
		admin, _, err := auth.CreateAPIKey(db, "root", "test", auth.RoleAdmin, secret, nil)
		Expect(err).NotTo(HaveOccurred())

		By("sharing the studio and minting a remote invite for one model")
		p2pOn(studio, "share")
		var minted struct {
			Link string `json:"link"`
		}
		code, body := do(studio, http.MethodPost, "/api/p2p/invites", admin,
			map[string]any{"kind": "remote", "label": "laptop", "relays": "none", "models": []string{"shared-model"}})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		Expect(json.Unmarshal(body, &minted)).To(Succeed())
		// The link holds public addresses only. Both servers run on loopback
		// here, so add the studio's loopback address as a server on a LAN
		// would have it.
		var st struct {
			Addrs []string `json:"addrs"`
		}
		code, body = do(studio, http.MethodGet, "/api/p2p/status", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(body, &st)).To(Succeed())
		Expect(st.Addrs).NotTo(BeEmpty())
		link := minted.Link + "&a=" + url.QueryEscape(st.Addrs[0])

		By("refusing the invite while P2P is off on the laptop")
		code, body = do(laptop, http.MethodPost, "/api/remotes", "", map[string]any{"invite": link})
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(string(body)).To(ContainSubstring("P2P is off"))

		By("adding the studio as a remote by invite on the laptop in mode connect")
		p2pOn(laptop, "connect")
		code, body = do(laptop, http.MethodPost, "/api/remotes", "",
			map[string]any{"invite": link, "models": []map[string]any{{"id": "shared-model", "usecases": []string{"chat"}}}})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var created struct {
			Remote  remotes.Remote `json:"remote"`
			Created []string       `json:"created"`
			Phrase  string         `json:"phrase"`
		}
		Expect(json.Unmarshal(body, &created)).To(Succeed())
		Expect(created.Phrase).NotTo(BeEmpty())
		Expect(created.Created).To(Equal([]string{"shared-model"}))
		r := created.Remote
		Expect(r.Transport).To(Equal(remotes.TransportP2P))
		Expect(r.Status).To(Equal(remotes.StatusOK), r.LastError)
		Expect(r.P2P).NotTo(BeNil())
		Expect(r.P2P.State).To(Equal(remotes.TunnelUp))
		Expect(r.P2P.Path).To(Equal("direct"))
		Expect(string(body)).NotTo(ContainSubstring(`"secret"`))

		By("answering 410 when the used invite is sent again")
		code, body = do(laptop, http.MethodPost, "/api/remotes", "", map[string]any{"invite": link, "name": "Again"})
		Expect(code).To(Equal(http.StatusGone), string(body))

		By("showing the same confirmation phrase on the studio")
		code, body = do(studio, http.MethodGet, "/api/p2p/devices", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		var devices struct {
			Devices []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"devices"`
		}
		Expect(json.Unmarshal(body, &devices)).To(Succeed())
		Expect(devices.Devices).To(HaveLen(1))
		Expect(devices.Devices[0].Kind).To(Equal("remote"))

		By("sending a request through the tunnel with the issued key, as the proxy backend does")
		cfg, ok := laptop.app.ModelConfigLoader().GetModelConfig("shared-model")
		Expect(ok).To(BeTrue())
		base := "http://127.0.0.1:" + strconv.Itoa(r.P2P.LocalPort)
		Expect(cfg.Proxy.UpstreamURL).To(Equal(base))
		key, err := config.ResolveProxyRemoteKey(context.Background(), r.ID, cfg.Proxy.UpstreamURL)
		Expect(err).NotTo(HaveOccurred())
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		Expect(err).NotTo(HaveOccurred())
		listing, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(listing))
		Expect(string(listing)).To(ContainSubstring("shared-model"))

		By("keeping the port across a restart of P2P on the laptop")
		p2pOn(laptop, "connect")
		code, body = do(laptop, http.MethodGet, "/api/remotes/"+r.ID, "", nil)
		Expect(code).To(Equal(http.StatusOK))
		var again remotes.Remote
		Expect(json.Unmarshal(body, &again)).To(Succeed())
		Expect(again.P2P.LocalPort).To(Equal(r.P2P.LocalPort))

		By("ending the remote when the studio revokes the laptop")
		code, _ = do(studio, http.MethodDelete, "/api/p2p/devices/"+devices.Devices[0].ID, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Eventually(func() remotes.Status {
			code, body := do(laptop, http.MethodPost, "/api/remotes/"+r.ID+"/refresh", "", nil)
			Expect(code).To(Equal(http.StatusOK))
			var cur remotes.Remote
			Expect(json.Unmarshal(body, &cur)).To(Succeed())
			return cur.Status
		}, 30*time.Second, time.Second).ShouldNot(Equal(remotes.StatusOK))
	})

	// The invite of the Share dialog lists no models: the remote then uses
	// every model a client key may use, and a model with tools stays out.
	It("shares every model with an invite that lists none, and answers a chat through the tunnel", func() {
		if mockBackendPath == "" {
			Skip("mock-backend binary not built; run 'make build-mock-backend'")
		}
		studio := newServer(true, map[string]string{
			"chat-model":  "name: chat-model\nbackend: mock-backend\nparameters:\n  model: mock-model.bin\n",
			"other-model": "name: other-model\nbackend: mock-backend\nparameters:\n  model: mock-model.bin\n",
			"tool-model":  "name: tool-model\nbackend: mock-backend\nparameters:\n  model: mock-model.bin\nagent:\n  max_iterations: 3\n",
		})
		studio.app.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)
		laptop := newServer(false, nil)

		db := studio.app.AuthDB()
		Expect(db.Create(&auth.User{ID: "root", Email: "root@example.test", Name: "root", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
		admin, _, err := auth.CreateAPIKey(db, "root", "test", auth.RoleAdmin, secret, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(studio.app.ModelConfigLoader().ClientModelProblem("tool-model")).To(HaveOccurred(), "the fixture must have tools attached")

		By("minting a remote invite without models, as the Share dialog does")
		p2pOn(studio, "share")
		code, body := do(studio, http.MethodPost, "/api/p2p/invites", admin, map[string]any{"kind": "remote", "label": "laptop", "relays": "none"})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var minted struct {
			Link string `json:"link"`
		}
		Expect(json.Unmarshal(body, &minted)).To(Succeed())
		var st struct {
			Addrs []string `json:"addrs"`
		}
		code, body = do(studio, http.MethodGet, "/api/p2p/status", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(body, &st)).To(Succeed())
		link := minted.Link + "&a=" + url.QueryEscape(st.Addrs[0])

		By("adding it on the laptop, which lists the models of the studio")
		p2pOn(laptop, "connect")
		code, body = do(laptop, http.MethodPost, "/api/remotes", "", map[string]any{"invite": link})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var created struct {
			Remote remotes.Remote `json:"remote"`
		}
		Expect(json.Unmarshal(body, &created)).To(Succeed())
		r := created.Remote
		Expect(r.Status).To(Equal(remotes.StatusOK), r.LastError)
		var listed []string
		for _, m := range r.New {
			listed = append(listed, m.ID)
		}
		Expect(listed).To(ConsistOf("chat-model", "other-model"))

		By("adopting a model and sending a chat request through the tunnel with the issued key")
		code, body = do(laptop, http.MethodPost, "/api/remotes/"+r.ID+"/adopt", "", map[string]any{"models": []map[string]any{{"id": "chat-model", "usecases": []string{"chat"}}}})
		Expect(code).To(Equal(http.StatusOK), string(body))
		cfg, ok := laptop.app.ModelConfigLoader().GetModelConfig("chat-model")
		Expect(ok).To(BeTrue())
		key, err := config.ResolveProxyRemoteKey(context.Background(), r.ID, cfg.Proxy.UpstreamURL)
		Expect(err).NotTo(HaveOccurred())
		client := &http.Client{Timeout: 60 * time.Second}
		send := func(model string) (int, string) {
			payload, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
			req, _ := http.NewRequest(http.MethodPost, cfg.Proxy.UpstreamURL+"/v1/chat/completions", bytes.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			out, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(out)
		}
		code, out := send("chat-model")
		Expect(code).To(Equal(http.StatusOK), out)
		Expect(out).To(ContainSubstring(`"choices"`))

		By("refusing a model with tools, and a model the studio does not have")
		code, out = send("tool-model")
		Expect(code).To(Equal(http.StatusForbidden), out)
		code, out = send("missing-model")
		Expect(code).To(Equal(http.StatusForbidden), out)
	})
})
