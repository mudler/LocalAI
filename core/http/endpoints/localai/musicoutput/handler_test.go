//go:build auth

// SPDX-License-Identifier: MIT
package musicoutput

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/music/outputstore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

func TestMusicOutput(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Protected music output HTTP")
}

// These are synthetic store-validator fixtures, NOT real WAV decoding or inference.
const syntheticAudio = "synthetic-validated-wav"

type observedStore struct {
	*outputstore.Store
	lookups, opens, closes int
	beforeOpen             func()
	openErr                error
}

func (s *observedStore) Lookup(ctx context.Context, id string) (outputstore.Manifest, error) {
	s.lookups++
	return s.Store.Lookup(ctx, id)
}
func (s *observedStore) Open(ctx context.Context, m outputstore.Manifest) (io.ReadCloser, error) {
	s.opens++
	if s.beforeOpen != nil {
		s.beforeOpen()
	}
	if s.openErr != nil {
		return nil, s.openErr
	}
	r, e := s.Store.Open(ctx, m)
	if e != nil {
		return nil, e
	}
	return &observedReader{ReadCloser: r, closed: func() { s.closes++ }}, nil
}

type observedReader struct {
	io.ReadCloser
	closed func()
}

func (r *observedReader) Close() error { r.closed(); return r.ReadCloser.Close() }

var _ = Describe("handler through actual auth.Middleware", func() {
	var db *gorm.DB
	var app *config.ApplicationConfig
	var s *observedStore
	var now time.Time
	var id, ownerKey, foreignKey, adminKey string
	var keyID string
	var dir string
	var call func(string, string, string) *httptest.ResponseRecorder
	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "music-http-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		db, err = auth.InitDB(filepath.Join(dir, "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		app = config.NewApplicationConfig()
		app.Auth.APIKeyHMACSecret = "test-only-hmac-secret"
		// Both override mechanisms exercise handler-level enforcement, including HEAD.
		app.PathWithoutAuth = []string{"/v1/music/outputs"}
		app.ApiKeys = []string{"trusted-legacy-key"}
		app.DisableApiKeyRequirementForHttpGet = true
		app.HttpGetExemptedEndpoints = []*regexp.Regexp{regexp.MustCompile(".*")}
		for _, u := range []auth.User{{ID: "owner", Role: auth.RoleUser, Status: auth.StatusActive}, {ID: "foreign", Role: auth.RoleUser, Status: auth.StatusActive}, {ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}} {
			Expect(db.Create(&u).Error).To(Succeed())
		}
		ownerKey, _, err = auth.CreateAPIKey(db, "owner", "owner-key", auth.RoleUser, app.Auth.APIKeyHMACSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		var key *auth.UserAPIKey
		foreignKey, _, err = auth.CreateAPIKey(db, "foreign", "foreign-key", auth.RoleUser, app.Auth.APIKeyHMACSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		adminKey, key, err = auth.CreateAPIKey(db, "admin", "admin-key", auth.RoleAdmin, app.Auth.APIKeyHMACSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		keyID = key.ID
		for _, u := range []string{"owner", "foreign"} {
			Expect(auth.UpdateUserPermissions(db, u, auth.PermissionMap{FeatureMusic: true})).To(Succeed())
			Expect(auth.UpdateModelAllowlist(db, u, auth.ModelAllowlist{Enabled: true, Models: []string{"canonical-generation", "canonical-transcription"}})).To(Succeed())
		}
		objects, err := outputstore.NewPrivateFilesystem(filepath.Join(dir, "objects"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(objects.Close)
		now = time.Now().UTC()
		store, err := outputstore.New(outputstore.Config{DB: db, Objects: objects, StagingRoot: filepath.Join(dir, "staging"), Clock: func() time.Time { return now }, ValidateWAV: func(_ context.Context, r io.ReadSeeker, n int64) error {
			b, e := io.ReadAll(r)
			if e != nil {
				return e
			}
			if string(b) != syntheticAudio || int64(len(b)) != n {
				return errors.New("invalid synthetic fixture")
			}
			return nil
		}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		s = &observedStore{Store: store}
		ctx := context.Background()
		Expect(store.RegisterRequest(ctx, outputstore.RequestMetadata{ID: "test-request", Principal: "owner", GenerationModel: "canonical-generation", TranscriptionModel: "canonical-transcription", Deadline: now.Add(time.Minute)})).To(Succeed())
		candidate, err := store.ImportCandidate(ctx, "test-request", strings.NewReader(syntheticAudio))
		Expect(err).NotTo(HaveOccurred())
		candidate, err = store.FetchValidate(ctx, "test-request", candidate)
		Expect(err).NotTo(HaveOccurred())
		prepared, err := store.Prepare(ctx, "test-request", candidate)
		Expect(err).NotTo(HaveOccurred())
		id, err = store.Commit(ctx, "test-request", prepared)
		Expect(err).NotTo(HaveOccurred())
		call = func(method, credential, target string) *httptest.ResponseRecorder {
			e := echo.New()
			e.Use(auth.Middleware(db, app))
			h := NewHandler(s, db, app)
			e.GET("/v1/music/outputs/:output_id/audio", h)
			e.HEAD("/v1/music/outputs/:output_id/audio", h)
			req := httptest.NewRequest(method, target, nil)
			req.Host = "evil.example"
			req.Header.Set("Accept", "text/html")
			if credential != "" {
				req.Header.Set("Authorization", "Bearer "+credential)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}
	})
	DescribeTable("opaque authorization and delivery", func(method, scenario string, status int) {
		credential := ownerKey
		targetID := id
		switch scenario {
		case "anonymous":
			credential = ""
		case "invalid credential":
			credential = "bad-secret"
		case "foreign":
			credential = foreignKey
		case "admin":
			credential = adminKey
		case "legacy admin":
			credential = "trusted-legacy-key"
		case "missing":
			targetID = strings.Repeat("a", 64)
		case "invalid id":
			targetID = "invalid"
		case "expired":
			now = now.Add(outputstore.OutputLifetime)
		case "feature revoked":
			Expect(auth.UpdateUserPermissions(db, "owner", auth.PermissionMap{FeatureMusic: false})).To(Succeed())
		case "feature absent":
			Expect(auth.UpdateUserPermissions(db, "owner", auth.PermissionMap{})).To(Succeed())
		case "generation revoked":
			Expect(auth.UpdateModelAllowlist(db, "owner", auth.ModelAllowlist{Enabled: true, Models: []string{"canonical-transcription"}})).To(Succeed())
		case "transcription revoked":
			Expect(auth.UpdateModelAllowlist(db, "owner", auth.ModelAllowlist{Enabled: true, Models: []string{"canonical-generation"}})).To(Succeed())
		case "permissions unavailable":
			Expect(db.Migrator().DropTable(&auth.UserPermission{})).To(Succeed())
		case "expiry during open":
			s.beforeOpen = func() { now = now.Add(outputstore.OutputLifetime) }
		case "storage error":
			s.openErr = errors.New("/private/secret.wav credential worker.example")
		case "open mode":
			Expect(db.Model(&outputstore.Manifest{}).Where("id = ?", id).Update("principal", OpenPrincipal).Error).To(Succeed())
			db = nil
			app.ApiKeys = nil
			credential = ""
		case "legacy only":
			db = nil
			credential = "trusted-legacy-key"
		case "legacy only anonymous":
			db = nil
			credential = ""
		case "db only anonymous":
			app.ApiKeys = nil
			credential = ""
		case "revoked credential":
			Expect(auth.RevokeAPIKey(db, keyID, "admin")).To(Succeed())
			credential = adminKey
		}
		rec := call(method, credential, "/v1/music/outputs/"+targetID+"/audio?model=unrelated-query-model")
		Expect(rec.Code).To(Equal(status), rec.Body.String())
		Expect(rec.Header().Get("Cache-Control")).To(Equal("private, no-store"))
		Expect(rec.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
		if method == http.MethodHead {
			Expect(rec.Body.Len()).To(BeZero())
		}
		if status == 200 {
			Expect(rec.Header().Get("Content-Type")).To(Equal("audio/wav"))
			Expect(rec.Header().Get("Content-Disposition")).To(Equal(`attachment; filename="music.wav"`))
			Expect(rec.Header().Get("Content-Length")).To(Equal(strconv.Itoa(len(syntheticAudio))))
			if method == http.MethodGet {
				Expect(rec.Body.String()).To(Equal(syntheticAudio))
			}
			Expect(s.opens).To(Equal(1))
			Expect(s.closes).To(Equal(1))
		} else {
			Expect(rec.Header().Get("Content-Disposition")).To(BeEmpty())
			if method == http.MethodGet {
				var body map[string]any
				Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
				detail := body["error"].(map[string]any)
				Expect(detail["code"]).To(Equal(float64(status)))
				Expect(detail["stage"]).To(Equal("delivery"))
				Expect(detail["reason"]).NotTo(BeEmpty())
				Expect(detail["type"]).To(Equal("music_stage_error"))
				Expect(rec.Body.String()).NotTo(ContainSubstring("private/"))
				Expect(rec.Body.String()).NotTo(ContainSubstring("worker.example"))
			}
			if status == 401 {
				Expect(s.lookups).To(BeZero())
				Expect(s.opens).To(BeZero())
			}
			if scenario != "expiry during open" && scenario != "storage error" {
				Expect(s.opens).To(BeZero())
			}
		}
	}, func() []TableEntry {
		var entries []TableEntry
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, scenario := range []struct {
				name   string
				status int
			}{{"owner", 200}, {"admin", 200}, {"legacy admin", 200}, {"anonymous", 401}, {"invalid credential", 401}, {"foreign", 404}, {"missing", 404}, {"invalid id", 404}, {"expired", 404}, {"feature revoked", 404}, {"feature absent", 200}, {"generation revoked", 404}, {"transcription revoked", 404}, {"permissions unavailable", 404}, {"expiry during open", 404}, {"storage error", 500}, {"open mode", 200}, {"legacy only", 200}, {"legacy only anonymous", 401}, {"db only anonymous", 401}, {"revoked credential", 401}} {
				entries = append(entries, Entry(method+" "+scenario.name, method, scenario.name, scenario.status))
			}
		}
		return entries
	}())
})
