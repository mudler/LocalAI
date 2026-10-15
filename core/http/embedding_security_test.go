// SPDX-License-Identifier: MIT
//go:build auth

package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

type countedEmbeddingBody struct{ remaining, read int64 }

func (r *countedEmbeddingBody) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := range p[:n] {
		p[i] = ' '
	}
	r.remaining -= n
	r.read += n
	return int(n), nil
}
func (*countedEmbeddingBody) Close() error { return nil }

var _ = Describe("Embedding production security", func() {
	newApp := func(global int, authenticated bool) (*application.Application, string) {
		root := GinkgoT().TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		a, err := application.New(config.WithContext(ctx), config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithUploadLimitMB(global), config.WithAuthEnabled(authenticated), config.WithAuthDatabaseURL(root+"/auth.db"), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(a.Shutdown()).To(Succeed()) })
		a.ApplicationConfig().EnableTracing = true
		token := ""
		if authenticated {
			user := auth.User{ID: root, Email: "test@example.invalid", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal, Subject: root}
			Expect(a.AuthDB().Create(&user).Error).To(Succeed())
			token, err = auth.CreateSession(a.AuthDB(), user.ID, a.ApplicationConfig().Auth.APIKeyHMACSecret)
			Expect(err).NotTo(HaveOccurred())
		}
		return a, token
	}
	aliases := []string{"/embeddings", "/v1/embeddings", "/v1/engines/allowed/embeddings"}
	It("bounds reads before production auth and tracing for every alias and transfer length", func() {
		for _, global := range []int{0, 64, 1} {
			for _, authenticated := range []bool{false, true} {
				a, token := newApp(global, authenticated)
				e, err := API(a)
				Expect(err).NotTo(HaveOccurred())
				for _, path := range aliases {
					for _, known := range []bool{false, true} {
						limit := int64(embeddingmedia.MaxBodyBytes)
						if global == 1 {
							limit = 1 << 20
						}
						body := &countedEmbeddingBody{remaining: int64(embeddingmedia.MaxBodyBytes) + 4096}
						req := httptest.NewRequest(http.MethodPost, path, body)
						req.ContentLength = -1
						req.TransferEncoding = []string{"chunked"}
						if known {
							req.ContentLength = body.remaining
							req.TransferEncoding = nil
						}
						req.Header.Set("Content-Type", "application/json")
						if token != "" {
							req.Header.Set("Authorization", "Bearer "+token)
						}
						rec := httptest.NewRecorder()
						e.ServeHTTP(rec, req)
						Expect(body.read).To(BeNumerically("<=", limit+1), fmt.Sprintf("global=%d auth=%v path=%s known=%v", global, authenticated, path, known))
						Expect(rec.Code).To(Equal(413), fmt.Sprintf("global=%d auth=%v path=%s known=%v body=%s", global, authenticated, path, known, rec.Body.String()))
						if known {
							Expect(body.read).To(BeZero())
						}
					}
				}
			}
		}
	})
	It("reads almost nothing from unauthenticated embedding requests", func() {
		a, _ := newApp(0, true)
		e, err := API(a)
		Expect(err).NotTo(HaveOccurred())
		for _, path := range aliases {
			body := &countedEmbeddingBody{remaining: 40 << 20}
			req := httptest.NewRequest(http.MethodPost, path, body)
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(401), path)
			Expect(body.read).To(BeNumerically("<=", 64<<10), path)
		}
	})
	It("omits production validation and unsupported payloads from traces", func() {
		a, _ := newApp(0, false)
		e, err := API(a)
		Expect(err).NotTo(HaveOccurred())
		middleware.ClearTraces()
		for _, path := range aliases {
			for _, body := range []string{
				`{"input":{"content":[{"type":"text","text":"private-text"},{"type":"image_url","image_url":{"url":"https://example.invalid/?private-query=secret"}}]}}`,
				`{"input":{"content":[{"type":"input_audio","input_audio":{"data":"cHJpdmF0ZS1tZWRpYQ==","format":"wav"}}]}}`,
				`{"input":{"content":[{"type":"unknown","text":"private-text"}]}}`,
			} {
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				Expect(rec.Code).To(Or(Equal(400), Equal(501)))
				Expect(rec.Body.String()).NotTo(ContainSubstring("private-"))
				Expect(rec.Body.String()).NotTo(ContainSubstring("cHJpdmF0"))
			}
		}
		// Traces are written asynchronously; whatever lands must carry no payload.
		Eventually(func() int { return len(middleware.GetTraces()) }).Should(Equal(len(aliases) * 3))
		dump, err := json.Marshal(middleware.GetTraces())
		Expect(err).NotTo(HaveOccurred())
		for _, secret := range []string{"private-", "cHJpdmF0"} {
			Expect(string(dump)).NotTo(ContainSubstring(secret))
		}
		Expect(filepath.Walk(a.ApplicationConfig().DataPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			Expect(string(data)).NotTo(ContainSubstring("private-text"))
			Expect(string(data)).NotTo(ContainSubstring("private-query"))
			Expect(string(data)).NotTo(ContainSubstring("cHJpdmF0"))
			return err
		})).To(Succeed())
	})
	It("leaves nonembedding body consumption and global limits unchanged", func() {
		for _, global := range []int{0, 1} {
			a, _ := newApp(global, false)
			e, err := API(a)
			Expect(err).NotTo(HaveOccurred())
			e.POST("/body-probe", func(c echo.Context) error {
				_, err := io.Copy(io.Discard, c.Request().Body)
				if err != nil {
					return err
				}
				return c.NoContent(204)
			})
			body := &countedEmbeddingBody{remaining: 2 << 20}
			req := httptest.NewRequest("POST", "/body-probe", body)
			req.ContentLength = -1
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if global == 0 {
				Expect(rec.Code).To(Equal(204))
				Expect(body.read).To(Equal(int64(2 << 20)))
			} else {
				Expect(rec.Code).To(Equal(413))
			}
		}
	})

	It("preserves nonadmin model permissions and quotas on every alias", func() {
		a, token := newApp(0, true)
		e, err := API(a)
		Expect(err).NotTo(HaveOccurred())
		db := a.AuthDB()
		var user auth.User
		Expect(db.First(&user).Error).To(Succeed())
		for _, path := range aliases {
			send := func() int {
				req := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"allowed","input":{"content":[{"type":"text","text":"private-input"}]}}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				return rec.Code
			}
			Expect(auth.UpdateModelAllowlist(db, user.ID, auth.ModelAllowlist{Enabled: true, Models: []string{"denied"}})).To(Succeed())
			Expect(send()).To(Equal(403))
			Expect(auth.UpdateModelAllowlist(db, user.ID, auth.ModelAllowlist{Enabled: true, Models: []string{"allowed"}})).To(Succeed())
			zero := int64(0)
			rule, err := auth.CreateOrUpdateQuotaRule(db, user.ID, "allowed", &zero, nil, 3600)
			Expect(err).NotTo(HaveOccurred())
			Expect(send()).To(Equal(429))
			Expect(auth.DeleteQuotaRule(db, rule.ID, user.ID)).To(Succeed())
			Expect(send()).To(Equal(501))
		}
	})
})
