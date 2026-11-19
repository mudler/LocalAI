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
	renamed  [2]string
	regen    int
	limits   SetRelayLimitsRequest
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
func (f *fakeService) ListDevices(context.Context) ([]DeviceView, error) {
	return []DeviceView{{ID: "dev", Status: "active", Path: PathView{Kind: "direct"}}}, f.err
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
	})
})
