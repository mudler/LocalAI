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
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ListModelsEndpoint for a client key", func() {
	It("lists only the models of the key that it can still use", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		ml := model.NewModelLoader(st)
		bcl := config.NewModelConfigLoader(dir)
		for name, extra := range map[string]string{
			"listed":    "",
			"unlisted":  "",
			"gained":    "mcp:\n  auto: true\n",
			"listed-as": "",
		} {
			path := filepath.Join(dir, name+".yaml")
			Expect(os.WriteFile(path, []byte("name: "+name+"\nbackend: stub\n"+extra), 0o600)).To(Succeed())
			Expect(bcl.ReadModelConfig(path)).To(Succeed())
		}
		owner := &auth.User{ID: uuid.NewString(), Email: "o@example.com", Provider: auth.ProviderLocal, Subject: uuid.NewString(), Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(owner).Error).To(Succeed())

		list := func(key *auth.UserAPIKey) []string {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/v1/models", nil), rec)
			u := *owner
			c.Set("auth_user", &u)
			if key != nil {
				c.Set("auth_apikey", key)
			}
			Expect(ListModelsEndpoint(bcl, ml, config.NewApplicationConfig(), db)(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			var resp struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			out := []string{}
			for _, m := range resp.Data {
				out = append(out, m.ID)
			}
			return out
		}
		client := &auth.UserAPIKey{
			ID: uuid.NewString(), UserID: owner.ID, Kind: auth.KeyKindClient,
			Features: auth.PermissionMap{auth.FeatureClientInference: true},
			Models:   auth.ModelList{"listed", "gained", "gone"},
		}
		Expect(list(client)).To(ConsistOf("listed"), "not the unlisted, not the one with tools, not the one with no config")
		// A client key without the feature sees nothing, and the owner sees all.
		Expect(list(&auth.UserAPIKey{ID: "x", Kind: auth.KeyKindClient, Models: auth.ModelList{"listed"}})).To(BeEmpty())
		Expect(list(nil)).To(ContainElements("listed", "unlisted", "gained"))
	})
})
