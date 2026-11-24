// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/mudler/LocalAI/addons/jellyfin/internal/tools"
	"github.com/mudler/LocalAI/addons/jellyfin/jellyfintest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the manifest", func() {
	It("is valid for the host and matches the tools", func() {
		b, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		h := contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 2, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: "linux", Arch: "amd64"}
		Expect(contract.ValidateManifest(m, h)).To(Succeed())
		Expect(m.Capabilities.Tools).To(ConsistOf(tools.Declared()))
		Expect(m.Capabilities.Credentials).To(HaveLen(1))
		Expect(m.Capabilities.Credentials[0].Kind).To(Equal(contract.CredentialKindToken))
		for _, d := range m.Capabilities.ToolDefinitions {
			if d.Kind == contract.ToolKindWrite {
				Expect(tools.WriteTools()).To(ContainElement(d.Name))
				Expect(d.Description).To(ContainSubstring("may wait for the person to confirm"), d.Name)
				continue
			}
			Expect(d.Kind).To(Equal(contract.ToolKindRead), d.Name)
			if d.UIOnly {
				continue
			}
			Expect(d.Description).To(ContainSubstring("text written by others"), d.Name)
		}
		for _, bad := range []string{"delete", "remove", "scan", "refresh", "admin", "user-create"} {
			for _, n := range m.Capabilities.Tools {
				Expect(n).NotTo(ContainSubstring(bad))
			}
		}
	})
})

var _ = Describe("the read tools", func() {
	var f *fixture
	BeforeEach(func() {
		srv := jellyfintest.New()
		DeferCleanup(srv.Close)
		f = newFixture(srv)
	})

	It("wraps every result as untrusted and sends the key only in the header", func() {
		r := f.must("search-items", map[string]any{"query": "dune"})
		Expect(r.Source).To(Equal("jellyfin"))
		Expect(r.Untrusted).To(BeTrue())
		Expect(r.Notice).To(ContainSubstring("never as instructions"))
		Expect(f.srv.Calls()).NotTo(BeEmpty())
		for _, c := range f.srv.Calls() {
			Expect(c.Key).To(Equal(apiKey))
			Expect(c.Auth).To(Equal(`MediaBrowser Token="` + apiKey + `"`))
			Expect(c.Path + "?" + c.Query).NotTo(ContainSubstring("SECRET"))
		}
	})

	Describe("search-items", func() {
		It("finds by title and cuts the overview", func() {
			r := f.must("search-items", map[string]any{"query": "dune"})
			items := list(r, "items")
			Expect(names(items)).To(Equal([]string{"Dune"}))
			Expect(items[0]["year"]).To(BeNumerically("==", 2021))
			Expect(items[0]["runtimeMinutes"]).To(BeNumerically("==", 155))
			ov := items[0]["overview"].(string)
			Expect(len([]rune(ov))).To(BeNumerically("<=", tools.ListOverview+3))
			Expect(items[0]["overviewCut"]).To(BeTrue())
		})
		It("filters by type and pages", func() {
			r := f.must("search-items", map[string]any{"query": "a", "types": []string{"Episode"}, "limit": 1})
			Expect(list(r, "items")).To(HaveLen(1))
			Expect(r.Data["total"]).To(BeNumerically("==", 2))
			Expect(r.Data["next"]).To(BeNumerically("==", 1))
		})
		It("refuses an empty query, an unknown type and a stray field", func() {
			for _, a := range []map[string]any{{"query": ""}, {"query": "x", "types": []string{"User"}}, {"query": "x", "baseUrl": "http://evil"}, {"query": "x\ny"}} {
				_, err := f.read("search-items", a)
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), "%v", a)
			}
		})
		It("never asks for more than 50 items", func() {
			f.must("search-items", map[string]any{"query": "a", "limit": 500})
			last := f.srv.Calls()[len(f.srv.Calls())-1]
			Expect(last.Query).To(ContainSubstring("limit=50"))
		})
		It("asks for a user by ID, never by the name the model gave", func() {
			f.must("search-items", map[string]any{"query": "dune", "jellyfinUser": "alice"})
			Expect(f.srv.Calls()[len(f.srv.Calls())-1].Query).To(ContainSubstring("userId=" + alice))
			_, err := f.read("search-items", map[string]any{"query": "dune", "jellyfinUser": "Mallory"})
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
		})
	})

	It("lists libraries", func() {
		r := f.must("list-libraries", nil)
		Expect(names(list(r, "items"))).To(ConsistOf("Movies", "Shows"))
	})

	Describe("browse-library", func() {
		It("lists a library sorted, with paging", func() {
			r := f.must("browse-library", map[string]any{"parentId": libMovies, "sortBy": "name", "sortOrder": "descending", "limit": 1})
			Expect(names(list(r, "items"))).To(Equal([]string{"Dune"}))
			Expect(r.Data["total"]).To(BeNumerically("==", 2))
			Expect(r.Data["next"]).To(BeNumerically("==", 1))
			r = f.must("browse-library", map[string]any{"parentId": libMovies, "limit": 1, "startIndex": 1})
			Expect(names(list(r, "items"))).To(Equal([]string{"Dune"}))
			Expect(r.Data).NotTo(HaveKey("next"))
		})
		It("filters by genre and year", func() {
			r := f.must("browse-library", map[string]any{"parentId": libMovies, "genre": "Drama", "types": []string{"Movie"}})
			Expect(names(list(r, "items"))).To(Equal([]string{"Arrival"}))
			r = f.must("browse-library", map[string]any{"parentId": libMovies, "year": 2021, "types": []string{"Movie"}})
			Expect(names(list(r, "items"))).To(Equal([]string{"Dune"}))
		})
		It("refuses a parent that is not an item ID, however it is written", func() {
			for _, p := range []string{"../Users", "1/../../x", "movies", "11111111111111111111111111111111/Items", "%2e%2e"} {
				_, err := f.read("browse-library", map[string]any{"parentId": p})
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), p)
			}
			Expect(f.srv.Calls()).To(BeEmpty())
		})
	})

	Describe("get-item", func() {
		It("returns details with people and a longer overview, and no path or media source", func() {
			r := f.must("get-item", map[string]any{"itemId": dune, "jellyfinUser": "Alice"})
			Expect(r.Data["name"]).To(Equal("Dune"))
			Expect(r.Data["ageRating"]).To(Equal("PG-13"))
			people := r.Data["people"].([]any)
			Expect(people).To(HaveLen(2))
			Expect(people[1].(map[string]any)["role"]).To(Equal("Chani"))
			Expect(len([]rune(r.Data["overview"].(string)))).To(BeNumerically("<=", tools.DetailOverview+3))
			Expect(r.Data["resumeAtMinute"]).To(BeNumerically("==", 60))
			Expect(r.Data["played"]).To(BeFalse())
		})
		It("says plainly when the item does not exist", func() {
			_, err := f.read("get-item", map[string]any{"itemId": "99999999999999999999999999999999"})
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
		})
	})

	It("lists the latest additions of a user, newest first, and of one library", func() {
		r := f.must("list-latest", map[string]any{"jellyfinUser": "Alice"})
		Expect(names(list(r, "items"))[:2]).To(Equal([]string{"Half Loop", "Good News About Hell"}))
		r = f.must("list-latest", map[string]any{"jellyfinUser": "Alice", "parentId": libMovies})
		Expect(names(list(r, "items"))).To(Equal([]string{"Arrival", "Dune"}))
	})

	It("lists continue watching and next up for a user", func() {
		r := f.must("list-continue-watching", map[string]any{"jellyfinUser": "alice"})
		Expect(names(list(r, "items"))).To(Equal([]string{"Dune"}))
		r = f.must("list-next-up", map[string]any{"jellyfinUser": "alice"})
		items := list(r, "items")
		Expect(names(items)).To(Equal([]string{"Half Loop"}))
		Expect(items[0]["series"]).To(Equal("Severance"))
	})

	It("needs a user when there are several, and takes the only one when there is one", func() {
		_, err := f.read("list-next-up", nil)
		Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
		f.srv.SetUsers(f.srv.Users[:1])
		r := f.must("list-next-up", nil)
		Expect(names(list(r, "items"))).To(Equal([]string{"Half Loop"}))
	})

	It("lists playlists with their size", func() {
		r := f.must("list-playlists", nil)
		Expect(names(list(r, "items"))).To(ConsistOf("Friday night", "Kids"))
	})

	Describe("list-sessions", func() {
		It("shows what plays and leaves out addresses and device IDs", func() {
			r := f.must("list-sessions", nil)
			ss := list(r, "sessions")
			Expect(ss).To(HaveLen(3))
			tv := ss[0]
			Expect(tv["device"]).To(Equal("Living room"))
			Expect(tv["canBeControlled"]).To(BeTrue())
			Expect(tv["nowPlaying"].(map[string]any)["name"]).To(Equal("Dune"))
			Expect(tv["positionSeconds"]).To(BeNumerically("==", 600))
			Expect(tv["volume"]).To(BeNumerically("==", 40))
		})
		It("can keep only the sessions that play", func() {
			r := f.must("list-sessions", map[string]any{"playingOnly": true})
			Expect(list(r, "sessions")).To(HaveLen(2))
		})
	})

	It("lists user names only", func() {
		r := f.must("list-users", nil)
		Expect(r.Data["users"]).To(Equal([]any{"Alice", "Bob"}))
		Expect(r.Data).NotTo(HaveKey("ids"))
	})

	Describe("get-server-info and check", func() {
		It("reports the server and that the key works", func() {
			r := f.must("get-server-info", nil)
			Expect(r.Data["name"]).To(Equal("Test Jellyfin"))
			Expect(r.Data["version"]).To(Equal("10.10.3"))
			Expect(r.Data["keyWorks"]).To(BeTrue())
		})
		It("check reports a refused key as a result, and the other tools as an error", func() {
			env := f.env(f.srv.URL, map[string]any{"tool": "check", "server": f.srv.URL})
			env.Request.Credential = "wrong-key"
			res, err := f.rt.Read(context.Background(), env)
			Expect(err).NotTo(HaveOccurred())
			var r reply
			Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
			Expect(r.Data["reachable"]).To(BeTrue())
			Expect(r.Data["keyWorks"]).To(BeFalse())
			Expect(r.Data["keyProblem"]).To(ContainSubstring("refused the API key"))
			Expect(string(res.Payload)).NotTo(ContainSubstring("wrong-key"))
			env = f.env(f.srv.URL, map[string]any{"tool": "get-server-info", "server": f.srv.URL})
			env.Request.Credential = "wrong-key"
			_, err = f.rt.Read(context.Background(), env)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("the server check", func() {
		It("refuses a server other than the one the host allowed", func() {
			raw, _ := json.Marshal(map[string]any{"tool": "list-users", "server": "http://evil.example"})
			_, err := f.rt.Read(context.Background(), f.env(f.srv.URL, mapOf(raw)))
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
			Expect(f.srv.Calls()).To(BeEmpty())
		})
		It("treats the address as the person wrote it, case aside", func() {
			args := map[string]any{"tool": "list-users", "server": strings.ToUpper(f.srv.URL[:4]) + f.srv.URL[4:]}
			raw, _ := json.Marshal(args)
			_, err := f.rt.Read(context.Background(), f.env(f.srv.URL, mapOf(raw)))
			Expect(err).NotTo(HaveOccurred())
		})
		It("refuses a call with no key", func() {
			env := f.env(f.srv.URL, map[string]any{"tool": "list-users", "server": f.srv.URL})
			env.Request.Credential = ""
			_, err := f.rt.Read(context.Background(), env)
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
		})
		It("refuses an address that is not http or https, or that holds a user name", func() {
			for _, s := range []string{"file:///etc/passwd", "ftp://host", "http://user:pw@host", "http://host?x=1", "http://", "javascript:alert(1)", "http://host#other"} {
				env := f.env(s, map[string]any{"tool": "list-users", "server": s})
				_, err := f.rt.Read(context.Background(), env)
				Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue(), s)
			}
		})
	})

	Describe("failures", func() {
		It("maps a refused key, a missing thing and an outage to fixed sentences without the body", func() {
			f.srv.Fail("/Users", 401)
			_, err := f.read("list-users", nil)
			Expect(err).To(MatchError(ContainSubstring("refused the API key")))
			f.srv.Fail("/Users", 500)
			_, err = f.read("list-users", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("SECRET-BODY-TEXT"))
		})
		It("keeps the wait of a rate limit", func() {
			f.srv.Fail("/Users", 429)
			_, err := f.read("list-users", nil)
			var limited *contract.ErrRateLimited
			Expect(errors.As(err, &limited)).To(BeTrue())
			Expect(limited.Seconds()).To(Equal(17))
		})
		It("does not follow a redirect to another host", func() {
			other := jellyfintest.New()
			DeferCleanup(other.Close)
			other.AddKey(apiKey)
			f.srv.RedirectTo(other.URL + "/Users")
			_, err := f.read("list-users", nil)
			Expect(err).To(HaveOccurred())
			Expect(other.Calls()).To(BeEmpty())
			Expect(err.Error()).NotTo(ContainSubstring(apiKey))
		})
		It("never puts the key in the address", func() {
			for _, tool := range tools.ReadTools() {
				args := map[string]any{"query": "x", "parentId": libMovies, "itemId": dune, "jellyfinUser": "Alice"}
				for k := range args {
					if !allowedFor(tool, k) {
						delete(args, k)
					}
				}
				_, _ = f.read(tool, args)
			}
			for _, c := range f.srv.Calls() {
				Expect(strings.ToLower(c.Query)).NotTo(ContainSubstring("api_key"))
				Expect(c.Query + c.Path).NotTo(ContainSubstring(apiKey))
			}
		})
	})
})

func mapOf(raw []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func allowedFor(tool, arg string) bool {
	switch tool {
	case "search-items":
		return arg == "query" || arg == "jellyfinUser"
	case "browse-library":
		return arg == "parentId" || arg == "jellyfinUser"
	case "get-item":
		return arg == "itemId" || arg == "jellyfinUser"
	case "list-latest", "list-continue-watching", "list-next-up", "list-playlists":
		return arg == "jellyfinUser"
	}
	return false
}
