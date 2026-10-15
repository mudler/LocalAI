// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dispatchBody is a typed dispatch request as the management UI sends it.
func dispatchBody(operation, addon, action string, generation uint64, arguments string) string {
	if arguments == "" {
		arguments = "{}"
	}
	return fmt.Sprintf(`{"operationId":%q,"addonId":%q,"generation":%d,"action":%q,"arguments":%s}`, operation, addon, generation, action, arguments)
}

func payloadOf(r reply) map[string]any {
	m, _ := r.body["payload"].(map[string]any)
	return m
}

func listOf(m map[string]any, key string) []map[string]any {
	out := []map[string]any{}
	items, _ := m[key].([]any)
	for _, v := range items {
		item, _ := v.(map[string]any)
		out = append(out, item)
	}
	return out
}

func byID(items []map[string]any, field, id string) map[string]any {
	for _, item := range items {
		if item[field] == id {
			return item
		}
	}
	return nil
}

// appsResolver is the credential configuration of the apps test owner.
var appsResolver catalog.CredentialResolver

var _ = Describe("catalog and management actions", Label("catalogui"), func() {
	var s *appsStack
	BeforeEach(func() {
		// The owner configures one reference; a library can name it, never a
		// secret of its own.
		store, err := credentials.Parse([]byte("- match: https://library.example\n  bearer: secret"), nil)
		Expect(err).NotTo(HaveOccurred())
		appsResolver = catalog.CredentialResolver{References: map[string]catalog.CredentialBinding{"library-token": {Origin: "https://library.example", Store: store}}}
		DeferCleanup(func() { appsResolver = catalog.CredentialResolver{} })
		s = newAppsStack()
	})

	It("serves catalog search, details and the installed management view to administrators only, the same from every frontend", func() {
		s.each(func(name string, e http.Handler) {
			admin := bearerOf(s.tokens["alice"])
			all := send(e, "POST", "/api/addons/dispatch", dispatchBody("search-all-"+name, "", "catalog-search", 0, `{}`), admin)
			Expect(all.code).To(Equal(200), name)
			page := payloadOf(all)
			Expect(page).To(HaveKeyWithValue("total", BeNumerically("==", 2)), name)
			Expect(byID(listOf(page, "releases"), "addonId", "notes")).To(HaveKeyWithValue("digest", HavePrefix("sha256:")), name)

			one := send(e, "POST", "/api/addons/dispatch", dispatchBody("search-one-"+name, "", "catalog-search", 0, `{"text":"head","sourceId":"test","limit":1}`), admin)
			Expect(one.code).To(Equal(200), name)
			Expect(listOf(payloadOf(one), "releases")).To(HaveLen(1), name)
			Expect(listOf(payloadOf(one), "releases")[0]).To(HaveKeyWithValue("addonId", "headless"), name)

			bad := send(e, "POST", "/api/addons/dispatch", dispatchBody("search-bad-"+name, "", "catalog-search", 0, `{"limit":1000}`), admin)
			Expect(bad.code).To(Equal(403), name+": an out-of-range limit is a denied request, not a page")
			unknown := send(e, "POST", "/api/addons/dispatch", dispatchBody("search-field-"+name, "", "catalog-search", 0, `{"text":"x","sourceUrl":"https://elsewhere.example"}`), admin)
			Expect(unknown.code).To(Equal(403), name+": unknown argument fields are refused")

			details := send(e, "POST", "/api/addons/dispatch", dispatchBody("details-"+name, "notes", "catalog-details", 0, `{"sourceId":"test","version":"1.0.0"}`), admin)
			Expect(details.code).To(Equal(200), name)
			release, _ := payloadOf(details)["release"].(map[string]any)
			Expect(release).To(HaveKeyWithValue("addonId", "notes"), name)
			Expect(release).To(HaveKeyWithValue("os", "linux"), name)
			Expect(payloadOf(details)["installed"]).To(HaveKeyWithValue("generation", BeNumerically("==", 1)), name)
			missing := send(e, "POST", "/api/addons/dispatch", dispatchBody("details-missing-"+name, "ghost", "catalog-details", 0, `{"sourceId":"test","version":"1.0.0"}`), admin)
			Expect(missing.code).To(Equal(403), name)

			installed := send(e, "POST", "/api/addons/dispatch", dispatchBody("installed-"+name, "", "addons-installed", 0, `{}`), admin)
			Expect(installed.code).To(Equal(200), name)
			list := listOf(payloadOf(installed), "addons")
			Expect(list).To(HaveLen(2), name)
			notes, headless := byID(list, "id", "notes"), byID(list, "id", "headless")
			Expect(notes).To(HaveKeyWithValue("enabled", true))
			Expect(notes).To(HaveKeyWithValue("version", "1.0.0"))
			Expect(notes).To(HaveKeyWithValue("generation", BeNumerically("==", 1)))
			Expect(notes).To(HaveKeyWithValue("verification", "sha256"))
			Expect(notes).To(HaveKeyWithValue("sourceId", "test"))
			Expect(notes).To(HaveKeyWithValue("platform", HaveKeyWithValue("os", "linux")))
			Expect(notes).To(HaveKeyWithValue("compatibility", HaveKeyWithValue("host", HaveKeyWithValue("max", BeNumerically("==", 2)))))
			Expect(notes).To(HaveKeyWithValue("title", "Notes"))
			Expect(notes).To(HaveKeyWithValue("toolCount", BeNumerically("==", 2)))
			Expect(notes).To(HaveKey("storage"))
			Expect(notes).NotTo(HaveKey("update"), "an update is reported only from an authoritative check, which this release has none of")
			Expect(headless).To(HaveKeyWithValue("enabled", false))
			Expect(headless).To(HaveKeyWithValue("state", "stopped"), "health is an observation separate from enabled")
			Expect(headless).NotTo(HaveKey("mainPage"))
			raw, _ := json.Marshal(installed.body)
			for _, secret := range []string{s.root, "directory", "catalog.example", "127.0.0.1"} {
				Expect(string(raw)).NotTo(ContainSubstring(secret), name+": owner paths and addresses never reach the browser")
			}

			// Everyone else, whatever the feature grants, is denied by the server.
			for _, who := range []string{"carol", "dave"} {
				for _, action := range []string{"catalog-search", "catalog-details", "catalog-sources", "addons-installed"} {
					r := send(e, "POST", "/api/addons/dispatch", dispatchBody("deny-"+who+action+name, "notes", action, 0, `{"sourceId":"test","version":"1.0.0"}`), bearerOf(s.tokens[who]))
					Expect(r.code).To(Equal(403), name+" "+who+" "+action)
					Expect(r.body).NotTo(HaveKey("payload"))
				}
			}
			Expect(send(e, "POST", "/api/addons/dispatch", dispatchBody("anon"+name, "", "addons-installed", 0, `{}`), nil).code).To(Equal(401))
		})
	})

	It("refuses every management call of the owner itself for a caller that is not an administrator", func() {
		var key auth.UserAPIKey
		Expect(s.db.Where("user_id = ?", "carol").First(&key).Error).To(Succeed())
		carol := contract.Actor{UserID: "carol", CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}
		ctx := context.Background()
		_, err := s.manager.Installed(ctx, carol)
		Expect(err).To(MatchError(managerpkg.ErrForbidden))
		_, err = s.manager.CatalogSearch(ctx, carol, catalog.Query{})
		Expect(err).To(MatchError(managerpkg.ErrForbidden))
		_, err = s.manager.CatalogDetails(ctx, carol, "test", "notes", "1.0.0")
		Expect(err).To(MatchError(managerpkg.ErrForbidden))
		_, err = s.manager.Sources(ctx, carol)
		Expect(err).To(MatchError(managerpkg.ErrForbidden))
		_, err = s.manager.SaveSource(ctx, carol, catalog.Source{ID: "mine", Name: "Mine", URL: "https://library.example/releases.yaml"})
		Expect(err).To(MatchError(managerpkg.ErrForbidden))
		_, err = s.manager.RefreshSource(ctx, carol, "test")
		Expect(err).To(MatchError(managerpkg.ErrForbidden))
		// The administrator is served by the same calls.
		_, err = s.manager.Installed(ctx, s.owner)
		Expect(err).NotTo(HaveOccurred())
	})

	It("saves, refreshes and removes a library by reference and leaves installed addons alone", func() {
		s.each(func(name string, e http.Handler) {
			admin := bearerOf(s.tokens["alice"])
			list := send(e, "POST", "/api/addons/dispatch", dispatchBody("sources-"+name, "", "catalog-sources", 0, `{}`), admin)
			Expect(list.code).To(Equal(200), name)
			sources := listOf(payloadOf(list), "sources")
			Expect(byID(sources, "id", "test")).To(HaveKeyWithValue("enabled", true))
			Expect(byID(sources, "id", "test")).NotTo(HaveKey("removed"))

			// A secret is never accepted where a reference belongs.
			for label, source := range map[string]string{
				"credentials in the URL": `{"id":"cred","name":"Cred","url":"https://user:hunter2@library.example/releases.yaml","enabled":false}`,
				"a token in the query":   `{"id":"query","name":"Query","url":"https://library.example/releases.yaml?token=hunter2","enabled":false}`,
				"a pasted token":         `{"id":"paste","name":"Paste","url":"https://library.example/releases.yaml","enabled":false,"authReference":"ghp_Hunter2Hunter2Hunter2Hunter2"}`,
				"an unknown reference":   `{"id":"unknown","name":"Unknown","url":"https://library.example/releases.yaml","enabled":false,"authReference":"not-configured"}`,
				"an unknown field":       `{"id":"extra","name":"Extra","url":"https://library.example/releases.yaml","enabled":false,"password":"hunter2"}`,
				"a new loopback opt-in":  `{"id":"dev","name":"Dev","url":"http://127.0.0.1:9/releases.yaml","enabled":false,"allowLoopbackHTTP":true}`,
				"a bad identifier":       `{"id":"../x","name":"Bad","url":"https://library.example/releases.yaml","enabled":false}`,
			} {
				r := send(e, "POST", "/api/addons/dispatch", dispatchBody("save-bad-"+name+label, "", "catalog-source-save", 0, source), admin)
				Expect(r.code).To(Equal(403), name+": "+label)
				raw, _ := json.Marshal(r.body)
				Expect(string(raw)).NotTo(ContainSubstring("hunter2"), name+": "+label)
			}

			// A reference the owner configured is accepted and echoed as a name.
			id := "private-" + strings.ReplaceAll(name, "front-", "")
			save := send(e, "POST", "/api/addons/dispatch", dispatchBody("save-"+name, "", "catalog-source-save", 0,
				fmt.Sprintf(`{"id":%q,"name":"Private","url":"https://library.example/releases.yaml","enabled":false,"authReference":"library-token"}`, id)), admin)
			Expect(save.code).To(Equal(200), name)
			Expect(payloadOf(save)).To(HaveKeyWithValue("authReference", "library-token"))
			after := listOf(payloadOf(send(e, "POST", "/api/addons/dispatch", dispatchBody("sources-after-"+name, "", "catalog-sources", 0, `{}`), admin)), "sources")
			Expect(byID(after, "id", id)).To(HaveKeyWithValue("authReference", "library-token"))
			raw, _ := json.Marshal(after)
			Expect(string(raw)).NotTo(ContainSubstring("hunter2"))

			// Refresh reports the real outcome of the fetch.
			refresh := send(e, "POST", "/api/addons/dispatch", dispatchBody("refresh-"+name, "", "catalog-source-refresh", 0, `{"sourceId":"test"}`), admin)
			Expect(refresh.code).To(Equal(200), name)
			Expect(payloadOf(refresh)).To(HaveKeyWithValue("count", BeNumerically("==", 2)))
			Expect(payloadOf(refresh)).To(HaveKeyWithValue("sourceId", "test"))
			refreshed := byID(listOf(payloadOf(send(e, "POST", "/api/addons/dispatch", dispatchBody("sources-refreshed-"+name, "", "catalog-sources", 0, `{}`), admin)), "sources"), "id", "test")
			Expect(refreshed).To(HaveKeyWithValue("refresh", HaveKeyWithValue("count", BeNumerically("==", 2))), name)
			Expect(refreshed["refresh"]).NotTo(HaveKeyWithValue("failed", true), name)
			disabled := send(e, "POST", "/api/addons/dispatch", dispatchBody("refresh-off-"+name, "", "catalog-source-refresh", 0, fmt.Sprintf(`{"sourceId":%q}`, id)), admin)
			Expect(disabled.code).To(Equal(503), name+": a disabled library cannot be refreshed, and that is not success")
			Expect(disabled.errType()).To(Equal("outcome_unknown"), name+": a refresh is a mutation and keeps its operation")
			Expect(disabled.errOperation()).To(Equal("refresh-off-" + name))

			// Everyone else is denied, and nothing was saved for them.
			for _, who := range []string{"carol", "dave"} {
				r := send(e, "POST", "/api/addons/dispatch", dispatchBody("deny-save-"+who+name, "", "catalog-source-save", 0, `{"id":"mine","name":"Mine","url":"https://library.example/releases.yaml","enabled":false}`), bearerOf(s.tokens[who]))
				Expect(r.code).To(Equal(403), name+" "+who)
				r = send(e, "POST", "/api/addons/dispatch", dispatchBody("deny-refresh-"+who+name, "", "catalog-source-refresh", 0, `{"sourceId":"test"}`), bearerOf(s.tokens[who]))
				Expect(r.code).To(Equal(403), name+" "+who)
			}
			ids := []string{}
			for _, src := range listOf(payloadOf(send(e, "POST", "/api/addons/dispatch", dispatchBody("sources-final-"+name, "", "catalog-sources", 0, `{}`), admin)), "sources") {
				ids = append(ids, src["id"].(string))
			}
			Expect(ids).NotTo(ContainElement("mine"))
		})

		// Removing the library that installed the addons leaves them installed.
		before := listOf(payloadOf(send(s.frontends["front-a"], "POST", "/api/addons/dispatch", dispatchBody("installed-before", "", "addons-installed", 0, `{}`), bearerOf(s.tokens["alice"]))), "addons")
		remove := send(s.frontends["front-b"], "POST", "/api/addons/dispatch", dispatchBody("remove-test", "", "catalog-source-save", 0, `{"id":"test","name":"Test","url":"https://library.example/releases.yaml","enabled":true,"removed":true}`), bearerOf(s.tokens["alice"]))
		Expect(remove.code).To(Equal(200))
		sources := listOf(payloadOf(send(s.frontends["owner"], "POST", "/api/addons/dispatch", dispatchBody("sources-removed", "", "catalog-sources", 0, `{}`), bearerOf(s.tokens["alice"]))), "sources")
		Expect(byID(sources, "id", "test")).To(BeNil())
		afterList := listOf(payloadOf(send(s.frontends["front-a"], "POST", "/api/addons/dispatch", dispatchBody("installed-after", "", "addons-installed", 0, `{}`), bearerOf(s.tokens["alice"]))), "addons")
		Expect(afterList).To(Equal(before))
		page := payloadOf(send(s.frontends["front-b"], "POST", "/api/addons/dispatch", dispatchBody("search-removed", "", "catalog-search", 0, `{}`), bearerOf(s.tokens["alice"])))
		Expect(page).To(HaveKeyWithValue("total", BeNumerically("==", 0)))
		apps := appsOf(send(s.frontends["owner"], "GET", "/api/addons/apps", "", bearerOf(s.tokens["carol"])))
		Expect(apps).To(HaveKey("notes"))
	})

	It("upgrades an installed addon through the typed action and keeps its earlier generation", func() {
		s.each(func(name string, e http.Handler) {
			admin := bearerOf(s.tokens["alice"])
			before := byID(listOf(payloadOf(send(e, "POST", "/api/addons/dispatch", dispatchBody("up-before-"+name, "", "addons-installed", 0, `{}`), admin)), "addons"), "id", "headless")
			generation := uint64(before["generation"].(float64))
			r := send(e, "POST", "/api/addons/dispatch", dispatchBody("upgrade-"+name, "headless", "upgrade", 0, `{"sourceId":"test","version":"1.0.0"}`), admin)
			Expect(r.code).To(Equal(200), name)
			Expect(payloadOf(r)).To(HaveKeyWithValue("Phase", "completed"))
			after := byID(listOf(payloadOf(send(e, "POST", "/api/addons/dispatch", dispatchBody("up-after-"+name, "", "addons-installed", 0, `{}`), admin)), "addons"), "id", "headless")
			Expect(uint64(after["generation"].(float64))).To(Equal(generation + 1))
			Expect(after).To(HaveKeyWithValue("generations", BeNumerically("==", generation+1)))
			denied := send(e, "POST", "/api/addons/dispatch", dispatchBody("upgrade-deny-"+name, "headless", "upgrade", 0, `{"sourceId":"test","version":"1.0.0"}`), bearerOf(s.tokens["carol"]))
			Expect(denied.code).To(Equal(403))
		})
		// The same operation replays and never upgrades twice.
		r := send(s.frontends["owner"], "POST", "/api/addons/dispatch", dispatchBody("upgrade-replay", "headless", "upgrade", 0, `{"sourceId":"test","version":"1.0.0"}`), bearerOf(s.tokens["alice"]))
		Expect(r.code).To(Equal(200))
		first, err := s.manager.Status(context.Background(), "headless")
		Expect(err).NotTo(HaveOccurred())
		again := send(s.frontends["front-a"], "POST", "/api/addons/dispatch", dispatchBody("upgrade-replay", "headless", "upgrade", 0, `{"sourceId":"test","version":"1.0.0"}`), bearerOf(s.tokens["alice"]))
		Expect(again.code).To(Equal(200))
		second, err := s.manager.Status(context.Background(), "headless")
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Generation).To(Equal(first.Generation))
		Expect(second.Generations).To(HaveLen(int(second.Generation)))
	})

	It("reports a lost source change as an unknown outcome and a lost read as unavailable", func() {
		certs, roots := certificates()
		pins := map[string][32]byte{}
		keys := map[string]ed25519.PublicKey{}
		private := map[string]ed25519.PrivateKey{}
		for _, id := range []string{"owner", "front-a"} {
			pins[id] = sha256.Sum256(certs[id].Certificate[0])
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			Expect(e).NotTo(HaveOccurred())
			keys[id], private[id] = pub, priv
		}
		owner := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/addons/audience" {
				_ = json.NewEncoder(w).Encode(map[string]string{"audience": strings.Repeat("a", 64)})
				return
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		carrier := func(id string) *gateway.Carrier {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			return c
		}
		owner.TLS = carrier("owner").ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		front := s.frontend(&gateway.Gateway{Forward: carrier("front-a"), Revalidate: auth.AddonRevalidate(s.db, auth.AddonModeNamedUser)})
		admin := bearerOf(s.tokens["alice"])
		for _, action := range []string{"catalog-search", "addons-installed", "catalog-sources"} {
			r := send(front, "POST", "/api/addons/dispatch", dispatchBody("lost-read-"+action, "", action, 0, `{}`), admin)
			Expect(r.code).To(Equal(503), action)
			Expect(r.errType()).To(Equal("addon_error"), action)
			Expect(r.errOperation()).To(BeEmpty(), action)
		}
		for _, call := range []struct{ action, arguments string }{
			{"catalog-source-save", `{"id":"lost","name":"Lost","url":"https://library.example/releases.yaml","enabled":false}`},
			{"catalog-source-refresh", `{"sourceId":"test"}`},
			{"upgrade", `{"sourceId":"test","version":"1.0.0"}`},
		} {
			r := send(front, "POST", "/api/addons/dispatch", dispatchBody("lost-"+call.action, "notes", call.action, 0, call.arguments), admin)
			Expect(r.code).To(Equal(503), call.action)
			Expect(r.errType()).To(Equal("outcome_unknown"), call.action)
			Expect(r.errOperation()).To(Equal("lost-" + call.action))
		}
	})

	It("documents the management actions and every emitted field of the installed view", func() {
		spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths map[string]map[string]struct {
				Description string `json:"description"`
			} `json:"paths"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		description := doc.Paths["/api/addons/dispatch"]["post"].Description
		for _, action := range []string{"catalog-search", "catalog-details", "catalog-sources", "catalog-source-save", "catalog-source-refresh", "addons-installed", "upgrade"} {
			Expect(description).To(ContainSubstring(action))
		}
		// Every field the installed view emits is described in the feature docs.
		page, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "content", "features", "addons.md"))
		Expect(err).NotTo(HaveOccurred())
		full := apps.Installed{ID: "a", Title: "t", Description: "d", Icon: "i", Version: "1", Generation: 1, Enabled: true, State: "running", SourceID: "s", Channel: "c", Selection: "1", Verification: "sha256", Digest: "sha256:x",
			Platform: apps.Platform{OS: "linux", Arch: "amd64"}, Compatibility: apps.Compatibility{Host: contract.VersionRange{Min: 1, Max: 1}, API: contract.VersionRange{Min: 1, Max: 1}, Protocol: contract.VersionRange{Min: 1, Max: 1}, DataFormat: contract.VersionRange{Min: 1, Max: 1}},
			Tools: []string{"x"}, ToolCount: 1, HasUI: true, MainPage: "/m", LaunchMode: "always", Generations: 1, Restarts: 2, Storage: apps.Storage{Bytes: 1, Truncated: true}, Update: &apps.Update{Available: true, Version: "2", CheckedAt: "2026-01-01T00:00:00Z"}}
		b, err := json.Marshal(full)
		Expect(err).NotTo(HaveOccurred())
		var shape map[string]any
		Expect(json.Unmarshal(b, &shape)).To(Succeed())
		Expect(len(shape)).To(BeNumerically(">=", 20))
		for field := range shape {
			Expect(string(page)).To(ContainSubstring("`"+field+"`"), "the docs describe the installed field "+field)
		}
	})
})
