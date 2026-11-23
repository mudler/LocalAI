package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay"
)

// captureStdout returns what fn prints to os.Stdout.
func captureStdout(fn func()) string {
	r, w, err := os.Pipe()
	Expect(err).ToNot(HaveOccurred())
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	func() {
		defer func() { os.Stdout = old }()
		fn()
	}()
	_ = w.Close()
	return <-done
}

func runP2P(args ...string) (string, error) {
	ctx, err := parseCLI(append([]string{"p2p"}, args...)...)
	if err != nil {
		return "", err
	}
	var runErr error
	out := captureStdout(func() { runErr = ctx.Run(&cliContext.Context{}) })
	return out, runErr
}

var _ = Describe("local-ai p2p relay flags", func() {
	relayConfig := func(args ...string) (p2prelay.RunConfig, error) {
		ctx, err := parseCLI(append([]string{"p2p", "relay", "--data-dir", GinkgoT().TempDir()}, args...)...)
		Expect(err).ToNot(HaveOccurred())
		cmd := ctx.Selected().Target.Addr().Interface().(*P2PRelayCMD)
		return cmd.Config()
	}

	It("starts a gated relay without a bandwidth limit", func() {
		cfg, err := relayConfig()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Open).To(BeFalse())
		Expect(cfg.RateLimit).To(BeZero())
		Expect(cfg.CircuitMaxBytes).To(Equal(p2prelay.DefaultCircuitMaxBytes))
		Expect(cfg.CircuitMaxDuration).To(Equal(p2prelay.DefaultCircuitMaxDuration))
		Expect(cfg.Port).To(Equal(4001))
		Expect(cfg.PendingTTL).To(Equal(p2prelay.DefaultPendingTTL))
	})

	It("gives an open relay the 64 KiB/s default", func() {
		cfg, err := relayConfig("--open")
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(Equal(p2prelay.DefaultOpenRateLimit))
	})

	It("removes the limit of an open relay with --rate-limit=0", func() {
		cfg, err := relayConfig("--open", "--rate-limit=0")
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(BeZero())
	})

	It("reads limits, caps and the public address", func() {
		cfg, err := relayConfig("--rate-limit=1000", "--total-rate-limit=5000", "--circuit-max-bytes=10", "--circuit-max-duration=1m",
			"--max-circuits=3", "--max-reservations=4", "--max-reservations-per-ip=2", "--max-circuits-per-peer=1",
			"--max-conns-per-ip=5", "--conn-rate-per-ip=1.5", "--conn-burst-per-ip=9", "--conn-rate-total=50", "--conn-burst-total=70", "--pending-ttl=2m", "--public-ip=203.0.113.9", "--json")
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(BeEquivalentTo(1000))
		Expect(cfg.TotalRateLimit).To(BeEquivalentTo(5000))
		Expect(cfg.CircuitMaxBytes).To(BeEquivalentTo(10))
		Expect(cfg.CircuitMaxDuration).To(Equal(time.Minute))
		Expect(cfg.MaxCircuits).To(Equal(3))
		Expect(cfg.MaxReservations).To(Equal(4))
		Expect(cfg.MaxReservationsPerIP).To(Equal(2))
		Expect(cfg.MaxCircuitsPerPeer).To(Equal(1))
		Expect(cfg.MaxConnsPerIP).To(Equal(5))
		Expect(cfg.ConnRatePerIP).To(Equal(1.5))
		Expect(cfg.ConnBurstPerIP).To(Equal(9))
		Expect(cfg.ConnRateTotal).To(Equal(50.0))
		Expect(cfg.ConnBurstTotal).To(Equal(70))
		Expect(cfg.PendingTTL).To(Equal(2 * time.Minute))
		Expect(cfg.PublicIP.String()).To(Equal("203.0.113.9"))
		Expect(cfg.JSON).To(BeTrue())
	})

	It("takes admins from the flag and from LOCALAI_RELAY_ADMINS", func() {
		id1, id2 := newPeerIDText(), newPeerIDText()
		cfg, err := relayConfig("--admin", id1)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Admins).To(HaveLen(1))

		GinkgoT().Setenv("LOCALAI_RELAY_ADMINS", id1+","+id2)
		cfg, err = relayConfig()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Admins).To(HaveLen(2))
	})

	It("rejects bad input and the combination of --open and --admin", func() {
		_, err := relayConfig("--admin", "not-a-peer")
		Expect(err).To(MatchError(ContainSubstring("not a peer ID")))
		_, err = relayConfig("--public-ip", "::1")
		Expect(err).To(MatchError(ContainSubstring("--public-ip")))
		_, err = relayConfig("--announce", "nonsense")
		Expect(err).To(HaveOccurred())
		_, err = relayConfig("--open", "--admin", newPeerIDText())
		Expect(err).To(MatchError(ContainSubstring("exclude each other")))
	})
})

func newPeerIDText() string {
	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "k")
	_, err := p2pnet.LoadOrCreateIdentity(path)
	Expect(err).ToNot(HaveOccurred())
	id, err := p2pnet.IdentityPeerID(path)
	Expect(err).ToNot(HaveOccurred())
	return id.String()
}

var _ = Describe("local-ai p2p relays", func() {
	var (
		srv      *httptest.Server
		requests []string
		bodies   []string
		auth     string
		relays   []map[string]any
		checkOut map[string]any
		status   = http.StatusOK
	)
	BeforeEach(func() {
		requests, bodies, auth = nil, nil, ""
		status = http.StatusOK
		relays = []map[string]any{{"id": "12D3KooWrelayone", "label": "home", "addr": "/ip4/203.0.113.7/tcp/4001/p2p/12D3KooWrelayone", "state": "in_sync", "mode": "gated", "members": 2}}
		checkOut = map[string]any{"connected": true, "detail": []string{"connect: ok"}, "relay": map[string]any{"id": "12D3KooWrelayone", "state": "in_sync"}}
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			requests = append(requests, r.Method+" "+r.URL.Path)
			bodies = append(bodies, string(b))
			auth = r.Header.Get("Authorization")
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
				return
			}
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/p2p/relays":
				_ = json.NewEncoder(w).Encode(map[string]any{"relays": relays})
			case strings.HasSuffix(r.URL.Path, "/check"):
				_ = json.NewEncoder(w).Encode(checkOut)
			case r.Method == http.MethodDelete:
				_, _ = w.Write([]byte(`{}`))
			default:
				_ = json.NewEncoder(w).Encode(relays[0])
			}
		}))
		DeferCleanup(srv.Close)
	})

	It("lists relays, sending the API key", func() {
		out, err := runP2P("relays", "list", "--server", srv.URL, "--api-key", "k1")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring("in_sync"))
		Expect(out).To(ContainSubstring("home"))
		Expect(auth).To(Equal("Bearer k1"))
	})

	It("reads the key from LOCALAI_API_KEY and prints JSON", func() {
		GinkgoT().Setenv("LOCALAI_API_KEY", "envkey")
		out, err := runP2P("relays", "list", "--server", srv.URL, "--json")
		Expect(err).ToNot(HaveOccurred())
		Expect(auth).To(Equal("Bearer envkey"))
		Expect(json.Valid([]byte(out))).To(BeTrue(), out)
	})

	It("adds a relay with a label and a claim code", func() {
		out, err := runP2P("relays", "add", "/ip4/203.0.113.7/tcp/4001/p2p/12D3KooWrelayone", "--label", "home", "--claim", "AAAA-BBBB-CCCC", "--server", srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(requests).To(ConsistOf("POST /api/p2p/relays"))
		var body map[string]string
		Expect(json.Unmarshal([]byte(bodies[0]), &body)).To(Succeed())
		Expect(body).To(Equal(map[string]string{"addr": "/ip4/203.0.113.7/tcp/4001/p2p/12D3KooWrelayone", "label": "home", "claim_code": "AAAA-BBBB-CCCC"}))
		Expect(out).To(ContainSubstring("state:   in_sync"))
	})

	It("removes a relay by its label, address or ID", func() {
		for _, ref := range []string{"home", "12D3KooWrelayone", relays[0]["addr"].(string)} {
			requests = nil
			_, err := runP2P("relays", "remove", ref, "--server", srv.URL)
			Expect(err).ToNot(HaveOccurred(), ref)
			Expect(requests).To(Equal([]string{"GET /api/p2p/relays", "DELETE /api/p2p/relays/12D3KooWrelayone"}))
		}
		_, err := runP2P("relays", "remove", "other", "--server", srv.URL)
		Expect(err).To(MatchError(ContainSubstring(`no relay "other"`)))
	})

	It("checks, syncs and claims", func() {
		out, err := runP2P("relays", "check", "--server", srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring("connect: ok"))
		Expect(requests).To(ContainElement("POST /api/p2p/relays/12D3KooWrelayone/check"))

		_, err = runP2P("relays", "sync", "home", "--server", srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(requests).To(ContainElement("POST /api/p2p/relays/12D3KooWrelayone/sync"))

		_, err = runP2P("relays", "claim", "home", "CODE", "--server", srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(requests).To(ContainElement("POST /api/p2p/relays/12D3KooWrelayone/claim"))
		Expect(bodies[len(bodies)-1]).To(MatchJSON(`{"code":"CODE"}`))
	})

	It("exits with status 1 when a checked relay is not in sync", func() {
		checkOut["relay"] = map[string]any{"id": "12D3KooWrelayone", "state": "unreachable"}
		out, err := runP2P("relays", "check", "home", "--server", srv.URL)
		Expect(err).To(MatchError(ExitCodeError{Code: 1}))
		Expect(out).To(ContainSubstring("firewall"))
	})

	It("explains an authentication failure", func() {
		status = http.StatusUnauthorized
		_, err := runP2P("relays", "list", "--server", srv.URL)
		Expect(err).To(MatchError(ContainSubstring("--api-key")))
	})

	It("explains an unreachable server", func() {
		url := srv.URL
		srv.Close()
		_, err := runP2P("relays", "list", "--server", url)
		Expect(err).To(MatchError(ContainSubstring("cannot reach the server")))
	})

	It("prints the status of the server", func() {
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/api/p2p/status"))
			_, _ = w.Write([]byte(`{"mode":"share","peer_id":"12D3KooWabc","reachability":"private","addrs":["/ip4/1.2.3.4/tcp/1"]}`))
		})
		out, err := runP2P("status", "--server", srv.URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring("mode:         share"))
		Expect(out).To(ContainSubstring("12D3KooWabc"))
	})
})

var _ = Describe("local-ai p2p id", func() {
	It("prints the peer ID of the identity file and never creates one", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "p2p", "identity.key")
		_, err := runP2P("id", "--data-path", dir)
		Expect(err).To(MatchError(ContainSubstring("no identity")))
		_, statErr := os.Stat(path)
		Expect(os.IsNotExist(statErr)).To(BeTrue())

		_, err = p2pnet.LoadOrCreateIdentity(path)
		Expect(err).ToNot(HaveOccurred())
		want, err := p2pnet.IdentityPeerID(path)
		Expect(err).ToNot(HaveOccurred())

		out, err := runP2P("id", "--data-path", dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.TrimSpace(out)).To(Equal(want.String()))

		out, err = runP2P("id", "--identity", path, "--json")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring(want.String()))
	})
})

var _ = Describe("local-ai p2p pair", func() {
	validLink := func(mod func(*p2ppair.Link)) string {
		id := newPeerIDText()
		pid, err := p2pnet.ParsePeerID(id)
		Expect(err).ToNot(HaveOccurred())
		l := &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: pid, Code: "ABCDEFGHJK", Expires: time.Now().Add(time.Hour), Label: "x"}
		mod(l)
		return l.String()
	}

	It("refuses a bad link and an expired link", func() {
		_, err := runP2P("pair", "--invite", "https://example.com")
		Expect(err).To(MatchError(p2ppair.ErrBadLink))
		_, err = runP2P("pair", "--invite", validLink(func(l *p2ppair.Link) { l.Expires = time.Now().Add(-time.Minute) }))
		Expect(err).To(MatchError(ContainSubstring("expired")))
	})

	It("redeems over P2P, shows the phrase and saves only with an identity", func() {
		serverDir := GinkgoT().TempDir()
		gate := p2pnet.NewAllowlist()
		gate.SetPairing(true)
		gate.AllowPairingProtocol(p2pnet.ProtoHTTP)
		server, err := p2pnet.New(GinkgoT().Context(), p2pnet.Config{
			IdentityPath: filepath.Join(serverDir, "id.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			Gate:         gate,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(server.Close)
		ln, err := server.Listen(p2pnet.ProtoHTTP, nil)
		Expect(err).ToNot(HaveOccurred())
		var got map[string]any
		mux := http.NewServeMux()
		mux.HandleFunc("/api/p2p/redeem", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"device_id":"dev-1","kind":"device","phrase":"red fox lamp tide","server":{"peer_id":"` + server.ID().String() + `","name":"my server","version":"1"}}`))
		})
		hs := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
		go func() { _ = hs.Serve(ln) }()
		DeferCleanup(hs.Close)

		link := &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: server.ID(), Code: "ABCDEFGHJK", Expires: time.Now().Add(time.Hour), Label: "x"}
		link.Addrs = append(link.Addrs, server.Libp2p().Addrs()...)

		out, err := runP2P("pair", "--invite", link.String(), "--name", "laptop")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring("red fox lamp tide"))
		Expect(out).To(ContainSubstring("Paired with my server"))
		Expect(got["code"]).To(Equal("ABCDEFGHJK"))
		Expect(got["name"]).To(Equal("laptop"))
		Expect(got["v"]).To(BeEquivalentTo(1))

		dir := GinkgoT().TempDir()
		out, err = runP2P("pair", "--invite", link.String(), "--data-path", dir, "--json")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring(`"phrase":"red fox lamp tide"`))
		rec := filepath.Join(dir, "p2p", "pairs", server.ID().String()+".json")
		st, err := os.Stat(rec)
		Expect(err).ToNot(HaveOccurred())
		if runtime.GOOS != "windows" {
			// Windows has no owner-only file mode to check.
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		}
		_, err = p2pnet.IdentityPeerID(filepath.Join(dir, "p2p", "identity.key"))
		Expect(err).ToNot(HaveOccurred())
	})

	It("refuses an invite that names an open relay unless told otherwise", func() {
		link := validLink(func(l *p2ppair.Link) {
			ai, err := p2pnet.ParseAddrInfo("/ip4/203.0.113.7/tcp/4001/p2p/" + newPeerIDText())
			Expect(err).ToNot(HaveOccurred())
			l.Relays = append(l.Relays, ai)
			l.Open = []int{0}
		})
		_, err := runP2P("pair", "--invite", link)
		Expect(err).To(MatchError(ContainSubstring("--allow-open-relay")))
	})
})

var _ = Describe("local-ai p2p forward", func() {
	It("needs a paired identity", func() {
		_, err := runP2P("forward", "--peer", newPeerIDText(), "--listen", "127.0.0.1:0", "--data-path", GinkgoT().TempDir())
		Expect(err).To(MatchError(ContainSubstring("pair this machine first")))
	})

	It("rejects a bad peer ID", func() {
		_, err := runP2P("forward", "--peer", "nope", "--listen", "127.0.0.1:0")
		Expect(err).To(MatchError(ContainSubstring("not a peer ID")))
	})
})
