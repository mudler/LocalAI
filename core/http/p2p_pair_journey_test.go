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
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// The whole journey of a user who pairs their own phone: an administrator
// invites the user and turns on the feature default, the user mints an
// invite, a device redeems it with the real invite client over local P2P
// hosts, signs in with its credential and acts as the user, and loses that
// access when the user revokes it or the administrator disables the user.
// Everything runs in-process on loopback: no relay, no DHT, no mDNS.
var _ = Describe("P2P user pairing journey", func() {
	const (
		secret   = "test-secret-for-p2p-journey"
		password = "a-long-enough-passphrase-for-alice-7"
		// p2pTimeout bounds every request over P2P. The hosts are on
		// loopback, so a request takes milliseconds; the limit only tells a
		// refused peer from a hang.
		p2pTimeout = 10 * time.Second
	)

	var (
		e     *echo.Echo
		admin string
		db    *gorm.DB
	)

	// call sends a request to the normal listener. A key starting with
	// "cookie:" is sent as the session cookie, any other as a bearer key.
	call := func(method, path, cred string, body any) (int, []byte) {
		var rd io.Reader = http.NoBody
		if body != nil {
			b, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		if s, ok := strings.CutPrefix(cred, "cookie:"); ok {
			req.AddCookie(&http.Cookie{Name: "session", Value: s})
		} else if cred != "" {
			req.Header.Set("Authorization", "Bearer "+cred)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	sessionOf := func(cookies []*http.Cookie) string {
		for _, k := range cookies {
			if k.Name == "session" && k.Value != "" {
				return k.Value
			}
		}
		return ""
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

		app, err := application.New(
			config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(dir, "data")),
			config.WithDynamicConfigDir(cfgDir),
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(secret),
			config.WithAuthRegistrationMode("invite"),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })

		db = app.AuthDB()
		Expect(db.Create(&auth.User{ID: "root", Email: "root@example.test", Name: "root", Role: auth.RoleAdmin,
			Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
		admin, _, err = auth.CreateAPIKey(db, "root", "test", auth.RoleAdmin, secret, nil)
		Expect(err).NotTo(HaveOccurred())

		// Local hosts only: loopback listener, no public DHT, no mDNS and no
		// relay, so the test needs no network and finds no stranger.
		app.ApplicationConfig().P2P = config.P2PSettings{Mode: "share", DisablePublicDHT: true, DisableMDNS: true,
			ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}}
		Expect(app.RestartP2PNet()).To(Succeed())

		e, err = API(app)
		Expect(err).NotTo(HaveOccurred())
	})

	It("pairs a phone that signs in as its user until the user revokes it or is disabled", func() {
		By("letting the administrator invite alice, who registers")
		code, body := call(http.MethodPost, "/api/auth/admin/invites", admin, map[string]any{"expiresInHours": 1})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var userInvite struct {
			Code string `json:"code"`
		}
		Expect(json.Unmarshal(body, &userInvite)).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(
			`{"email":"alice@example.test","name":"alice","password":"`+password+`","inviteCode":"`+userInvite.Code+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		var registered struct {
			User struct {
				ID   string `json:"id"`
				Role string `json:"role"`
			} `json:"user"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &registered)).To(Succeed())
		Expect(registered.User.Role).To(Equal(auth.RoleUser))
		aliceID := registered.User.ID
		alice := "cookie:" + sessionOf(rec.Result().Cookies())
		Expect(alice).NotTo(Equal("cookie:"))

		By("refusing alice an invite while she does not have p2p_pair")
		code, body = call(http.MethodPost, "/api/p2p/me/invites", alice, map[string]any{"label": "phone"})
		Expect(code).To(Equal(http.StatusForbidden), string(body))

		By("letting alice mint once the administrator turns the default of p2p_pair on")
		code, body = call(http.MethodPut, "/api/auth/admin/features/p2p_pair/default", admin, map[string]any{"enabled": true})
		Expect(code).To(Equal(http.StatusOK), string(body))

		// mint has alice mint an invite and parses its link.
		mint := func(label string) *p2ppair.Link {
			code, body := call(http.MethodPost, "/api/p2p/me/invites", alice, map[string]any{"label": label, "ttl_seconds": 600})
			Expect(code).To(Equal(http.StatusCreated), string(body))
			var minted struct {
				Link string `json:"link"`
				Kind string `json:"kind"`
			}
			Expect(json.Unmarshal(body, &minted)).To(Succeed())
			Expect(minted.Kind).To(Equal("device"))
			l, err := p2ppair.ParseLink(minted.Link)
			Expect(err).NotTo(HaveOccurred())
			return l
		}

		// A link carries only public addresses, and the server listens on
		// loopback. A phone on the same network finds such addresses by
		// local discovery; here each device host learns them from the status
		// of the server, which the administrator reads.
		code, body = call(http.MethodGet, "/api/p2p/status", admin, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))
		var status struct {
			PeerID string   `json:"peer_id"`
			Addrs  []string `json:"addrs"`
		}
		Expect(json.Unmarshal(body, &status)).To(Succeed())
		server, err := peer.Decode(status.PeerID)
		Expect(err).NotTo(HaveOccurred())
		base := "http://" + server.String()
		var serverAddrs []ma.Multiaddr
		for _, a := range status.Addrs {
			m, err := ma.NewMultiaddr(a)
			Expect(err).NotTo(HaveOccurred())
			serverAddrs = append(serverAddrs, m)
		}
		Expect(serverAddrs).NotTo(BeEmpty())

		// newHost starts a device host with a fresh identity that knows the
		// loopback addresses of the server. It only dials.
		newHost := func() *p2pnet.Host {
			h, err := p2pnet.New(context.Background(), p2pnet.Config{
				IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
				ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(h.Close)
			h.Libp2p().Peerstore().AddAddrs(server, serverAddrs, peerstore.PermanentAddrTTL)
			return h
		}

		// device is a paired phone: its host, the HTTP client that reaches
		// the server over P2P, and the credential it got.
		type device struct {
			host *p2pnet.Host
			c    *http.Client
			cred string
		}
		// pair redeems a fresh invite of alice from a new host with the real
		// invite client.
		pair := func(label string) device {
			l := mint(label)
			Expect(l.Peer).To(Equal(server))
			h := newHost()
			ctx, cancel := context.WithTimeout(context.Background(), p2pTimeout)
			defer cancel()
			res, err := pairclient.Redeem(ctx, h, l, pairclient.Request{Name: label, Platform: "android"})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Kind).To(Equal("device"))
			Expect(res.SignIn).To(BeTrue())
			Expect(res.DeviceCredential).NotTo(BeEmpty())
			Expect(res.OwnerName).To(Equal("alice"))
			Expect(res.ClientKey).To(BeNil())
			c := h.HTTPClient(server, pairclient.DialOptions(l)...)
			c.Timeout = p2pTimeout
			return device{host: h, c: c, cred: res.DeviceCredential}
		}

		// signIn posts a credential from d over P2P. err is set when the
		// server refused the peer before any HTTP answer.
		signIn := func(d device, cred string) (int, string, error) {
			resp, err := d.c.Post(base+"/api/p2p/device/session", "application/json",
				strings.NewReader(`{"credential":"`+cred+`"}`))
			if err != nil {
				return 0, "", err
			}
			defer func() { _ = resp.Body.Close() }()
			_, _ = io.Copy(io.Discard, resp.Body)
			return resp.StatusCode, sessionOf(resp.Cookies()), nil
		}

		// overP2P sends a GET with the session cookie over P2P from d.
		overP2P := func(d device, path, session string) (int, []byte, error) {
			req, _ := http.NewRequest(http.MethodGet, base+path, nil)
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
			resp, err := d.c.Do(req)
			if err != nil {
				return 0, nil, err
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, b, nil
		}

		// me asks over P2P from d who the session is.
		me := func(d device, session string) (int, []byte, error) {
			return overP2P(d, "/api/auth/me", session)
		}

		By("pairing a phone with the real invite client over local P2P")
		phone := pair("phone")

		By("exchanging the credential for a session of alice over P2P")
		code, session, err := signIn(phone, phone.cred)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK))
		Expect(session).NotTo(BeEmpty())
		code, body, err = me(phone, session)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK), string(body))
		var who struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		}
		Expect(json.Unmarshal(body, &who)).To(Succeed())
		Expect(who.ID).To(Equal(aliceID))
		Expect(who.Role).To(Equal(auth.RoleUser), "the session has alice's rights, not more")
		code, _, err = overP2P(phone, "/api/auth/admin/users", session)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusForbidden), "the phone session is not an admin session")
		code, _ = call(http.MethodGet, "/api/auth/me", "cookie:"+session, nil)
		Expect(code).To(Equal(http.StatusUnauthorized), "a device session is refused on the normal listener")
		code, _ = call(http.MethodGet, "/api/auth/me", session, nil)
		Expect(code).To(Equal(http.StatusUnauthorized), "also as a bearer token")

		By("refusing a wrong credential")
		code, s, err := signIn(phone, "not-the-credential")
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(s).To(BeEmpty())

		By("refusing the right credential from another paired peer")
		tablet := pair("tablet")
		code, s, err = signIn(tablet, phone.cred)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(s).To(BeEmpty())

		By("refusing an unpaired peer at the gate, with no invite pending")
		stranger := device{host: newHost()}
		stranger.c = stranger.host.HTTPClient(server)
		stranger.c.Timeout = p2pTimeout
		_, _, err = signIn(stranger, phone.cred)
		Expect(err).To(HaveOccurred(), "the gate admits only paired peers")

		By("signing the second device in with its own credential")
		code, tabletSession, err := signIn(tablet, tablet.cred)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK))
		code, _, err = me(tablet, session)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusUnauthorized), "the phone's session is refused from another paired peer")

		By("listing both devices to alice")
		code, body = call(http.MethodGet, "/api/p2p/me/devices", alice, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))
		var mine struct {
			Devices []struct {
				ID     string `json:"id"`
				PeerID string `json:"peer_id"`
				Status string `json:"status"`
				SignIn bool   `json:"sign_in"`
			} `json:"devices"`
		}
		Expect(json.Unmarshal(body, &mine)).To(Succeed())
		Expect(mine.Devices).To(HaveLen(2))
		var phoneID string
		for _, d := range mine.Devices {
			Expect(d.SignIn).To(BeTrue())
			Expect(d.Status).To(Equal("active"))
			if d.PeerID == phone.host.ID().String() {
				phoneID = d.ID
			}
		}
		Expect(phoneID).NotTo(BeEmpty())

		By("ending the phone's session and refusing the phone at the gate when alice revokes it")
		code, body = call(http.MethodDelete, "/api/p2p/me/devices/"+phoneID, alice, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))
		u, _ := auth.ValidateSession(db, session, secret)
		Expect(u).To(BeNil(), "the session of a revoked device ends")
		// A new connection, not a stream of the old one the revoke closed,
		// must be the one refused.
		phone.c.CloseIdleConnections()
		_, _, err = me(phone, session)
		Expect(err).To(HaveOccurred(), "a revoked peer is refused at the gate")
		_, _, err = signIn(phone, phone.cred)
		Expect(err).To(HaveOccurred())

		By("keeping the second device working")
		code, body, err = me(tablet, tabletSession)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK), string(body))

		By("ending the second device's session and dropping it when the administrator disables alice")
		code, body = call(http.MethodPut, "/api/auth/admin/users/"+aliceID+"/status", admin, map[string]any{"status": "disabled"})
		Expect(code).To(Equal(http.StatusOK), string(body))
		u, _ = auth.ValidateSession(db, tabletSession, secret)
		Expect(u).To(BeNil(), "disabling the user ends the device's session")
		code, body = call(http.MethodGet, "/api/p2p/devices?owner="+aliceID, admin, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(ContainSubstring(`"status":"revoked"`))
		Expect(string(body)).NotTo(ContainSubstring(`"status":"active"`))
		tablet.c.CloseIdleConnections()
		_, _, err = me(tablet, tabletSession)
		Expect(err).To(HaveOccurred(), "the device of a disabled user is dropped from the gate")
		_, _, err = signIn(tablet, tablet.cred)
		Expect(err).To(HaveOccurred(), "a new session exchange is refused")

		By("limiting alice's pending invites once she is active again")
		code, body = call(http.MethodPut, "/api/auth/admin/users/"+aliceID+"/status", admin, map[string]any{"status": "active"})
		Expect(code).To(Equal(http.StatusOK), string(body))
		code, body = call(http.MethodPut, "/api/p2p/user-pairing", admin, map[string]any{"max_pending": 1})
		Expect(code).To(Equal(http.StatusOK), string(body))
		// Disabling alice ended her web session too: she signs in again.
		req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(
			`{"email":"alice@example.test","password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		alice = "cookie:" + sessionOf(rec.Result().Cookies())
		Expect(alice).NotTo(Equal("cookie:"))
		mint("third")
		code, body = call(http.MethodPost, "/api/p2p/me/invites", alice, map[string]any{"label": "fourth"})
		Expect(code).To(Equal(http.StatusTooManyRequests), string(body))
		Expect(string(body)).To(ContainSubstring(`"type":"limit_reached"`))
	})
})
