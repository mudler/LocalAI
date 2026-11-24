//go:build auth

package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The LocalAI client tool list_models reads this route as the acting user, so
// the model allow-list of that user must narrow the answer.
var _ = Describe("ListModelCapabilitiesEndpoint with accounts", func() {
	It("shows a user with an allow-list only the models on it, and an administrator all", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		ml := model.NewModelLoader(st)
		bcl := config.NewModelConfigLoader(dir)
		for name, usecase := range map[string]string{"flux": "image", "hidden": "image", "voice": "tts"} {
			path := filepath.Join(dir, name+".yaml")
			Expect(os.WriteFile(path, []byte("name: "+name+"\nbackend: stub\nknown_usecases: ["+usecase+"]\n"), 0o600)).To(Succeed())
			Expect(bcl.ReadModelConfig(path)).To(Succeed())
		}
		mk := func(role string, allow *auth.ModelAllowlist) *auth.User {
			u := &auth.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.com", Provider: auth.ProviderLocal, Subject: uuid.NewString(), Role: role, Status: auth.StatusActive}
			Expect(db.Create(u).Error).To(Succeed())
			if allow != nil {
				Expect(db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: u.ID, Permissions: auth.PermissionMap{}, AllowedModels: *allow}).Error).To(Succeed())
			}
			return u
		}
		ids := func(u *auth.User) []string {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/v1/models/capabilities", nil), rec)
			c.Set("auth_user", u)
			Expect(ListModelCapabilitiesEndpoint(bcl, ml, config.NewApplicationConfig(), db)(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			var resp schema.ModelCapabilitiesResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			var out []string
			for _, m := range resp.Data {
				out = append(out, m.ID)
			}
			return out
		}
		limited := mk(auth.RoleUser, &auth.ModelAllowlist{Enabled: true, Models: []string{"flux", "voice"}})
		Expect(ids(limited)).To(ConsistOf("flux", "voice"))
		Expect(ids(mk(auth.RoleAdmin, nil))).To(ConsistOf("flux", "hidden", "voice"))
		Expect(ids(mk(auth.RoleUser, &auth.ModelAllowlist{Enabled: false}))).To(ConsistOf("flux", "hidden", "voice"))
	})
})
