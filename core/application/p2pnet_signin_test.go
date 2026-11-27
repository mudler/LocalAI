//go:build auth

package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// signInUser adds an active user who has p2p_pair (or not) to the auth
// database of a.
func signInUser(a *Application, id, role string, pair bool) {
	db := a.AuthDB()
	Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
	Expect(auth.UpdateUserPermissions(db, id, auth.PermissionMap{auth.FeatureP2PPair: pair})).To(Succeed())
}

// pairSignIn mints a sign-in invite for owner, redeems it from a new device
// and returns the device host, its client and the credential.
func pairSignIn(server *p2pRuntime, owner string) (*p2pnet.Host, *http.Client, string) {
	ctx := context.Background()
	m, err := server.pair.Mint(ctx, p2ppair.MintRequest{OwnerID: owner, CreatedBy: owner, Kind: p2ppair.KindDevice, Relays: p2ppair.RelayNone, SignIn: true})
	Expect(err).ToNot(HaveOccurred())
	server.reload()
	h, c := device(server)
	status, out := redeemCode(c, server, m.Link.Code)
	Expect(status).To(Equal(http.StatusOK))
	cred, _ := out["device_credential"].(string)
	Expect(cred).ToNot(BeEmpty())
	if owner != "" {
		Expect(out).To(HaveKeyWithValue("owner_name", owner), "the redeem names the account the device signs in as")
	}
	return h, c, cred
}

// p2pPairStoreMust returns the pairing store of a.
func (a *Application) p2pPairStoreMust() p2ppair.Store {
	st, err := a.p2pPairStore()
	Expect(err).ToNot(HaveOccurred())
	return st
}

// pairPlain mints an admission-only invite for owner and redeems it.
func pairPlain(server *p2pRuntime, owner string) (*p2pnet.Host, *http.Client) {
	m, err := server.pair.Mint(context.Background(), p2ppair.MintRequest{OwnerID: owner, CreatedBy: owner, Kind: p2ppair.KindDevice, Relays: p2ppair.RelayNone})
	Expect(err).ToNot(HaveOccurred())
	server.reload()
	h, c := device(server)
	status, _ := redeemCode(c, server, m.Link.Code)
	Expect(status).To(Equal(http.StatusOK))
	return h, c
}

type sessionAnswer struct {
	status   int
	body     string
	cookie   string
	location string
}

// askSession posts a credential to the session route over P2P, as JSON or as
// a form.
func askSession(c *http.Client, server *p2pRuntime, credential string, form bool) sessionAnswer {
	target := "http://" + server.host.ID().String() + deviceSessionPath
	var req *http.Request
	var err error
	if form {
		req, err = http.NewRequest(http.MethodPost, target, strings.NewReader(url.Values{"credential": {credential}}.Encode()))
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		b, _ := json.Marshal(map[string]string{"credential": credential})
		req, err = http.NewRequest(http.MethodPost, target, strings.NewReader(string(b)))
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
	}
	// Keep the redirect of a form post as it is.
	nc := *c
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := nc.Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	out := sessionAnswer{status: resp.StatusCode, body: string(b), location: resp.Header.Get("Location")}
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" {
			out.cookie = ck.Value
		}
	}
	return out
}

var _ = Describe("P2P device sign-in", func() {
	It("exchanges the credential of a sign-in device for a session of its owner", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		h, c, cred := pairSignIn(server, "alice")

		By("answering JSON with the user and a session cookie")
		ans := askSession(c, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusOK), ans.body)
		Expect(ans.cookie).ToNot(BeEmpty())
		var u p2papi.SessionUser
		Expect(json.Unmarshal([]byte(ans.body), &u)).To(Succeed())
		Expect(u.ID).To(Equal("alice"))
		Expect(ans.body).ToNot(ContainSubstring(cred))

		By("tying the session to the device")
		user, sess := auth.ValidateSession(a.AuthDB(), ans.cookie, a.ApplicationConfig().Auth.APIKeyHMACSecret)
		Expect(user).ToNot(BeNil())
		Expect(user.ID).To(Equal("alice"))
		Expect(sess.DeviceID).ToNot(BeNil())
		devs, err := server.pair.ListDevices(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].PeerID).To(Equal(h.ID().String()))
		Expect(*sess.DeviceID).To(Equal(devs[0].ID))

		By("answering a form post with a redirect to the start page")
		ans = askSession(c, server, cred, true)
		Expect(ans.status).To(Equal(http.StatusSeeOther), ans.body)
		Expect(ans.location).To(Equal("/"))
		Expect(ans.cookie).ToNot(BeEmpty())

		By("refusing another method")
		resp, err := c.Get("http://" + server.host.ID().String() + deviceSessionPath)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	It("gives an admin owner a session even without an explicit permission", func() {
		a, server := shareServer()
		signInUser(a, "root", auth.RoleAdmin, false)
		_, c, cred := pairSignIn(server, "root")
		ans := askSession(c, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusOK), ans.body)
	})

	It("refuses every other case with one answer", func() {
		a, server := shareServer()
		db := a.AuthDB()
		signInUser(a, "alice", auth.RoleUser, true)
		signInUser(a, "bob", auth.RoleUser, true)
		signInUser(a, "carol", auth.RoleUser, true)
		signInUser(a, "dave", auth.RoleUser, true)

		_, alice, aliceCred := pairSignIn(server, "alice")
		_, plain := pairPlain(server, "alice")
		bobPeer, _, bobCred := pairSignIn(server, "bob")
		_, carol, carolCred := pairSignIn(server, "carol")
		davePeer, _, daveCred := pairSignIn(server, "dave")

		var refusal string
		refused := func(ans sessionAnswer) {
			GinkgoHelper()
			Expect(ans.status).To(Equal(http.StatusForbidden), ans.body)
			Expect(ans.cookie).To(BeEmpty())
			if refusal == "" {
				refusal = ans.body
			}
			Expect(ans.body).To(Equal(refusal))
		}

		By("a wrong credential")
		refused(askSession(alice, server, "wrong-"+aliceCred[6:], false))
		By("the credential of another device, from the wrong peer")
		refused(askSession(alice, server, bobCred, false))
		By("an admission-only device")
		refused(askSession(plain, server, aliceCred, false))
		By("an owner without p2p_pair")
		Expect(auth.UpdateUserPermissions(db, "carol", auth.PermissionMap{auth.FeatureP2PPair: false})).To(Succeed())
		refused(askSession(carol, server, carolCred, false))

		// A disabled owner and a revoked device leave the gate at the next
		// reload, so the route is checked through the service, which must
		// refuse them on its own.
		svc := p2pService{n: a.p2p()}
		By("a disabled owner")
		Expect(db.Model(&auth.User{}).Where("id = ?", "bob").Update("status", auth.StatusDisabled).Error).To(Succeed())
		_, err := svc.DeviceSession(context.Background(), bobPeer.ID().String(), bobCred)
		Expect(err).To(MatchError(p2papi.ErrSignInRefused))

		By("a revoked device")
		devs, err := server.pair.ListDevices(context.Background())
		Expect(err).ToNot(HaveOccurred())
		for _, d := range devs {
			if d.OwnerID == "dave" {
				Expect(server.pair.Revoke(context.Background(), d.ID, "test")).To(Succeed())
			}
		}
		_, err = svc.DeviceSession(context.Background(), davePeer.ID().String(), daveCred)
		Expect(err).To(MatchError(p2papi.ErrSignInRefused))
		Expect(refusal).To(ContainSubstring(p2papi.ErrSignInRefused.Error()))
	})

	It("limits wrong credentials per peer only, apart from the redeem limiter", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		// Every device here dials from loopback, so they share one source.
		_, c1, _ := pairSignIn(server, "alice")
		_, c2, _ := pairSignIn(server, "alice")
		_, c3, cred3 := pairSignIn(server, "alice")
		for _, c := range []*http.Client{c1, c2} {
			for range 5 {
				Expect(askSession(c, server, "nope", false).status).To(Equal(http.StatusForbidden))
			}
			Expect(askSession(c, server, "nope", false).status).To(Equal(http.StatusTooManyRequests))
		}

		By("letting another peer on the same source sign in")
		ans := askSession(c3, server, cred3, false)
		Expect(ans.status).To(Equal(http.StatusOK), ans.body)

		By("letting a new device on the same source redeem an invite")
		pairSignIn(server, "alice")
	})

	It("counts wrong credentials per peer", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		_, c, cred := pairSignIn(server, "alice")
		for range 5 {
			Expect(askSession(c, server, "nope", false).status).To(Equal(http.StatusForbidden))
		}
		ans := askSession(c, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusTooManyRequests), ans.body)
	})

	It("ends the sessions of a device when it is revoked", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		h, c, cred := pairSignIn(server, "alice")
		ans := askSession(c, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusOK))
		secret := a.ApplicationConfig().Auth.APIKeyHMACSecret

		devs, err := server.pair.ListDevices(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(a.P2P().RevokeDevice(context.Background(), devs[0].ID, "root")).To(Succeed())

		u, _ := auth.ValidateSession(a.AuthDB(), ans.cookie, secret)
		Expect(u).To(BeNil())
		Expect(server.gate.Allowed(h.ID())).To(BeFalse())
	})

	It("replaces the session of a device when it signs in again", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		_, c, cred := pairSignIn(server, "alice")
		secret := a.ApplicationConfig().Auth.APIKeyHMACSecret
		first := askSession(c, server, cred, false)
		Expect(first.status).To(Equal(http.StatusOK))
		second := askSession(c, server, cred, false)
		Expect(second.status).To(Equal(http.StatusOK))

		u, _ := auth.ValidateSession(a.AuthDB(), first.cookie, secret)
		Expect(u).To(BeNil(), "the first session ends")
		u, _ = auth.ValidateSession(a.AuthDB(), second.cookie, secret)
		Expect(u).ToNot(BeNil())
		var n int64
		Expect(a.AuthDB().Model(&auth.Session{}).Where("device_id IS NOT NULL").Count(&n).Error).To(Succeed())
		Expect(n).To(BeEquivalentTo(1))
	})

	It("leaves no session when the device is revoked during the exchange", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		_, c, cred := pairSignIn(server, "alice")
		devs, err := server.pair.ListDevices(context.Background())
		Expect(err).ToNot(HaveOccurred())

		// The revoke lands after the credential check and before the insert,
		// as when another frontend revokes the device: the row and the
		// sessions change, and the connection of this frontend stays open.
		beforeDeviceSessionCreate = func() {
			_, err := a.p2pPairStoreMust().RevokeDevice(context.Background(), devs[0].ID, "root", time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.DeleteDeviceSessions(a.AuthDB(), devs[0].ID)).To(Succeed())
		}
		DeferCleanup(func() { beforeDeviceSessionCreate = func() {} })

		ans := askSession(c, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusForbidden), ans.body)
		Expect(ans.cookie).To(BeEmpty())
		var n int64
		Expect(a.AuthDB().Model(&auth.Session{}).Where("device_id = ?", devs[0].ID).Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero(), "no session row of the revoked device is left")
	})

	It("reports a revoke whose sessions could not be ended", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		h, c, cred := pairSignIn(server, "alice")
		Expect(askSession(c, server, cred, false).status).To(Equal(http.StatusOK))
		devs, err := server.pair.ListDevices(context.Background())
		Expect(err).ToNot(HaveOccurred())

		// The delete of the sessions fails, as on a lock timeout.
		Expect(a.AuthDB().Migrator().DropTable(&auth.Session{})).To(Succeed())

		By("failing the admin revoke and the user revoke")
		Expect(a.P2P().RevokeDevice(context.Background(), devs[0].ID, "root")).ToNot(Succeed())
		Expect(a.P2PUsers().RevokeMyDevice(context.Background(), "alice", devs[0].ID)).ToNot(Succeed())

		By("still revoking the device and closing its connections")
		d, err := server.pair.GetDevice(context.Background(), devs[0].ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(d.Status()).To(Equal(p2ppair.DeviceRevoked))
		Expect(server.gate.Allowed(h.ID())).To(BeFalse())

		By("failing the revoke of the user's devices too, with or without a running host")
		pairSignIn(server, "alice")
		Expect(a.RevokeUserDevices(context.Background(), "alice", "root")).ToNot(Succeed())
		pairSignIn(server, "alice")
		a.StopP2PNet()
		Expect(a.RevokeUserDevices(context.Background(), "alice", "root")).ToNot(Succeed())
	})

	It("revokes the sign-in devices of a user and ends their sessions", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		signInUser(a, "root", auth.RoleAdmin, true)
		h, c, cred := pairSignIn(server, "alice")
		admin, _ := pairPlain(server, "root")
		ans := askSession(c, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusOK))

		Expect(a.RevokeUserDevices(context.Background(), "alice", "root")).To(Succeed())

		u, _ := auth.ValidateSession(a.AuthDB(), ans.cookie, a.ApplicationConfig().Auth.APIKeyHMACSecret)
		Expect(u).To(BeNil())
		Expect(server.gate.Allowed(h.ID())).To(BeFalse())
		Expect(server.gate.Allowed(admin.ID())).To(BeTrue(), "the devices of other users stay")
		devs, err := server.pair.ListDevices(context.Background())
		Expect(err).ToNot(HaveOccurred())
		for _, d := range devs {
			if d.OwnerID == "alice" {
				Expect(d.Status()).To(Equal(p2ppair.DeviceRevoked))
			} else {
				Expect(d.Status()).To(Equal(p2ppair.DeviceActive))
			}
		}
	})

	It("revokes the devices of a user in the store while P2P is not in mode share", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		pairSignIn(server, "alice")
		a.StopP2PNet()

		Expect(a.RevokeUserDevices(context.Background(), "alice", "root")).To(Succeed())
		store, err := a.p2pPairStore()
		Expect(err).ToNot(HaveOccurred())
		devs, err := store.ListDevicesByOwner(context.Background(), "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].Status()).To(Equal(p2ppair.DeviceRevoked))
	})

	It("drops from the gate the sign-in devices of an owner who is no longer active", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		signInUser(a, "root", auth.RoleAdmin, true)
		h, _, _ := pairSignIn(server, "alice")
		admin, _ := pairPlain(server, "root")
		Expect(server.gate.Allowed(h.ID())).To(BeTrue())

		// Only the user row changes, as when a frontend missed the revoke.
		Expect(a.AuthDB().Model(&auth.User{}).Where("id = ?", "alice").Update("status", auth.StatusDisabled).Error).To(Succeed())
		Eventually(func() bool { return server.gate.Allowed(h.ID()) }).Should(BeFalse())
		Expect(server.gate.Allowed(admin.ID())).To(BeTrue())

		By("dropping them as well when the owner is deleted")
		Expect(a.AuthDB().Model(&auth.User{}).Where("id = ?", "alice").Update("status", auth.StatusActive).Error).To(Succeed())
		Eventually(func() bool { return server.gate.Allowed(h.ID()) }).Should(BeTrue())
		Expect(auth.DeleteUserCascade(a.AuthDB(), "alice")).To(Succeed())
		Eventually(func() bool { return server.gate.Allowed(h.ID()) }).Should(BeFalse())
	})

	It("lets only devices of admins without sign-in reserve on the in-process relay", func() {
		a, server := shareServer()
		signInUser(a, "alice", auth.RoleUser, true)
		signInUser(a, "root", auth.RoleAdmin, true)
		phone, _, _ := pairSignIn(server, "alice")
		adminPhone, _, _ := pairSignIn(server, "root")
		laptop, _ := pairPlain(server, "root")
		legacy, _ := pairPlain(server, "")

		Expect(server.paired(phone.ID())).To(BeTrue())
		Expect(server.mayReserve(phone.ID())).To(BeFalse(), "a user's sign-in device never listens")
		Expect(server.mayReserve(adminPhone.ID())).To(BeFalse(), "an admin's sign-in device never listens either")
		Expect(server.mayReserve(laptop.ID())).To(BeTrue())
		Expect(server.mayReserve(legacy.ID())).To(BeTrue(), "the identity of LOCALAI_API_KEY is an admin")

		By("refusing a device of a non-admin owner without sign-in")
		_, err := server.pair.Mint(context.Background(), p2ppair.MintRequest{OwnerID: "alice", Kind: p2ppair.KindDevice, Relays: p2ppair.RelayNone})
		Expect(err).ToNot(HaveOccurred())
		server.reload()
		other, _ := pairPlain(server, "alice")
		Expect(server.paired(other.ID())).To(BeTrue())
		Expect(server.mayReserve(other.ID())).To(BeFalse())
	})

	It("lets a device session be used only over P2P from its device while the device is active", func() {
		a, server := shareServerWith(func(a *Application) http.Handler {
			e := echo.New()
			e.Use(auth.Middleware(a.AuthDB(), a.ApplicationConfig()))
			e.GET("/api/whoami", func(c echo.Context) error { return c.String(http.StatusOK, auth.GetUser(c).ID) })
			return e
		})
		signInUser(a, "alice", auth.RoleUser, true)
		signInUser(a, "bob", auth.RoleUser, true)
		_, phone, cred := pairSignIn(server, "alice")
		_, tablet, _ := pairSignIn(server, "bob")
		ans := askSession(phone, server, cred, false)
		Expect(ans.status).To(Equal(http.StatusOK))

		whoami := func(c *http.Client, session string) int {
			req, err := http.NewRequest(http.MethodGet, "http://"+server.host.ID().String()+"/api/whoami", nil)
			Expect(err).ToNot(HaveOccurred())
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
			resp, err := c.Do(req)
			if err != nil {
				return 0
			}
			_ = resp.Body.Close()
			return resp.StatusCode
		}

		By("accepting the session from the device that opened it")
		Expect(whoami(phone, ans.cookie)).To(Equal(http.StatusOK))

		By("refusing it over P2P from another paired peer")
		Expect(whoami(tablet, ans.cookie)).To(Equal(http.StatusUnauthorized))

		By("refusing it once the device is revoked, even if the session row survived")
		devs, err := server.pair.ListDevicesByOwner(context.Background(), "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		// Revoke the row only, as a revoke whose session cleanup failed.
		_, err = a.p2pPairStoreMust().RevokeDevice(context.Background(), devs[0].ID, "root", time.Now())
		Expect(err).ToNot(HaveOccurred())
		u, _ := auth.ValidateSession(a.AuthDB(), ans.cookie, a.ApplicationConfig().Auth.APIKeyHMACSecret)
		Expect(u).ToNot(BeNil(), "the row is still there")
		Expect(whoami(phone, ans.cookie)).To(Equal(http.StatusUnauthorized))
	})
})
