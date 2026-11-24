//go:build auth

package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	goruntime "runtime"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/resources"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The user picker of a folder of a person's home, through the real server:
// the routes, the account database and the picker that the other kinds of
// sharing use.
var _ = Describe("The folder share picker over the real routes", func() {
	BeforeEach(func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
	})

	It("finds people as the picker of the other kinds: by three characters of the display name, never a nameless account by a prefix, at a bounded rate", func() {
		app, call := newShareRouteServer()
		db := app.AuthDB()
		for _, id := range shareRouteUsers {
			Expect(auth.UpdateUserPermissions(db, id, auth.PermissionMap{auth.FeatureAgents: true, auth.FeatureCollections: true, auth.FeatureChats: true})).To(Succeed())
		}
		const nameless = "33333333-3333-4333-8333-333333333333"
		Expect(db.Create(&auth.User{ID: nameless, Email: "ceo@example.test", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())

		code, body := call("alice", http.MethodPost, "/api/chats/files/folders", `{"path":"/Projects"}`, "")
		Expect(code).To(BeNumerically("<", 300), body)
		code, body = call("alice", http.MethodPost, "/api/chats/files/shares", `{"path":"/Projects"}`, "")
		Expect(code).To(Equal(http.StatusOK), body)
		var folder struct {
			ID int64 `json:"id"`
		}
		Expect(json.Unmarshal([]byte(body), &folder)).To(Succeed())
		id := fmt.Sprint(folder.ID)

		searches := 0
		search := func(q string) (int, string, []map[string]any) {
			searches++
			code, body := call("alice", http.MethodGet, "/api/chats/files/shares/"+id+"/users?q="+q, "", id)
			var out struct {
				Users []map[string]any `json:"users"`
			}
			if code == http.StatusOK {
				Expect(json.Unmarshal([]byte(body), &out)).To(Succeed())
			}
			return code, body, out.Users
		}

		code, body, _ = search("bo")
		Expect(code).To(Equal(http.StatusBadRequest), "two characters are too few: "+body)
		for _, q := range []string{"ceo", "ceo%40exa", "ceo%40example"} {
			code, body, found := search(q)
			Expect(code).To(Equal(http.StatusOK), body)
			Expect(found).To(BeEmpty(), q+": a prefix of an address finds nobody")
		}
		code, body, found := search("ceo%40example.test")
		Expect(code).To(Equal(http.StatusOK), body)
		Expect(found).To(Equal([]map[string]any{{"id": nameless, "displayName": ""}}), "the whole address finds an account without a name, shown without one")
		code, body, found = search("bob")
		Expect(code).To(Equal(http.StatusOK), body)
		Expect(found).To(Equal([]map[string]any{{"id": shareRouteUsers["bob"], "displayName": "bob"}}), "an ID and a display name only")
		code, body, found = search("ali")
		Expect(code).To(Equal(http.StatusOK), body)
		Expect(found).To(BeEmpty(), "never the owner")

		// The query that is too short took no search from the budget. A slow
		// run may get a search back every DefaultUserSearchEvery, so search
		// until the refusal, a bounded number of times.
		for searches <= resources.DefaultUserSearchBurst {
			code, body, _ = search("bob")
			Expect(code).To(Equal(http.StatusOK), body)
		}
		for range 2 * resources.DefaultUserSearchBurst {
			if code, body, _ = search("bob"); code != http.StatusOK {
				break
			}
		}
		Expect(code).To(Equal(http.StatusTooManyRequests), body)
		Expect(body).To(ContainSubstring("rate_limited"))
	})
})
