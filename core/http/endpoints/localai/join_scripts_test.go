package localai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cluster"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The join scripts", func() {
	var (
		ctx     context.Context
		store   *cluster.JoinTokenStore
		limiter *cluster.JoinFailureLimiter
		e       *echo.Echo
	)

	get := func(path, token, extra string) *httptest.ResponseRecorder {
		GinkgoHelper()
		target := path + "?t=" + url.QueryEscape(token) + extra
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Host = "ai.example.com"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "join.db")+"?_busy_timeout=10000"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err = cluster.NewJoinTokenStore(db)
		Expect(err).ToNot(HaveOccurred())
		limiter = cluster.NewJoinFailureLimiter(5, time.Minute)
		e = echo.New()
		e.GET(JoinShellPath, JoinScriptEndpoint(store, limiter, JoinScriptShell))
		e.GET(JoinPowerShellPath, JoinScriptEndpoint(store, limiter, JoinScriptPowerShell))
		e.GET(JoinKubernetesPath, JoinScriptEndpoint(store, limiter, JoinScriptKubernetes))
	})

	It("serves each script for a valid token, with the token and the frontend in it, and spends nothing", func() {
		token, row, err := store.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(err).ToNot(HaveOccurred())

		sh := get(JoinShellPath, token, "")
		Expect(sh.Code).To(Equal(http.StatusOK))
		Expect(sh.Body.String()).To(HavePrefix("#!/bin/sh"))
		Expect(sh.Body.String()).To(ContainSubstring("LOCALAI_REGISTER_TO='http://ai.example.com'"))
		Expect(sh.Body.String()).To(ContainSubstring("LOCALAI_JOIN_TOKEN='" + token + "'"))
		Expect(sh.Body.String()).To(ContainSubstring("set -eu"))
		Expect(sh.Body.String()).ToNot(ContainSubstring("set -x"))
		Expect(sh.Body.String()).ToNot(MatchRegexp(`(?m)^\s*(echo|printf)[^\n]*JOIN_TOKEN`), "the script never prints the token")
		Expect(sh.Body.String()).ToNot(ContainSubstring("@@"), "every placeholder is filled")
		Expect(sh.Header().Get("Cache-Control")).To(Equal("no-store"))

		ps := get(JoinPowerShellPath, token, "")
		Expect(ps.Code).To(Equal(http.StatusOK))
		Expect(ps.Body.String()).To(ContainSubstring("$env:LOCALAI_JOIN_TOKEN = '" + token + "'"))

		yml := get(JoinKubernetesPath, token, "")
		Expect(yml.Code).To(Equal(http.StatusOK))
		Expect(yml.Body.String()).To(ContainSubstring("kind: StatefulSet"))
		Expect(yml.Body.String()).To(ContainSubstring("name: localai-worker\n"))
		Expect(yml.Body.String()).To(ContainSubstring("kind: Secret"))
		Expect(yml.Body.String()).To(ContainSubstring(`LOCALAI_JOIN_TOKEN: "` + token + `"`))
		Expect(yml.Body.String()).To(ContainSubstring("mountPath: /data"))
		Expect(yml.Body.String()).To(ContainSubstring("# nvidia.com/gpu: 1"))
		Expect(yml.Body.String()).ToNot(ContainSubstring("@@"))

		// None of that took a use.
		rows, err := store.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows[0].ID).To(Equal(row.ID))
		Expect(rows[0].Uses).To(BeZero())
		_, err = store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred(), "the token still works for a registration")
	})

	It("limits the connections with routes, within what the token allows", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{Routes: []string{"nats", "websocket"}})
		sh := get(JoinShellPath, token, "&routes=nats,websocket")
		Expect(sh.Body.String()).To(ContainSubstring("LOCALAI_WORKER_ROUTES='nats,websocket'"))
		yml := get(JoinKubernetesPath, token, "&routes=nats")
		Expect(yml.Body.String()).To(ContainSubstring(`value: "nats"`))
		Expect(yml.Body.String()).To(ContainSubstring("POD_IP"))

		narrow, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{Routes: []string{"websocket"}})
		Expect(get(JoinShellPath, narrow, "&routes=nats").Code).To(Equal(http.StatusBadRequest))
		Expect(get(JoinShellPath, narrow, "&routes=pigeon").Code).To(Equal(http.StatusBadRequest))
		inherited := get(JoinShellPath, narrow, "")
		Expect(inherited.Body.String()).To(ContainSubstring("LOCALAI_WORKER_ROUTES='websocket'"), "a restricted token restricts its script")
	})

	It("answers 404 for a token that is unknown, expired, revoked or used up, and says nothing else", func() {
		expired, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{TTL: time.Second})
		time.Sleep(1100 * time.Millisecond)
		revoked, row, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(store.Revoke(ctx, row.ID, "admin")).To(Succeed())
		spent, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		_, err := store.Consume(ctx, spent)
		Expect(err).ToNot(HaveOccurred())

		// This spec sends many bad tokens from one source: lift the throttle.
		roomy := cluster.NewJoinFailureLimiter(1000, time.Minute)
		e = echo.New()
		e.GET(JoinShellPath, JoinScriptEndpoint(store, roomy, JoinScriptShell))
		e.GET(JoinPowerShellPath, JoinScriptEndpoint(store, roomy, JoinScriptPowerShell))
		e.GET(JoinKubernetesPath, JoinScriptEndpoint(store, roomy, JoinScriptKubernetes))

		for _, token := range []string{expired, revoked, spent, "ljt_0000000000000000.nope", "", "junk"} {
			for _, path := range []string{JoinShellPath, JoinPowerShellPath, JoinKubernetesPath} {
				rec := get(path, token, "")
				Expect(rec.Code).To(Equal(http.StatusNotFound), path+" "+token)
				Expect(rec.Body.String()).To(MatchJSON(`{"error":"not found"}`))
			}
		}
	})

	It("throttles a source that asks for many tokens that are not valid", func() {
		for range 5 {
			Expect(get(JoinShellPath, "ljt_0000000000000000.nope", "").Code).To(Equal(http.StatusNotFound))
		}
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(get(JoinShellPath, token, "").Code).To(Equal(http.StatusTooManyRequests))
	})

	It("quotes a crafted host so that it cannot add a command to the script", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		req := httptest.NewRequest(http.MethodGet, JoinShellPath+"?t="+url.QueryEscape(token), nil)
		req.Host = "x'; rm -rf /; echo '"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Body.String()).To(ContainSubstring(`LOCALAI_REGISTER_TO='http://x'\''; rm -rf /; echo '\'''`))
	})
})
