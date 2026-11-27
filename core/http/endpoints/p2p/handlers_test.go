package p2p_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"

	. "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeService records the calls and returns the configured error.
type fakeService struct {
	err      error
	mintBy   string
	mint     MintRequest
	redeemBy string
	redeem   RedeemRequest
	routesBy string
	renamed  [2]string
	regen    int
	limits   SetRelayLimitsRequest
	session  [2]string
	owner    string
	user     string
	myMint   MyMintRequest
	myID     string
	setPair  SetUserPairingRequest
}

func (f *fakeService) Status(context.Context) (Status, error) {
	return Status{Mode: "share", Enabled: true, Running: true, PeerID: "peer"}, f.err
}
func (f *fakeService) MintInvite(_ context.Context, by string, req MintRequest) (MintResult, error) {
	f.mintBy, f.mint = by, req
	return MintResult{ID: "inv", Link: "localai://pair?v=1", Kind: req.Kind, Warnings: []string{"no_relay"}}, f.err
}
func (f *fakeService) ListInvites(context.Context) ([]InviteView, error) {
	return []InviteView{{ID: "inv", Status: "pending"}}, f.err
}
func (f *fakeService) CancelInvite(context.Context, string) error { return f.err }
func (f *fakeService) ListDevices(_ context.Context, owner string) ([]DeviceView, error) {
	f.owner = owner
	return []DeviceView{{ID: "dev", Status: "active", Path: PathView{Kind: "direct"}, OwnerID: "alice", OwnerName: "Alice", SignIn: true}}, f.err
}
func (f *fakeService) UserPairing(context.Context) (UserPairingSettings, error) {
	return UserPairingSettings{Relays: "auto", MaxDevices: 5, MaxPending: 2, MaxTTLSeconds: 3600}, f.err
}
func (f *fakeService) SetUserPairing(_ context.Context, by string, req SetUserPairingRequest) (UserPairingSettings, error) {
	f.user, f.setPair = by, req
	return UserPairingSettings{Relays: "none", MaxDevices: *req.MaxDevices, MaxPending: 2, MaxTTLSeconds: 3600}, f.err
}
func (f *fakeService) MyPairing(_ context.Context, user string) (MyPairing, error) {
	f.user = user
	return MyPairing{Available: true, Mode: "share", Limits: UserPairingLimits{MaxDevices: 5}}, f.err
}
func (f *fakeService) MintMyInvite(_ context.Context, user string, req MyMintRequest) (MintResult, error) {
	f.user, f.myMint = user, req
	return MintResult{ID: "inv", Link: "localai://pair?v=1", Kind: "device"}, f.err
}
func (f *fakeService) ListMyInvites(_ context.Context, user string) ([]InviteView, error) {
	f.user = user
	return []InviteView{{ID: "inv", Status: "pending"}}, f.err
}
func (f *fakeService) CancelMyInvite(_ context.Context, user, id string) error {
	f.user, f.myID = user, id
	return f.err
}
func (f *fakeService) ListMyDevices(_ context.Context, user string) ([]DeviceView, error) {
	f.user = user
	return []DeviceView{{ID: "dev", Status: "active"}}, f.err
}
func (f *fakeService) RenameMyDevice(_ context.Context, user, id, label string) error {
	f.user, f.myID, f.renamed = user, id, [2]string{id, label}
	return f.err
}
func (f *fakeService) RevokeMyDevice(_ context.Context, user, id string) error {
	f.user, f.myID = user, id
	return f.err
}
func (f *fakeService) RenameDevice(_ context.Context, id, label string) error {
	f.renamed = [2]string{id, label}
	return f.err
}
func (f *fakeService) RevokeDevice(context.Context, string, string) error { return f.err }
func (f *fakeService) ListRelays(context.Context) ([]Relay, error) {
	return []Relay{{ID: "r1", State: "in_sync"}}, f.err
}
func (f *fakeService) AddRelay(_ context.Context, addr, label, _ string) (Relay, error) {
	return Relay{ID: "r2", Addr: addr, Label: label}, f.err
}
func (f *fakeService) RemoveRelay(context.Context, string) error { return f.err }
func (f *fakeService) CheckRelay(context.Context, string) (RelayCheck, error) {
	return RelayCheck{Connected: true}, f.err
}
func (f *fakeService) SyncRelay(context.Context, string) (Relay, error) {
	return Relay{ID: "r1"}, f.err
}
func (f *fakeService) ClaimRelay(context.Context, string, string) (Relay, error) {
	return Relay{ID: "r1", Mode: "gated"}, f.err
}
func (f *fakeService) RelayLimits(context.Context, string) (RelayLimitsInfo, error) {
	return RelayLimitsInfo{Limits: RelayLimits{CircuitBytes: 1 << 30, CircuitSeconds: 1800}, Ceiling: RelayLimits{CircuitBytes: 8 << 30, CircuitSeconds: 86400}, CanChange: true}, f.err
}
func (f *fakeService) SetRelayLimits(_ context.Context, _ string, req SetRelayLimitsRequest) (RelayLimitsInfo, error) {
	f.limits = req
	return RelayLimitsInfo{Limits: RelayLimits{RateBPS: *req.RateBPS}, CanChange: true, Restarted: true}, f.err
}
func (f *fakeService) RegenerateIdentity(context.Context) (Status, error) {
	f.regen++
	return Status{PeerID: "new"}, f.err
}
func (f *fakeService) Redeem(_ context.Context, from string, req RedeemRequest) (RedeemResult, error) {
	f.redeemBy, f.redeem = from, req
	return RedeemResult{DeviceID: "dev", Kind: "device", Phrase: "a b c d"}, f.err
}

func (f *fakeService) DeviceSession(_ context.Context, from, credential string) (DeviceSession, error) {
	f.session = [2]string{from, credential}
	return DeviceSession{Token: "tok", User: SessionUser{ID: "alice", Name: "Alice", Role: "user"}}, f.err
}

func (f *fakeService) Routes(_ context.Context, from string) (Routes, error) {
	f.routesBy = from
	return Routes{Server: "srv", Addrs: []string{"/ip4/203.0.113.9/tcp/4001"}, Relays: []string{}, Hints: []string{}}, f.err
}

func call(h echo.HandlerFunc, method, body string, ctx context.Context, params ...string) *httptest.ResponseRecorder {
	e := echo.New()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	for i := 0; i+1 < len(params); i += 2 {
		c.SetParamNames(params[i])
		c.SetParamValues(params[i+1])
	}
	Expect(h(c)).To(Succeed())
	return rec
}

func errorOf(rec *httptest.ResponseRecorder) string {
	var r schema.ErrorResponse
	Expect(json.Unmarshal(rec.Body.Bytes(), &r)).To(Succeed())
	Expect(r.Error).ToNot(BeNil())
	return r.Error.Message
}

var _ = Describe("P2P handlers", func() {
	var svc *fakeService
	BeforeEach(func() { svc = &fakeService{} })

	It("reports the status", func() {
		rec := call(StatusEndpoint(svc), http.MethodGet, "", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"peer_id":"peer"`))
	})

	It("mints an invite with relays given as a word or a list", func() {
		rec := call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"device","label":"laptop","relays":"none"}`, nil)
		Expect(rec.Code).To(Equal(http.StatusCreated))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		Expect(svc.mint.Relays).To(Equal(RelayChoice{Mode: "none"}))

		call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"remote","relays":["r1","r2"],"models":["m"]}`, nil)
		Expect(svc.mint.Relays).To(Equal(RelayChoice{Mode: "explicit", IDs: []string{"r1", "r2"}}))
		Expect(svc.mint.Models).To(Equal([]string{"m"}))

		call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"device","relays":null}`, nil)
		Expect(svc.mint.Relays).To(Equal(RelayChoice{Mode: "auto"}))

		call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"remote","all_models":true}`, nil)
		Expect(svc.mint.AllModels).To(BeTrue())
		Expect(svc.mint.Models).To(BeEmpty())

		// An absent field keeps the zero value, which the service reads as auto.
		call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"device"}`, nil)
		Expect(svc.mint.Relays).To(Equal(RelayChoice{}))
	})

	It("refuses a relays value that is neither a word nor a list", func() {
		rec := call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"device","relays":"some"}`, nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("answers 409 naming the mode when the server does not share", func() {
		svc.err = &ModeError{Mode: "connect", Need: "share"}
		for _, h := range []echo.HandlerFunc{ListInvitesEndpoint(svc), ListDevicesEndpoint(svc), ListRelaysEndpoint(svc)} {
			rec := call(h, http.MethodGet, "", nil)
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(errorOf(rec)).To(ContainSubstring(`"connect"`))
		}
	})

	DescribeTable("maps the service errors",
		func(err error, code int) {
			svc.err = err
			rec := call(CancelInviteEndpoint(svc), http.MethodDelete, "", nil, "id", "x")
			Expect(rec.Code).To(Equal(code))
		},
		Entry("not found", ErrNotFound, http.StatusNotFound),
		Entry("invalid", fmt.Errorf("%w: label", ErrInvalid), http.StatusBadRequest),
		Entry("conflict", fmt.Errorf("%w: no longer pending", ErrConflict), http.StatusConflict),
		Entry("unavailable", fmt.Errorf("%w: no relay manager", ErrUnavailable), http.StatusServiceUnavailable),
		Entry("no user account", ErrNeedsAccount, http.StatusConflict),
		Entry("other", fmt.Errorf("disk on fire"), http.StatusInternalServerError),
	)

	It("tells a caller without a user account how to share a remote", func() {
		svc.err = fmt.Errorf("mint: %w", ErrNeedsAccount)
		rec := call(MintInviteEndpoint(svc), http.MethodPost, `{"kind":"remote"}`, nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(errorOf(rec)).To(Equal(ErrNeedsAccount.Error()))
		Expect(errorOf(rec)).To(ContainSubstring("LOCALAI_API_KEY"))
		var r schema.ErrorResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &r)).To(Succeed())
		Expect(r.Error.Type).To(Equal("needs_account"))
	})

	It("does not show an internal error to the caller", func() {
		svc.err = fmt.Errorf("secret detail")
		rec := call(ListDevicesEndpoint(svc), http.MethodGet, "", nil)
		Expect(errorOf(rec)).ToNot(ContainSubstring("secret"))
	})

	It("renames a device with the label trimmed", func() {
		rec := call(RenameDeviceEndpoint(svc), http.MethodPatch, `{"label":"  phone "}`, nil, "id", "dev")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(svc.renamed).To(Equal([2]string{"dev", "phone"}))
	})

	It("revokes a device", func() {
		rec := call(RevokeDeviceEndpoint(svc), http.MethodDelete, "", nil, "id", "dev")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"revoked":true`))
	})

	It("adds, checks, syncs and claims relays", func() {
		Expect(call(AddRelayEndpoint(svc), http.MethodPost, `{"addr":" /ip4/1.2.3.4/tcp/1/p2p/x ","label":"home"}`, nil).Code).To(Equal(http.StatusCreated))
		Expect(call(CheckRelayEndpoint(svc), http.MethodPost, "", nil, "id", "r1").Code).To(Equal(http.StatusOK))
		Expect(call(SyncRelayEndpoint(svc), http.MethodPost, "", nil, "id", "r1").Code).To(Equal(http.StatusOK))
		Expect(call(RemoveRelayEndpoint(svc), http.MethodDelete, "", nil, "id", "r1").Code).To(Equal(http.StatusOK))
		Expect(call(ClaimRelayEndpoint(svc), http.MethodPost, `{"code":""}`, nil, "id", "r1").Code).To(Equal(http.StatusBadRequest))
		Expect(call(ClaimRelayEndpoint(svc), http.MethodPost, `{"code":"ABC"}`, nil, "id", "r1").Code).To(Equal(http.StatusOK))
	})

	It("reads and changes the limits of a relay", func() {
		rec := call(RelayLimitsEndpoint(svc), http.MethodGet, "", nil, "id", "r1")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"circuit_seconds":1800`))
		Expect(rec.Body.String()).To(ContainSubstring(`"can_change":true`))
		Expect(rec.Body.String()).To(ContainSubstring(`"ceiling":{"circuit_bytes":8589934592,"circuit_seconds":86400,"rate_bps":0}`))

		rec = call(SetRelayLimitsEndpoint(svc), http.MethodPut, `{"rate_bps":65536}`, nil, "id", "r1")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(*svc.limits.RateBPS).To(BeEquivalentTo(65536))
		Expect(svc.limits.CircuitBytes).To(BeNil())
		Expect(rec.Body.String()).To(ContainSubstring(`"restarted":true`))
	})

	It("refuses a limits change that names no limit", func() {
		Expect(call(SetRelayLimitsEndpoint(svc), http.MethodPut, `{}`, nil, "id", "r1").Code).To(Equal(http.StatusBadRequest))
	})

	It("maps a limits conflict to 409", func() {
		svc.err = fmt.Errorf("%w: not an admin", ErrConflict)
		Expect(call(SetRelayLimitsEndpoint(svc), http.MethodPut, `{"rate_bps":1}`, nil, "id", "r1").Code).To(Equal(http.StatusConflict))
	})

	It("regenerates the identity only when confirmed", func() {
		rec := call(RegenerateIdentityEndpoint(svc), http.MethodPost, `{}`, nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(svc.regen).To(BeZero())
		rec = call(RegenerateIdentityEndpoint(svc), http.MethodPost, `{"confirm":true}`, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(svc.regen).To(Equal(1))
	})

	Describe("redeem", func() {
		overP2P := WithPeer(context.Background(), "12D3KooWpeer")

		It("answers 404 to a request that did not arrive over P2P", func() {
			rec := call(RedeemEndpoint(svc), http.MethodPost, `{"v":1,"code":"ABC"}`, context.Background())
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(svc.redeemBy).To(BeEmpty())
		})

		It("takes the caller from the connection, never from the body", func() {
			rec := call(RedeemEndpoint(svc), http.MethodPost, `{"v":1,"code":"ABC","name":"phone","peer":"other"}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(svc.redeemBy).To(Equal("12D3KooWpeer"))
			Expect(svc.redeem).To(Equal(RedeemRequest{V: 1, Code: "ABC", Name: "phone"}))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		})

		It("gives one answer for every code that cannot be redeemed", func() {
			svc.err = ErrInviteInvalid
			rec := call(RedeemEndpoint(svc), http.MethodPost, `{"v":1,"code":"ABC"}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusGone))
			Expect(errorOf(rec)).To(Equal(ErrInviteInvalid.Error()))
		})

		It("answers 429 when rate limited", func() {
			svc.err = ErrRateLimited
			rec := call(RedeemEndpoint(svc), http.MethodPost, `{"v":1,"code":"ABC"}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		})

		It("refuses a body that is not JSON", func() {
			rec := call(RedeemEndpoint(svc), http.MethodPost, `not json`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("adds the sign-in fields to the answer only for a sign-in invite", func() {
			rec := call(RedeemEndpoint(svc), http.MethodPost, `{"v":1,"code":"ABC"}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusOK))
			var plain map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &plain)).To(Succeed())
			Expect(plain).NotTo(HaveKey("device_credential"))
			Expect(plain).NotTo(HaveKey("sign_in"))
			Expect(plain).NotTo(HaveKey("owner_name"))

			b, err := json.Marshal(RedeemResult{DeviceID: "dev", DeviceCredential: "cred", SignIn: true, OwnerName: "Alice"})
			Expect(err).NotTo(HaveOccurred())
			var signIn map[string]any
			Expect(json.Unmarshal(b, &signIn)).To(Succeed())
			Expect(signIn).To(HaveKeyWithValue("device_credential", "cred"))
			Expect(signIn).To(HaveKeyWithValue("sign_in", true))
			Expect(signIn).To(HaveKeyWithValue("owner_name", "Alice"))
		})
	})

	Describe("routes", func() {
		overP2P := WithPeer(context.Background(), "12D3KooWpeer")

		It("answers 404 to a request that did not arrive over P2P", func() {
			rec := call(RoutesEndpoint(svc), http.MethodGet, "", context.Background())
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(svc.routesBy).To(BeEmpty())
		})

		It("answers the caller of the connection with the routes", func() {
			rec := call(RoutesEndpoint(svc), http.MethodGet, "", overP2P)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(svc.routesBy).To(Equal("12D3KooWpeer"))
			Expect(rec.Body.String()).To(ContainSubstring(`"addrs":["/ip4/203.0.113.9/tcp/4001"]`))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		})

		It("answers 403 to a peer that is not paired", func() {
			svc.err = ErrForbidden
			rec := call(RoutesEndpoint(svc), http.MethodGet, "", overP2P)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})
	})

	Describe("device session", func() {
		overP2P := WithPeer(context.Background(), "12D3KooWpeer")

		It("answers 404 to a request that did not arrive over P2P", func() {
			rec := call(DeviceSessionEndpoint(svc), http.MethodPost, `{"credential":"c"}`, context.Background())
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(svc.session[0]).To(BeEmpty())
		})

		It("sets the session cookie and answers JSON with the user", func() {
			rec := call(DeviceSessionEndpoint(svc), http.MethodPost, `{"credential":" c "}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(svc.session).To(Equal([2]string{"12D3KooWpeer", "c"}))
			Expect(rec.Header().Get("Set-Cookie")).To(ContainSubstring("session=tok"))
			Expect(rec.Header().Get("Set-Cookie")).To(ContainSubstring("HttpOnly"))
			Expect(rec.Body.String()).To(ContainSubstring(`"id":"alice"`))
			Expect(rec.Body.String()).ToNot(ContainSubstring("tok"))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		})

		It("redirects a form post to the start page", func() {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("credential=c")).WithContext(overP2P)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
			rec := httptest.NewRecorder()
			Expect(DeviceSessionEndpoint(svc)(e.NewContext(req, rec))).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusSeeOther))
			Expect(rec.Header().Get("Location")).To(Equal("/"))
			Expect(svc.session[1]).To(Equal("c"))
		})

		It("answers every refusal with the same 403 and no cookie", func() {
			svc.err = ErrSignInRefused
			rec := call(DeviceSessionEndpoint(svc), http.MethodPost, `{"credential":"c"}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(errorOf(rec)).To(Equal(ErrSignInRefused.Error()))
			Expect(rec.Header().Get("Set-Cookie")).To(BeEmpty())
		})

		It("answers 429 when the limiter refuses", func() {
			svc.err = ErrRateLimited
			rec := call(DeviceSessionEndpoint(svc), http.MethodPost, `{"credential":"c"}`, overP2P)
			Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		})
	})

	Describe("devices of an owner", func() {
		It("passes the owner filter and shows the owner fields", func() {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/?owner=%20alice%20", nil)
			rec := httptest.NewRecorder()
			Expect(ListDevicesEndpoint(svc)(e.NewContext(req, rec))).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(svc.owner).To(Equal("alice"))
			Expect(rec.Body.String()).To(ContainSubstring(`"owner_id":"alice"`))
			Expect(rec.Body.String()).To(ContainSubstring(`"owner_name":"Alice"`))
			Expect(rec.Body.String()).To(ContainSubstring(`"sign_in":true`))
		})
	})

	Describe("user pairing setting", func() {
		It("reads it", func() {
			rec := call(UserPairingEndpoint(svc), http.MethodGet, "", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring(`"max_devices":5`))
		})

		It("passes only the fields that were given", func() {
			rec := call(SetUserPairingEndpoint(svc), http.MethodPut, `{"max_devices":3}`, nil)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(svc.setPair.MaxDevices).ToNot(BeNil())
			Expect(*svc.setPair.MaxDevices).To(Equal(3))
			Expect(svc.setPair.Relays).To(BeNil())
			Expect(svc.setPair.MaxPending).To(BeNil())
		})

		It("answers 400 for a value out of range", func() {
			svc.err = fmt.Errorf("%w: max_devices must be between 1 and 100", ErrInvalid)
			rec := call(SetUserPairingEndpoint(svc), http.MethodPut, `{"max_devices":0}`, nil)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("routes of a user", func() {
		It("reads only the label and the life of an invite", func() {
			rec := call(MintMyInviteEndpoint(svc), http.MethodPost, `{"label":" phone ","ttl_seconds":600,"kind":"remote","relays":["r1"],"models":["m"],"all_models":true,"sign_in":false}`, nil)
			Expect(rec.Code).To(Equal(http.StatusCreated))
			Expect(svc.myMint).To(Equal(MyMintRequest{Label: "phone", TTLSeconds: 600}))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		})

		It("serves the other routes", func() {
			Expect(call(MyPairingEndpoint(svc), http.MethodGet, "", nil).Code).To(Equal(http.StatusOK))
			Expect(call(ListMyInvitesEndpoint(svc), http.MethodGet, "", nil).Code).To(Equal(http.StatusOK))
			Expect(call(CancelMyInviteEndpoint(svc), http.MethodDelete, "", nil, "id", "inv").Code).To(Equal(http.StatusOK))
			Expect(svc.myID).To(Equal("inv"))
			Expect(call(ListMyDevicesEndpoint(svc), http.MethodGet, "", nil).Code).To(Equal(http.StatusOK))
			Expect(call(RenameMyDeviceEndpoint(svc), http.MethodPatch, `{"label":" tablet "}`, nil, "id", "dev").Code).To(Equal(http.StatusOK))
			Expect(svc.renamed).To(Equal([2]string{"dev", "tablet"}))
			Expect(call(RevokeMyDeviceEndpoint(svc), http.MethodDelete, "", nil, "id", "dev").Code).To(Equal(http.StatusOK))
		})

		It("maps a limit to 429 and a missing account to 409 needs_account", func() {
			svc.err = fmt.Errorf("%w: you have 2 pending invites", ErrLimitReached)
			rec := call(MintMyInviteEndpoint(svc), http.MethodPost, `{}`, nil)
			Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
			Expect(errorOf(rec)).To(ContainSubstring("2 pending invites"))

			svc.err = ErrPairNeedsAccount
			rec = call(MyPairingEndpoint(svc), http.MethodGet, "", nil)
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(rec.Body.String()).To(ContainSubstring(`"type":"needs_account"`))
		})

		It("answers 404 for an invite or device of another user", func() {
			svc.err = ErrNotFound
			Expect(call(CancelMyInviteEndpoint(svc), http.MethodDelete, "", nil, "id", "x").Code).To(Equal(http.StatusNotFound))
			Expect(call(RevokeMyDeviceEndpoint(svc), http.MethodDelete, "", nil, "id", "x").Code).To(Equal(http.StatusNotFound))
		})
	})
})
