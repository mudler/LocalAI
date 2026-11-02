// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("collection legacy listing status", func() {
	It("returns explicit action-required status without exposing ambiguous names", func() {
		raw := os.Getenv("MEMORY_DATABASE_URL")
		if raw == "" {
			Skip("MEMORY_DATABASE_URL required")
		}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		conn, err := pgx.Connect(ctx, raw)
		Expect(err).NotTo(HaveOccurred())
		schema := fmt.Sprintf("legacy_http_%d", time.Now().UnixNano())
		_, err = conn.Exec(ctx, "CREATE SCHEMA "+schema)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, e := conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
			Expect(e).NotTo(HaveOccurred())
			Expect(conn.Close(context.Background())).To(Succeed())
		})
		_, err = conn.Exec(ctx, "CREATE TABLE "+schema+".collection_config(collection_name text PRIMARY KEY);INSERT INTO "+schema+".collection_config VALUES('ambiguous-private-name')")
		Expect(err).NotTo(HaveOccurred())
		u, err := url.Parse(raw)
		Expect(err).NotTo(HaveOccurred())
		q := u.Query()
		q.Set("search_path", schema+",public")
		u.RawQuery = q.Encode()
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(filepath.Join(dir, "models")), system.WithBackendPath(filepath.Join(dir, "backends")))
		Expect(err).NotTo(HaveOccurred())
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(st), config.WithDataPath(dir), config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithCollectionsStateDir(dir), config.WithCollectionsVectorEngine("postgres"), config.WithCollectionsDatabaseURL(u.String()), config.WithCollectionsPostgresNamespace(schema))
		Expect(err).NotTo(HaveOccurred())
		app.StartCollections()
		Expect(app.CollectionsService()).NotTo(BeNil())
		DeferCleanup(app.CollectionsService().Close)
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/collections", nil), rec)
		Expect(ListCollectionsEndpoint(app)(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))
		var body struct {
			Collections []string `json:"collections"`
			Legacy      struct {
				ActionRequired bool   `json:"action_required"`
				Message        string `json:"message"`
			} `json:"legacy_status"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Collections).To(BeEmpty())
		Expect(body.Legacy.ActionRequired).To(BeTrue())
		Expect(body.Legacy.Message).NotTo(BeEmpty())
		Expect(rec.Body.String()).NotTo(ContainSubstring("ambiguous-private-name"))
	})
})
