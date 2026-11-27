//go:build auth

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// shareServer is an application in mode share with an auth database, serving
// a main handler that answers "main" and the real redeem handler over P2P.
func shareServer() (*Application, *p2pRuntime) {
	return shareServerWith(func(*Application) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "main")
		})
	})
}

// shareServerWith is shareServer with the main handler that main returns.
func shareServerWith(main func(*Application) http.Handler) (*Application, *p2pRuntime) {
	db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
	Expect(err).ToNot(HaveOccurred())
	a := newP2PTestApp("share", GinkgoT().TempDir(), db)
	only := echo.New()
	only.POST(redeemPath, p2papi.RedeemEndpoint(a.P2P()))
	only.GET(routesPath, p2papi.RoutesEndpoint(a.P2P()))
	only.POST(deviceSessionPath, p2papi.DeviceSessionEndpoint(a.P2PUsers()))
	a.ServeP2PHTTP(main(a), only)
	Expect(a.StartP2PNet()).To(Succeed())
	rt, _ := a.p2p().runtime()
	Expect(rt).ToNot(BeNil())
	return a, rt
}

// device is a second host, like a laptop that pairs with the server.
func device(server *p2pRuntime) (*p2pnet.Host, *http.Client) {
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	ai := p2pnet.AddrInfo{ID: server.host.ID(), Addrs: server.host.Libp2p().Addrs()}
	c := h.HTTPClient(ai.ID, p2pnet.WithAddrs(ai.Addrs...))
	c.Timeout = 10 * time.Second
	return h, c
}

func get(c *http.Client, server *p2pRuntime, path string) (int, string, error) {
	resp, err := c.Get("http://" + server.host.ID().String() + path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

func redeemCode(c *http.Client, server *p2pRuntime, code string) (int, map[string]any) {
	body, _ := json.Marshal(map[string]any{"v": 1, "code": code, "name": "laptop", "platform": "linux"})
	resp, err := c.Post("http://"+server.host.ID().String()+redeemPath, "application/json", bytes.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func codeOf(link string) string {
	l, err := p2ppair.ParseLink(link)
	Expect(err).ToNot(HaveOccurred())
	return l.Code
}

var _ = Describe("P2P layer in mode share", func() {
	It("listens, admits strangers only to redeem, and admits a paired device", func() {
		a, server := shareServer()
		svc := a.P2P()
		ctx := context.Background()
		Expect(server.srv).ToNot(BeNil())
		Expect(transportListenAddrs(server)).ToNot(BeEmpty())

		_, client := device(server)
		By("refusing a stranger at the handshake while no invite is pending")
		_, _, err := get(client, server, "/api/models")
		Expect(err).To(HaveOccurred())

		By("minting an invite, which opens pairing")
		m, err := svc.MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "device", Label: "laptop", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(m.QR).To(BeTrue())
		Expect(m.Warnings).To(ContainElement("no_relay"))

		By("letting the stranger call only the redeem route")
		code, _, err := get(client, server, "/api/models")
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusForbidden))

		By("answering a wrong code with 410")
		status, _ := redeemCode(client, server, "AAAAAAAAAA")
		Expect(status).To(Equal(http.StatusGone))

		By("redeeming the right code")
		status, out := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		Expect(out["phrase"]).ToNot(BeEmpty())
		Expect(out).ToNot(HaveKey("client_key"))
		Expect(out["server"]).To(HaveKeyWithValue("peer_id", server.host.ID().String()))

		By("serving the paired device the whole handler")
		code, body, err := get(client, server, "/api/models")
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK))
		Expect(body).To(Equal("main"))

		By("listing the device with its path")
		devs, err := svc.ListDevices(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].Online).To(BeTrue())
		Expect(devs[0].Path.Kind).To(Equal("direct"))
		invs, err := svc.ListInvites(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(invs[0].Status).To(Equal("used"))
		By("listing the used invite with the phrase the device was shown")
		Expect(invs[0].Phrase).To(Equal(out["phrase"]))
		Expect(invs[0].DevicePeerID).To(Equal(devs[0].PeerID))
		Expect(invs[0].RelayPass).To(BeEmpty())

		By("closing pairing once no invite is pending")
		Expect(server.gate.AdmitConn("stranger")).To(BeFalse())

		By("revoking the device, which cuts it off")
		Expect(svc.RevokeDevice(ctx, devs[0].ID, "admin")).To(Succeed())
		client.CloseIdleConnections()
		Eventually(func() error {
			_, _, err := get(client, server, "/api/models")
			return err
		}).Should(HaveOccurred())
	})

	It("redeems an invite over its local addresses alone: no relay, no discovery, no addresses given by hand", func() {
		a, server := shareServer()
		// The test host listens on loopback, which stands for the private
		// address of a real machine.
		a.p2p().isLANAddr = func(m p2pnet.Multiaddr) bool {
			ip, err := p2pnet.AddrIP(m)
			return err == nil && ip.IsLoopback()
		}

		m, err := a.P2P().MintInvite(context.Background(), "admin", p2papi.MintRequest{Kind: "device", Label: "laptop", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		link, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		Expect(link.Addrs).ToNot(BeEmpty())
		Expect(link.Relays).To(BeEmpty())

		By("redeeming from a host that has no listener, no discovery and only the link")
		h, err := p2pnet.New(context.Background(), p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"), ListenAddrs: []string{},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(h.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		res, err := pairclient.Redeem(ctx, h, link, pairclient.Request{Name: "laptop", Platform: "linux"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Phrase).ToNot(BeEmpty())
		Expect(res.Server.PeerID).To(Equal(server.host.ID().String()))
	})

	It("gives the routes of a link to a paired device only, over P2P", func() {
		a, server := shareServer()
		a.p2p().isLANAddr = func(m p2pnet.Multiaddr) bool {
			ip, err := p2pnet.AddrIP(m)
			return err == nil && ip.IsLoopback()
		}
		ctx := context.Background()
		m, err := a.P2P().MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "device", Label: "laptop", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		link, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		_, client := device(server)

		By("refusing a peer that is admitted only to redeem")
		code, _, err := get(client, server, routesPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusForbidden))

		status, _ := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))

		By("answering the paired device with the lists of a link, without code or pass")
		code, body, err := get(client, server, routesPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK), body)
		var r p2papi.Routes
		Expect(json.Unmarshal([]byte(body), &r)).To(Succeed())
		Expect(r.Server).To(Equal(server.host.ID().String()))
		Expect(r.Addrs).To(Equal(link.AddrStrings()))
		Expect(r.Relays).To(BeEmpty())
		Expect(r.Hints).To(BeEmpty(), "the public DHT is off: no public relay")
		Expect(body).ToNot(ContainSubstring(link.Code))

		By("refusing another method")
		resp, err := client.Post("http://"+server.host.ID().String()+routesPath, "application/json", nil)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))

		By("refusing the service call for a peer that is not paired")
		_, err = a.P2P().Routes(ctx, hintPeer().String())
		Expect(err).To(MatchError(p2papi.ErrForbidden))
	})

	It("leaves the local addresses out of an invite that asks for it", func() {
		a, _ := shareServer()
		a.p2p().isLANAddr = func(p2pnet.Multiaddr) bool { return true }
		m, err := a.P2P().MintInvite(context.Background(), "admin", p2papi.MintRequest{Kind: "device", Label: "laptop", Relays: p2papi.RelayChoice{Mode: "none"}, OmitLANAddrs: true})
		Expect(err).ToNot(HaveOccurred())
		link, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		Expect(link.Addrs).To(BeEmpty())
		Expect(m.Warnings).To(ContainElement("no_public_addr"))
	})

	It("issues a client key for a remote invite and revokes it with the device", func() {
		a, server := shareServer()
		svc := a.P2P()
		ctx := context.Background()
		owner := &auth.User{ID: "owner-1", Name: "Owner", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(a.AuthDB().Create(owner).Error).To(Succeed())

		m, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", Label: "office", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		_, client := device(server)
		status, out := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		Expect(out).To(HaveKey("client_key"))
		keyID := out["client_key"].(map[string]any)["id"].(string)
		Expect(auth.ClientKeyActive(a.AuthDB(), keyID, owner.ID)).To(BeTrue())

		By("giving the key of an invite without models every model a client key may use")
		var key auth.UserAPIKey
		Expect(a.AuthDB().First(&key, "id = ?", keyID).Error).To(Succeed())
		Expect(key.ClientAllows(auth.FeatureClientInference)).To(BeTrue())
		Expect(key.AllModels).To(BeTrue())
		Expect(key.Models).To(BeEmpty())
		Expect(key.AllowsModel("added-later")).To(BeTrue())

		devs, err := svc.ListDevices(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(svc.RevokeDevice(ctx, devs[0].ID, owner.ID)).To(Succeed())
		Expect(auth.ClientKeyActive(a.AuthDB(), keyID, owner.ID)).To(BeFalse())
	})

	It("limits the key of a remote invite to the models it lists", func() {
		a, server := shareServer()
		models := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(models, "shared.yaml"), []byte("name: shared\nbackend: llama-cpp\n"), 0o600)).To(Succeed())
		a.backendLoader = config.NewModelConfigLoader(models)
		Expect(a.backendLoader.LoadModelConfigsFromPath(models)).To(Succeed())
		svc := a.P2P()
		ctx := context.Background()
		owner := &auth.User{ID: "owner-1", Name: "Owner", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(a.AuthDB().Create(owner).Error).To(Succeed())

		By("refusing an unknown model when the invite is made, not when it is redeemed")
		_, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", Models: []string{"missing"}, Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).To(MatchError(p2papi.ErrInvalid))
		Expect(err.Error()).To(ContainSubstring("missing"))

		m, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", Models: []string{"shared"}, Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		_, client := device(server)
		status, out := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		var key auth.UserAPIKey
		Expect(a.AuthDB().First(&key, "id = ?", out["client_key"].(map[string]any)["id"]).Error).To(Succeed())
		Expect(key.AllModels).To(BeFalse())
		Expect(key.Models).To(Equal(auth.ModelList{"shared"}))
		Expect(key.AllowsModel("shared")).To(BeTrue())
		Expect(key.AllowsModel("other")).To(BeFalse())
	})

	It("grants every model on an explicit all_models invite and shows what each invite and device got", func() {
		a, server := shareServer()
		models := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(models, "shared.yaml"), []byte("name: shared\nbackend: llama-cpp\n"), 0o600)).To(Succeed())
		a.backendLoader = config.NewModelConfigLoader(models)
		Expect(a.backendLoader.LoadModelConfigsFromPath(models)).To(Succeed())
		svc := a.P2P()
		ctx := context.Background()
		owner := &auth.User{ID: "owner-1", Name: "Owner", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(a.AuthDB().Create(owner).Error).To(Succeed())
		none := p2papi.RelayChoice{Mode: "none"}

		By("refusing all_models together with a list of models")
		_, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", AllModels: true, Models: []string{"shared"}, Relays: none})
		Expect(err).To(MatchError(p2papi.ErrInvalid))
		Expect(err.Error()).To(ContainSubstring("all_models"))

		By("giving the key of an explicit all_models invite every model")
		m, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", Label: "everything", AllModels: true, Relays: none})
		Expect(err).ToNot(HaveOccurred())
		_, client := device(server)
		status, out := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		var key auth.UserAPIKey
		Expect(a.AuthDB().First(&key, "id = ?", out["client_key"].(map[string]any)["id"]).Error).To(Succeed())
		Expect(key.AllModels).To(BeTrue())
		Expect(key.Models).To(BeEmpty())

		By("listing an invite with no list as all models, so old callers read the same")
		legacy, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", Label: "legacy", Relays: none})
		Expect(err).ToNot(HaveOccurred())
		picked, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "remote", Label: "picked", Models: []string{"shared"}, Relays: none})
		Expect(err).ToNot(HaveOccurred())
		dev, err := svc.MintInvite(ctx, owner.ID, p2papi.MintRequest{Kind: "device", Label: "phone", Relays: none})
		Expect(err).ToNot(HaveOccurred())
		invs, err := svc.ListInvites(ctx)
		Expect(err).ToNot(HaveOccurred())
		byID := map[string]p2papi.InviteView{}
		for _, i := range invs {
			byID[i.ID] = i
		}
		Expect(byID[legacy.ID].AllModels).To(BeTrue())
		Expect(byID[legacy.ID].Models).To(BeEmpty())
		Expect(byID[picked.ID].AllModels).To(BeFalse())
		Expect(byID[picked.ID].Models).To(Equal([]string{"shared"}))
		Expect(byID[dev.ID].AllModels).To(BeFalse())
		Expect(byID[dev.ID].Models).To(BeEmpty())

		By("listing the device of the redeemed invite with the same grant")
		devs, err := svc.ListDevices(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].AllModels).To(BeTrue())
		Expect(devs[0].Models).To(BeEmpty())
	})

	It("refuses a remote invite for an identity without a user account, and still makes device invites for it", func() {
		a, server := shareServer()
		svc := a.P2P()
		ctx := context.Background()

		By("refusing the remote invite of the API key set with LOCALAI_API_KEY, which has no user row")
		for _, by := range []string{"legacy-api-key", ""} {
			_, err := svc.MintInvite(ctx, by, p2papi.MintRequest{Kind: "remote", Relays: p2papi.RelayChoice{Mode: "none"}})
			Expect(err).To(MatchError(p2papi.ErrNeedsAccount), by)
		}
		invs, err := svc.ListInvites(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(invs).To(BeEmpty(), "a refused invite is not stored")

		By("refusing it for a disabled account too")
		Expect(a.AuthDB().Create(&auth.User{ID: "gone", Name: "Gone", Email: "g@example.com", Role: auth.RoleAdmin, Status: auth.StatusDisabled, Provider: auth.ProviderLocal}).Error).To(Succeed())
		_, err = svc.MintInvite(ctx, "gone", p2papi.MintRequest{Kind: "remote", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).To(MatchError(p2papi.ErrNeedsAccount))

		By("making and redeeming a device invite for the same identity, which needs no account")
		m, err := svc.MintInvite(ctx, "legacy-api-key", p2papi.MintRequest{Kind: "device", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		_, client := device(server)
		status, out := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		Expect(out["kind"]).To(Equal("device"))
		Expect(out).ToNot(HaveKey("client_key"))
	})

	It("answers 429 after five wrong codes from one peer", func() {
		a, server := shareServer()
		_, err := a.P2P().MintInvite(context.Background(), "admin", p2papi.MintRequest{Kind: "device", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		_, client := device(server)
		for range 5 {
			status, _ := redeemCode(client, server, "AAAAAAAAAA")
			Expect(status).To(Equal(http.StatusGone))
		}
		status, _ := redeemCode(client, server, "AAAAAAAAAA")
		Expect(status).To(Equal(http.StatusTooManyRequests))
	})

	It("reloads the allowlist from the store, as after a revoke on another frontend", func() {
		a, server := shareServer()
		ctx := context.Background()
		m, err := a.P2P().MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "device", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		h, client := device(server)
		status, _ := redeemCode(client, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		Expect(server.gate.Allowed(h.ID())).To(BeTrue())

		devs, err := server.pair.ListDevices(ctx)
		Expect(err).ToNot(HaveOccurred())
		// The store alone changes, as when another frontend revokes.
		Expect(server.pair.Revoke(ctx, devs[0].ID, "other")).To(Succeed())
		Eventually(func() bool { return server.gate.Allowed(h.ID()) }).Should(BeFalse())
	})

	It("restarts into mode connect on a change of mode", func() {
		a, _ := shareServer()
		a.ApplicationConfig().P2P.Mode = "connect"
		Expect(a.RestartP2PNet()).To(Succeed())
		rt, _ := a.p2p().runtime()
		Expect(rt.srv).To(BeNil())
		Expect(rt.pair).To(BeNil())
	})
})

// fakeRelays is a relay manager that records the pairing changes and hands
// out passes.
type fakeRelays struct {
	mu      sync.Mutex
	added   []string
	removed []string
	passes  int
}

func (f *fakeRelays) List() []p2papi.Relay {
	return []p2papi.Relay{{ID: "r1", Label: "home", Mode: "gated", State: "in_sync",
		Addr: "/ip4/192.0.2.9/tcp/4001/p2p/12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN"}}
}
func (f *fakeRelays) Add(context.Context, string, string, string) (p2papi.Relay, error) {
	return p2papi.Relay{}, nil
}
func (f *fakeRelays) Remove(context.Context, string) error { return nil }
func (f *fakeRelays) Check(context.Context, string) (p2papi.RelayCheck, error) {
	return p2papi.RelayCheck{}, nil
}
func (f *fakeRelays) Sync(context.Context, string) (p2papi.Relay, error) { return p2papi.Relay{}, nil }
func (f *fakeRelays) Claim(context.Context, string, string) (p2papi.Relay, error) {
	return p2papi.Relay{}, nil
}
func (f *fakeRelays) Limits(context.Context, string) (p2papi.RelayLimitsInfo, error) {
	return p2papi.RelayLimitsInfo{}, nil
}
func (f *fakeRelays) SetLimits(context.Context, string, p2papi.SetRelayLimitsRequest) (p2papi.RelayLimitsInfo, error) {
	return p2papi.RelayLimitsInfo{}, nil
}
func (f *fakeRelays) Registrar() p2ppair.PassRegistrar { return f }
func (f *fakeRelays) RegisterPass(context.Context, p2ppair.PassRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passes++
	return "PASS", nil
}
func (f *fakeRelays) CancelPass(context.Context, p2ppair.Relay, string) error { return nil }
func (f *fakeRelays) Resync()                                                 {}
func (f *fakeRelays) NodeAdmission(string) []p2papi.SplitRelayAdmission {
	return nil
}
func (f *fakeRelays) PeerAdded(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, p)
}
func (f *fakeRelays) PeerRemoved(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, p)
}
func (f *fakeRelays) seen() (added, removed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.added), slices.Clone(f.removed)
}
func (f *fakeRelays) Start(context.Context) error { return nil }
func (f *fakeRelays) Close() error                { return nil }

var _ = Describe("P2P layer with a relay manager", func() {
	It("puts a relay pass in invites and tells the relays about pairing and revoking", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).ToNot(HaveOccurred())
		a := newP2PTestApp("share", GinkgoT().TempDir(), db)
		relays := &fakeRelays{}
		a.p2p().relayFactory = func(relayManagerConfig) (relayManager, error) { return relays, nil }
		redeem := echo.New()
		redeem.POST(redeemPath, p2papi.RedeemEndpoint(a.P2P()))
		a.ServeP2PHTTP(http.NotFoundHandler(), redeem)
		Expect(a.StartP2PNet()).To(Succeed())
		server, _ := a.p2p().runtime()
		ctx := context.Background()

		m, err := a.P2P().MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "device"})
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Relay.Pass).To(BeTrue())
		Expect(m.Relay.ID).To(Equal("r1"))
		l, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		Expect(l.Pass).To(Equal("PASS"))

		h, client := device(server)
		status, _ := redeemCode(client, server, l.Code)
		Expect(status).To(Equal(http.StatusOK))
		added, _ := relays.seen()
		Expect(added).To(Equal([]string{h.ID().String()}))

		devs, err := a.P2P().ListDevices(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(a.P2P().RevokeDevice(ctx, devs[0].ID, "admin")).To(Succeed())
		_, removed := relays.seen()
		Expect(removed).To(Equal([]string{h.ID().String()}))

		rs, err := a.P2P().ListRelays(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rs).To(HaveLen(1))
	})

	It("answers 503 for the relay admin without a relay manager", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).ToNot(HaveOccurred())
		a := newP2PTestApp("share", GinkgoT().TempDir(), db)
		a.p2p().relayFactory = nil
		Expect(a.StartP2PNet()).To(Succeed())
		_, err = a.P2P().CheckRelay(context.Background(), "r1")
		Expect(err).To(MatchError(p2papi.ErrUnavailable))
	})
})
