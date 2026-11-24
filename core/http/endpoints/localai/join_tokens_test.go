package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cluster"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The admin API of join tokens", func() {
	var (
		store    *cluster.JoinTokenStore
		settings *memorySettings
		e        *echo.Echo
	)

	do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		GinkgoHelper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	BeforeEach(func() {
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "join.db")+"?_busy_timeout=10000"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err = cluster.NewJoinTokenStore(db)
		Expect(err).ToNot(HaveOccurred())
		settings = &memorySettings{m: map[string]string{}}
		e = echo.New()
		e.POST("/api/cluster/join-tokens", MintJoinTokenEndpoint(store, settings))
		e.GET("/api/cluster/join-tokens", ListJoinTokensEndpoint(store))
		e.DELETE("/api/cluster/join-tokens/:id", RevokeJoinTokenEndpoint(store))
	})

	It("mints a token with the defaults, shows it once, and builds the commands from the request host", func() {
		rec := do(http.MethodPost, "/api/cluster/join-tokens", `{}`, nil)
		Expect(rec.Code).To(Equal(http.StatusCreated))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		var out MintJoinTokenResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Token).To(HavePrefix("ljt_"))
		Expect(out.MaxUses).To(Equal(1))
		Expect(out.AutoApprove).To(BeFalse())
		Expect(out.State).To(Equal("unused"))
		Expect(out.ExpiresAt.Sub(out.CreatedAt).Minutes()).To(BeNumerically("~", 10, 0.01))
		Expect(out.Commands.InstallScript).To(ContainSubstring("http://example.com/join.sh?t=" + out.Token))
		Expect(out.Commands.Kubernetes).To(ContainSubstring("http://example.com/join.yaml?t=" + out.Token))
		Expect(out.Commands.Windows).To(ContainSubstring("join.ps1"))
		Expect(out.Commands.Docker).To(ContainSubstring("LOCALAI_JOIN_TOKEN='" + out.Token + "'"))
		Expect(out.Commands.Docker).To(ContainSubstring("LOCALAI_REGISTER_TO='http://example.com'"))
		Expect(out.Commands.DockerCompose).To(ContainSubstring("LOCALAI_JOIN_TOKEN: \"" + out.Token + "\""))
		Expect(out.Commands.Manual).To(ContainSubstring("local-ai worker"))
	})

	It("does not show the secret again in the list", func() {
		rec := do(http.MethodPost, "/api/cluster/join-tokens", `{"label":"rack 3"}`, nil)
		var minted MintJoinTokenResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &minted)).To(Succeed())
		secret := minted.Token[strings.Index(minted.Token, ".")+1:]

		list := do(http.MethodGet, "/api/cluster/join-tokens", "", nil)
		Expect(list.Code).To(Equal(http.StatusOK))
		Expect(list.Body.String()).ToNot(ContainSubstring(secret))
		var rows []JoinTokenView
		Expect(json.Unmarshal(list.Body.Bytes(), &rows)).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal(minted.ID))
		Expect(rows[0].Label).To(Equal("rack 3"))
		Expect(rows[0].CreatedBy).To(Equal("admin"))
	})

	It("takes limits for Kubernetes and Compose and refuses the ones out of range", func() {
		rec := do(http.MethodPost, "/api/cluster/join-tokens", `{"uses":5,"ttl_seconds":86400,"routes":["websocket"]}`, nil)
		Expect(rec.Code).To(Equal(http.StatusCreated))
		var out MintJoinTokenResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.MaxUses).To(Equal(5))
		Expect(out.Routes).To(Equal([]string{"websocket"}))
		Expect(out.Commands.InstallScript).To(ContainSubstring("&routes=websocket"))
		Expect(out.Commands.Docker).To(ContainSubstring("LOCALAI_WORKER_ROUTES='tunnel'"))

		for _, bad := range []string{`{"ttl_seconds":90000}`, `{"uses":101}`, `{"routes":["p2p"]}`, `{"ttl_seconds":-1}`} {
			Expect(do(http.MethodPost, "/api/cluster/join-tokens", bad, nil).Code).To(Equal(http.StatusBadRequest), bad)
		}
	})

	It("keeps auto-approve off unless the request or the cluster setting turns it on", func() {
		var out MintJoinTokenResponse
		rec := do(http.MethodPost, "/api/cluster/join-tokens", `{}`, nil)
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.AutoApprove).To(BeFalse())

		rec = do(http.MethodPost, "/api/cluster/join-tokens", `{"auto_approve":true}`, nil)
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.AutoApprove).To(BeTrue())

		settings.m[cluster.SettingJoinAutoApprove] = "true"
		rec = do(http.MethodPost, "/api/cluster/join-tokens", `{}`, nil)
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.AutoApprove).To(BeTrue(), "the cluster default applies when the request is silent")
		rec = do(http.MethodPost, "/api/cluster/join-tokens", `{"auto_approve":false}`, nil)
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.AutoApprove).To(BeFalse())
	})

	It("honours the forwarding headers only through the base URL helper", func() {
		// No trusted proxy is configured, so the forwarded host is ignored.
		rec := do(http.MethodPost, "/api/cluster/join-tokens", `{}`, map[string]string{"X-Forwarded-Host": "evil.example", "X-Forwarded-Proto": "https"})
		var out MintJoinTokenResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Commands.InstallScript).ToNot(ContainSubstring("evil.example"))
	})

	It("revokes a token and says so in the list", func() {
		rec := do(http.MethodPost, "/api/cluster/join-tokens", `{}`, nil)
		var minted MintJoinTokenResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &minted)).To(Succeed())

		Expect(do(http.MethodDelete, "/api/cluster/join-tokens/"+minted.ID, "", nil).Code).To(Equal(http.StatusOK))
		_, err := store.Consume(context.Background(), minted.Token)
		Expect(err).To(MatchError(cluster.ErrJoinTokenInvalid))

		var rows []JoinTokenView
		Expect(json.Unmarshal(do(http.MethodGet, "/api/cluster/join-tokens", "", nil).Body.Bytes(), &rows)).To(Succeed())
		Expect(rows[0].State).To(Equal("revoked"))
		Expect(do(http.MethodDelete, "/api/cluster/join-tokens/nope", "", nil).Code).To(Equal(http.StatusNotFound))
	})
})

var _ = Describe("BuildJoinCommands", func() {
	It("quotes the values so a crafted host cannot add a command", func() {
		cmds := BuildJoinCommands("http://h/'; rm -rf /; '", "ljt_x.y", nil)
		Expect(cmds.Docker).To(ContainSubstring(`'\''`))
		Expect(cmds.Manual).To(ContainSubstring(`'\''`))
	})

	It("names the routes of the worker, not the connections of the token", func() {
		cmds := BuildJoinCommands("https://ai.example.com/", "ljt_x.y", []string{"nats", "websocket"})
		Expect(cmds.Manual).To(ContainSubstring("LOCALAI_WORKER_ROUTES='nats,tunnel'"))
		Expect(cmds.InstallScript).To(ContainSubstring("https://ai.example.com/join.sh?t=ljt_x.y&routes=nats,websocket"))
	})
})
