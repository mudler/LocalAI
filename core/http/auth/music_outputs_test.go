//go:build auth

// SPDX-License-Identifier: MIT
package auth

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
)

var _ = Describe("Music output middleware boundary", func() {
	It("advertises both methods and the API feature", func() {
		Expect(APIFeatures).To(ContainElement(FeatureMusic))
		Expect(APIFeatureMetas()).To(ContainElement(FeatureMeta{FeatureMusic, "Music", true}))
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			Expect(RouteFeatureRegistry).To(ContainElement(RouteFeature{method, MusicOutputAudioRoute, FeatureMusic}))
		}
	})
	DescribeTable("delegates only the exact matched GET/HEAD audio route", func(method, path string, want bool) {
		e := echo.New()
		c := e.NewContext(httptest.NewRequest(method, "/v1/music/outputs/test/audio", nil), httptest.NewRecorder())
		c.SetPath(path)
		Expect(isMusicOutputAudio(c)).To(Equal(want))
	}, Entry("GET", http.MethodGet, MusicOutputAudioRoute, true), Entry("HEAD", http.MethodHead, MusicOutputAudioRoute, true), Entry("POST stays protected", http.MethodPost, MusicOutputAudioRoute, false), Entry("other leaf", http.MethodGet, "/v1/music/outputs/:output_id/metadata", false), Entry("prefix", http.MethodGet, "/v1/music/outputs/*", false), Entry("unmatched", http.MethodGet, "", false))
	It("does not skip model access on neighboring routes", func() {
		db, err := InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		u := &User{ID: "music-neighbor", Role: RoleUser, Status: StatusActive}
		Expect(db.Create(u).Error).To(Succeed())
		Expect(UpdateModelAllowlist(db, u.ID, ModelAllowlist{Enabled: true, Models: []string{"allowed"}})).To(Succeed())
		e := echo.New()
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error { c.Set(contextKeyUser, u); return next(c) }
		})
		e.Use(RequireModelAccess(db))
		e.GET("/v1/music/outputs/:output_id/metadata", func(c echo.Context) error { return c.NoContent(200) })
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/music/outputs/test/metadata?model=denied", nil))
		Expect(rec.Code).To(Equal(403))
	})
	DescribeTable("normalizes specific-route auth denial even with opaque errors", func(method string) {
		app := config.NewApplicationConfig()
		app.ApiKeys = []string{"secret"}
		app.OpaqueErrors = true
		e := echo.New()
		e.Use(Middleware(nil, app))
		e.Add(method, MusicOutputAudioRoute, func(c echo.Context) error { return c.NoContent(200) })
		req := httptest.NewRequest(method, "/v1/music/outputs/test/audio", nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("Authorization", "Basic malformed")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(401))
		Expect(rec.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("private, no-store"))
		if method == http.MethodHead {
			Expect(rec.Body.Len()).To(BeZero())
		} else {
			Expect(rec.Body.String()).To(ContainSubstring(`"code":401`))
		}
	}, Entry("GET", http.MethodGet), Entry("HEAD", http.MethodHead))
})
