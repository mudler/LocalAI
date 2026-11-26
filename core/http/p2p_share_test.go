//go:build auth

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The whole path of a device: the administrator switches the server to mode
// share and mints an invite over the normal API, a second P2P host redeems it
// over P2P, and is then admitted to the API, where it still signs in.
var _ = Describe("P2P sharing end to end", func() {
	const secret = "test-secret-for-p2p"
	var (
		e            *echo.Echo
		app          *application.Application
		admin, plain string
	)

	do := func(method, path, key string, body any) (int, []byte) {
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
		e.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	BeforeEach(func() {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		models, backends, cfgDir := filepath.Join(dir, "models"), filepath.Join(dir, "backends"), filepath.Join(dir, "config")
		for _, d := range []string{models, backends, cfgDir} {
			Expect(os.Mkdir(d, 0o750)).To(Succeed())
		}
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		app, err = application.New(
			config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(dir, "data")),
			config.WithDynamicConfigDir(cfgDir),
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(secret),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })

		db := app.AuthDB()
		keyFor := func(id, role string) string {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
			k, _, err := auth.CreateAPIKey(db, id, "test", role, secret, nil)
			Expect(err).NotTo(HaveOccurred())
			return k
		}
		admin, plain = keyFor("root", auth.RoleAdmin), keyFor("plain", auth.RoleUser)

		e, err = API(app)
		Expect(err).NotTo(HaveOccurred())
	})

	It("mints an invite over the API, redeems it over P2P, and admits the device", func() {
		By("reporting mode off before P2P is switched on")
		code, body := do(http.MethodGet, "/api/p2p/status", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`"mode":"off"`))
		code, _ = do(http.MethodGet, "/api/p2p/devices", admin, nil)
		Expect(code).To(Equal(http.StatusConflict))

		By("switching to mode share")
		// POST /api/settings applies p2p_mode the same way; the spec sets the
		// config directly because the settings file watcher of the test
		// server reads the config while the endpoint writes it, a race of
		// every runtime setting that this spec should not trip over.
		app.ApplicationConfig().P2P = config.P2PSettings{Mode: "share", DisablePublicDHT: true, DisableMDNS: true,
			ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}}
		Expect(app.RestartP2PNet()).To(Succeed())

		var st struct {
			Mode   string   `json:"mode"`
			PeerID string   `json:"peer_id"`
			Addrs  []string `json:"addrs"`
		}
		code, body = do(http.MethodGet, "/api/p2p/status", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(body, &st)).To(Succeed())
		Expect(st.Mode).To(Equal("share"))
		Expect(st.PeerID).NotTo(BeEmpty())

		By("keeping the admin routes from other users and the redeem route off the normal listener")
		code, _ = do(http.MethodGet, "/api/p2p/status", plain, nil)
		Expect(code).To(Equal(http.StatusForbidden))
		code, _ = do(http.MethodPost, "/api/p2p/redeem", admin, map[string]any{"v": 1, "code": "AAAAAAAAAA"})
		Expect(code).To(Equal(http.StatusNotFound))
		code, _ = do(http.MethodGet, "/api/p2p/routes", admin, nil)
		Expect(code).To(Equal(http.StatusNotFound), "the routes route exists over P2P only")

		By("minting a device invite")
		var minted struct {
			Link string `json:"link"`
		}
		code, body = do(http.MethodPost, "/api/p2p/invites", admin, map[string]any{"kind": "device", "label": "laptop", "relays": "none"})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		Expect(json.Unmarshal(body, &minted)).To(Succeed())
		Expect(minted.Link).To(HavePrefix("localai://pair?"))

		By("redeeming it over P2P from a second host")
		dev, err := p2pnet.New(context.Background(), p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(dev.Close)
		server, err := peer.Decode(st.PeerID)
		Expect(err).NotTo(HaveOccurred())
		var addrs []ma.Multiaddr
		for _, a := range st.Addrs {
			m, err := ma.NewMultiaddr(a)
			Expect(err).NotTo(HaveOccurred())
			addrs = append(addrs, m)
		}
		client := dev.HTTPClient(server, p2pnet.WithAddrs(addrs...))
		client.Timeout = 15 * time.Second
		base := "http://" + server.String()

		resp, err := client.Get(base + "/api/p2p/status")
		Expect(err).NotTo(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden), "an unpaired peer may only redeem")
		resp, err = client.Get(base + "/api/p2p/routes")
		Expect(err).NotTo(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden), "an unpaired peer gets no routes")

		inviteCode := linkCode(minted.Link)
		reqBody, _ := json.Marshal(map[string]any{"v": 1, "code": inviteCode, "name": "laptop", "platform": "linux"})
		resp, err = client.Post(base+"/api/p2p/redeem", "application/json", bytes.NewReader(reqBody))
		Expect(err).NotTo(HaveOccurred())
		var redeemed struct {
			Phrase string `json:"phrase"`
			Kind   string `json:"kind"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&redeemed)).To(Succeed())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(redeemed.Kind).To(Equal("device"))
		Expect(redeemed.Phrase).NotTo(BeEmpty())

		By("admitting the device, which still signs in")
		resp, err = client.Get(base + "/api/p2p/status")
		Expect(err).NotTo(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

		req, _ := http.NewRequest(http.MethodGet, base+"/api/p2p/status", nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		resp, err = client.Do(req)
		Expect(err).NotTo(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("giving the paired device its routes over P2P, with no sign-in")
		resp, err = client.Get(base + "/api/p2p/routes")
		Expect(err).NotTo(HaveOccurred())
		var routes struct {
			Server string   `json:"server"`
			Addrs  []string `json:"addrs"`
			Relays []string `json:"relays"`
			Hints  []string `json:"hints"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&routes)).To(Succeed())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(routes.Server).To(Equal(st.PeerID))
		Expect(routes.Relays).NotTo(BeNil())
		Expect(routes.Hints).NotTo(BeNil())

		By("listing the device as online")
		code, body = do(http.MethodGet, "/api/p2p/devices", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`"online":true`))
	})

	It("refuses a remote invite made with the API key of LOCALAI_API_KEY and says why", func() {
		const legacy = "legacy-key-for-p2p-test"
		app.ApplicationConfig().ApiKeys = []string{legacy}
		app.ApplicationConfig().P2P = config.P2PSettings{Mode: "share", DisablePublicDHT: true, DisableMDNS: true,
			ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}}
		Expect(app.RestartP2PNet()).To(Succeed())
		e, err := API(app)
		Expect(err).NotTo(HaveOccurred())
		send := func(key string, body any) (int, []byte) {
			b, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/api/p2p/invites", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+key)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec.Code, rec.Body.Bytes()
		}

		code, body := send(legacy, map[string]any{"kind": "remote", "label": "office", "relays": "none"})
		Expect(code).To(Equal(http.StatusConflict), string(body))
		var refusal struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(body, &refusal)).To(Succeed())
		Expect(refusal.Error.Type).To(Equal("needs_account"))
		Expect(refusal.Error.Message).To(ContainSubstring("user account"))
		Expect(refusal.Error.Message).To(ContainSubstring("LOCALAI_API_KEY"))

		By("still making a device invite with that key, and a remote invite with a user key")
		code, body = send(legacy, map[string]any{"kind": "device", "relays": "none"})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		code, body = send(admin, map[string]any{"kind": "remote", "relays": "none"})
		Expect(code).To(Equal(http.StatusCreated), string(body))
	})

	It("refuses an unknown P2P mode in the settings", func() {
		code, body := do(http.MethodPost, "/api/settings", admin, map[string]any{"p2p_mode": "both"})
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(string(body)).To(ContainSubstring("p2p_mode"))
	})
})

func linkCode(link string) string {
	l, err := p2ppair.ParseLink(link)
	Expect(err).NotTo(HaveOccurred())
	return l.Code
}
