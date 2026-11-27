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
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A user who has p2p_pair pairs a phone from the normal API, the phone signs
// in over P2P with its credential, and the administrator disabling or
// deleting the user takes the phone away.
var _ = Describe("P2P pairing by users through the API", func() {
	const (
		secret = "test-secret-for-p2p-users"
		legacy = "legacy-key-for-p2p-users"
	)
	var (
		e                            *echo.Echo
		app                          *application.Application
		admin, alice, bob, plain, ck string
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
		app.ApplicationConfig().ApiKeys = []string{legacy}

		db := app.AuthDB()
		keyFor := func(id, role string) string {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
			k, _, err := auth.CreateAPIKey(db, id, "test", role, secret, nil)
			Expect(err).NotTo(HaveOccurred())
			return k
		}
		admin = keyFor("root", auth.RoleAdmin)
		alice, bob, plain = keyFor("alice", auth.RoleUser), keyFor("bob", auth.RoleUser), keyFor("plain", auth.RoleUser)
		for _, id := range []string{"alice", "bob"} {
			Expect(auth.UpdateUserPermissions(db, id, auth.PermissionMap{auth.FeatureP2PPair: true})).To(Succeed())
		}
		// A paired client (only an administrator may own one), like the key of
		// a remote: an administrator owner must not open the user routes to it.
		code, _, err := auth.CreatePairingCode(db, "root", "box", []string{auth.FeatureClientSelf, auth.FeatureClientInference}, nil, 0, 0, secret, auth.WithAllModels())
		Expect(err).NotTo(HaveOccurred())
		ck, _, err = auth.RedeemPairingCode(db, code, "box", secret)
		Expect(err).NotTo(HaveOccurred())

		app.ApplicationConfig().P2P = config.P2PSettings{Mode: "share", DisablePublicDHT: true, DisableMDNS: true,
			ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}}
		Expect(app.RestartP2PNet()).To(Succeed())

		e, err = API(app)
		Expect(err).NotTo(HaveOccurred())
	})

	It("opens the user routes to users with p2p_pair and nobody else", func() {
		userRoutes := [][2]string{
			{http.MethodGet, "/api/p2p/me"},
			{http.MethodPost, "/api/p2p/me/invites"},
			{http.MethodGet, "/api/p2p/me/invites"},
			{http.MethodDelete, "/api/p2p/me/invites/x"},
			{http.MethodGet, "/api/p2p/me/devices"},
			{http.MethodPatch, "/api/p2p/me/devices/x"},
			{http.MethodDelete, "/api/p2p/me/devices/x"},
		}
		By("refusing a user without the feature and a client key")
		for _, r := range userRoutes {
			code, body := do(r[0], r[1], plain, map[string]any{})
			Expect(code).To(Equal(http.StatusForbidden), r[1]+" "+string(body))
			code, body = do(r[0], r[1], ck, map[string]any{})
			Expect(code).To(Equal(http.StatusForbidden), "client key on "+r[1]+" "+string(body))
			code, _ = do(r[0], r[1], "", map[string]any{})
			Expect(code).To(Equal(http.StatusUnauthorized), r[1])
		}

		By("serving a user who has the feature")
		code, body := do(http.MethodGet, "/api/p2p/me", alice, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(ContainSubstring(`"available":true`))
		code, _ = do(http.MethodDelete, "/api/p2p/me/devices/x", alice, nil)
		Expect(code).To(Equal(http.StatusNotFound))

		By("keeping every other P2P route to administrators")
		for _, r := range [][2]string{
			{http.MethodGet, "/api/p2p/status"},
			{http.MethodPost, "/api/p2p/invites"},
			{http.MethodGet, "/api/p2p/invites"},
			{http.MethodGet, "/api/p2p/devices"},
			{http.MethodDelete, "/api/p2p/devices/x"},
			{http.MethodGet, "/api/p2p/relays"},
			{http.MethodGet, "/api/p2p/user-pairing"},
			{http.MethodPut, "/api/p2p/user-pairing"},
			{http.MethodGet, "/api/p2p/split-workers"},
		} {
			code, _ := do(r[0], r[1], alice, map[string]any{})
			Expect(code).To(Equal(http.StatusForbidden), r[1])
		}

		By("following the default of the feature that an administrator sets")
		code, _ = do(http.MethodPut, "/api/auth/admin/features/p2p_pair/default", admin, map[string]any{"enabled": true})
		Expect(code).To(Equal(http.StatusOK))
		code, body = do(http.MethodGet, "/api/p2p/me", plain, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))

		By("refusing the identity of LOCALAI_API_KEY, which has no account")
		code, body = do(http.MethodGet, "/api/p2p/me", legacy, nil)
		Expect(code).To(Equal(http.StatusConflict), string(body))
		Expect(string(body)).To(ContainSubstring(`"type":"needs_account"`))
	})

	It("lets an administrator read and change the user pairing setting", func() {
		code, body := do(http.MethodGet, "/api/p2p/user-pairing", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`"max_devices":5`))
		code, body = do(http.MethodPut, "/api/p2p/user-pairing", admin, map[string]any{"max_pending": 1, "relays": "none"})
		Expect(code).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(ContainSubstring(`"max_pending":1`))
		Expect(string(body)).To(ContainSubstring(`"relays":"none"`))
		code, _ = do(http.MethodPut, "/api/p2p/user-pairing", admin, map[string]any{"max_devices": 0})
		Expect(code).To(Equal(http.StatusBadRequest))

		code, _ = do(http.MethodPost, "/api/p2p/me/invites", alice, map[string]any{})
		Expect(code).To(Equal(http.StatusCreated))
		code, body = do(http.MethodPost, "/api/p2p/me/invites", alice, map[string]any{})
		Expect(code).To(Equal(http.StatusTooManyRequests), string(body))
		Expect(string(body)).To(ContainSubstring(`"type":"limit_reached"`))
	})

	It("pairs a phone that signs in as its owner and loses it when the owner is disabled or deleted", func() {
		var st struct {
			PeerID string   `json:"peer_id"`
			Addrs  []string `json:"addrs"`
		}
		code, body := do(http.MethodGet, "/api/p2p/status", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(body, &st)).To(Succeed())
		server, err := peer.Decode(st.PeerID)
		Expect(err).NotTo(HaveOccurred())
		var addrs []ma.Multiaddr
		for _, a := range st.Addrs {
			m, err := ma.NewMultiaddr(a)
			Expect(err).NotTo(HaveOccurred())
			addrs = append(addrs, m)
		}
		base := "http://" + server.String()

		// phone mints an invite for owner with key, redeems it from a new
		// host and returns the client of that host and the credential.
		phone := func(key string) (*http.Client, string) {
			code, body := do(http.MethodPost, "/api/p2p/me/invites", key, map[string]any{
				"label": "phone", "ttl_seconds": 600, "kind": "remote", "models": []string{"x"}, "relays": "auto",
			})
			Expect(code).To(Equal(http.StatusCreated), string(body))
			var minted struct {
				Link string `json:"link"`
				Kind string `json:"kind"`
			}
			Expect(json.Unmarshal(body, &minted)).To(Succeed())
			Expect(minted.Kind).To(Equal("device"), "the kind in the body is ignored")

			h, err := p2pnet.New(context.Background(), p2pnet.Config{
				IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
				ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(h.Close)
			c := h.HTTPClient(server, p2pnet.WithAddrs(addrs...))
			c.Timeout = 15 * time.Second
			reqBody, _ := json.Marshal(map[string]any{"v": 1, "code": linkCode(minted.Link), "name": "phone", "platform": "android"})
			resp, err := c.Post(base+"/api/p2p/redeem", "application/json", bytes.NewReader(reqBody))
			Expect(err).NotTo(HaveOccurred())
			var redeemed struct {
				Credential string `json:"device_credential"`
				SignIn     bool   `json:"sign_in"`
				ClientKey  any    `json:"client_key"`
			}
			Expect(json.NewDecoder(resp.Body).Decode(&redeemed)).To(Succeed())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(redeemed.SignIn).To(BeTrue())
			Expect(redeemed.ClientKey).To(BeNil())
			Expect(redeemed.Credential).NotTo(BeEmpty())
			return c, redeemed.Credential
		}

		// signIn posts the credential and returns the session cookie.
		signIn := func(c *http.Client, cred string) (int, string) {
			resp, err := c.Post(base+"/api/p2p/device/session", "application/json", strings.NewReader(`{"credential":"`+cred+`"}`))
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			for _, k := range resp.Cookies() {
				if k.Name == "session" {
					return resp.StatusCode, k.Value
				}
			}
			return resp.StatusCode, ""
		}

		By("pairing alice's phone and signing it in over P2P")
		c, cred := phone(alice)
		code, session := signIn(c, cred)
		Expect(code).To(Equal(http.StatusOK))
		Expect(session).NotTo(BeEmpty())

		By("serving the session over P2P, as alice")
		req, _ := http.NewRequest(http.MethodGet, base+"/api/p2p/me/devices", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: session})
		resp, err := c.Do(req)
		Expect(err).NotTo(HaveOccurred())
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(b))
		Expect(string(b)).To(ContainSubstring(`"sign_in":true`))

		By("not serving the session route on the normal listener")
		code, _ = do(http.MethodPost, "/api/p2p/device/session", admin, map[string]any{"credential": cred})
		Expect(code).To(Equal(http.StatusNotFound))

		By("showing the owner to the administrator and filtering by owner")
		code, body = do(http.MethodGet, "/api/p2p/devices?owner=alice", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`"owner_id":"alice"`))
		Expect(string(body)).To(ContainSubstring(`"owner_name":"alice"`))
		code, body = do(http.MethodGet, "/api/p2p/devices?owner=bob", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(MatchJSON(`{"devices":[]}`))

		By("hiding alice's device from bob")
		code, body = do(http.MethodGet, "/api/p2p/me/devices", bob, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(MatchJSON(`{"devices":[]}`))

		By("revoking alice's phone and ending its session when alice is disabled")
		code, _ = do(http.MethodPut, "/api/auth/admin/users/alice/status", admin, map[string]any{"status": "disabled"})
		Expect(code).To(Equal(http.StatusOK))
		code, body = do(http.MethodGet, "/api/p2p/devices?owner=alice", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`"status":"revoked"`))
		var n int64
		Expect(app.AuthDB().Model(&auth.Session{}).Where("user_id = ?", "alice").Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero())

		By("revoking bob's phone when bob is deleted")
		bc, bcred := phone(bob)
		code, _ = signIn(bc, bcred)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = do(http.MethodDelete, "/api/auth/admin/users/bob", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		code, body = do(http.MethodGet, "/api/p2p/devices?owner=bob", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`"status":"revoked"`))
		Expect(string(body)).NotTo(ContainSubstring(`"status":"active"`))
	})
})
