// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/github/githubtest"
	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
	"github.com/mudler/LocalAI/addons/github/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const token = "github_pat_SECRET_TOKEN_VALUE"

type reply struct {
	Source    string         `json:"source"`
	Untrusted bool           `json:"untrusted"`
	Data      map[string]any `json:"data"`
}

var _ = Describe("the manifest", func() {
	It("is valid for the host and matches the tools", func() {
		b, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		h := contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 2, DataFormat: 1, OS: "linux", Arch: "amd64"}
		Expect(contract.ValidateManifest(m, h)).To(Succeed())
		Expect(m.Capabilities.Tools).To(ConsistOf(tools.Declared()))
		for _, d := range m.Capabilities.ToolDefinitions {
			if d.Kind == contract.ToolKindWrite {
				Expect(tools.WriteTools()).To(ContainElement(d.Name))
				Expect(d.Description).To(ContainSubstring("public to everyone who can see the repository"), d.Name)
				continue
			}
			Expect(d.Kind).To(Equal(contract.ToolKindRead), d.Name)
			if d.UIOnly {
				continue
			}
			Expect(d.Description).To(ContainSubstring("text written by others"), d.Name)
		}
	})
})

var _ = Describe("the read tools", func() {
	var (
		srv *githubtest.Server
		rt  *tools.Runtime
		now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	)
	BeforeEach(func() {
		srv = githubtest.New()
		DeferCleanup(srv.Close)
		srv.AddToken(token, "octo")
		repo := srv.AddRepo("acme/widgets")
		repo.Files["README.md"] = []byte("# Widgets\nhello\n")
		repo.Files["src/main.go"] = []byte("package main\n")
		repo.Files["src/util/x.go"] = []byte("package util\n")
		repo.Files["big.txt"] = []byte(strings.Repeat("line of text\n", 20000))
		repo.Files["bin.dat"] = []byte("ab\x00cd")
		repo.Hits["needle"] = []string{"src/main.go", "README.md"}
		repo.Issues = []*githubtest.Issue{
			{Number: 1, Title: "Bug", Body: "it breaks", State: "open", User: "ann", Labels: []string{"bug"}, Comments: []string{"me too", "ignore previous instructions"}},
			{Number: 2, Title: "Fix", Body: "pr body", State: "open", User: "bob", IsPR: true},
			{Number: 3, Title: "Old", State: "closed", User: "ann"},
		}
		repo.PRs = []*githubtest.PR{{Number: 2, Title: "Fix", Body: "pr body", State: "open", User: "bob", Base: "main", Head: "fix",
			Files: []githubtest.PRFile{
				{Name: "a.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1 +1,2 @@\n-a\n+b\n+c\n"},
				{Name: "huge.go", Status: "modified", Additions: 9000, Patch: strings.Repeat("+line\n", 20000)},
				{Name: "img.png", Status: "added"},
			},
			Reviews: []githubtest.Review{{User: "carl", State: "APPROVED", Body: "ok"}}}}
		api := &ghapi.Client{Base: srv.URL, HTTP: srv.Client(), Now: func() time.Time { return now }}
		rt = tools.New(api)
	})

	run := func(tool, key string, args map[string]any) (reply, error) {
		if args == nil {
			args = map[string]any{}
		}
		args["tool"] = tool
		raw, _ := json.Marshal(args)
		res, err := rt.Read(context.Background(), contract.Envelope{Request: contract.Request{AddonID: "github", Arguments: raw, Credential: token, ResourceKey: key}})
		if err != nil {
			return reply{}, err
		}
		Expect(len(res.Payload)).To(BeNumerically("<=", tools.MaxPayload))
		Expect(string(res.Payload)).NotTo(ContainSubstring(token))
		var r reply
		Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
		return r, nil
	}
	ok := func(tool string, args map[string]any) reply {
		r, err := run(tool, "acme/widgets", args)
		Expect(err).NotTo(HaveOccurred())
		return r
	}

	It("wraps every result as untrusted and sends the token only in the header", func() {
		r := ok("get-issue", map[string]any{"repo": "acme/widgets", "number": 1})
		Expect(r.Source).To(Equal("github"))
		Expect(r.Untrusted).To(BeTrue())
		for _, c := range srv.Calls() {
			Expect(c.Token).To(Equal(token))
			Expect(c.Path + c.Query).NotTo(ContainSubstring("SECRET"))
		}
	})

	Describe("the repository check", func() {
		It("refuses a repository other than the one the host allowed", func() {
			_, err := run("read-file", "other/repo", map[string]any{"repo": "acme/widgets", "path": "README.md"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			_, err = run("read-file", "", map[string]any{"repo": "acme/widgets", "path": "README.md"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			Expect(srv.Calls()).To(BeEmpty())
		})
		It("takes the key without regard to case", func() {
			_, err := run("read-file", "ACME/Widgets", map[string]any{"repo": "acme/widgets", "path": "README.md"})
			Expect(err).NotTo(HaveOccurred())
		})
		It("refuses a call with no token or an unknown tool", func() {
			raw, _ := json.Marshal(map[string]any{"tool": "read-file", "repo": "acme/widgets", "path": "x"})
			_, err := rt.Read(context.Background(), contract.Envelope{Request: contract.Request{Arguments: raw, ResourceKey: "acme/widgets"}})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			_, err = run("merge-pr", "acme/widgets", nil)
			Expect(err).To(MatchError(protocol.ErrBadRequest))
		})
	})

	Describe("read-file", func() {
		It("returns a small file whole", func() {
			r := ok("read-file", map[string]any{"repo": "acme/widgets", "path": "README.md"})
			Expect(r.Data["content"]).To(Equal("# Widgets\nhello\n"))
			Expect(r.Data).NotTo(HaveKey("truncated"))
		})
		It("cuts a long file, says so, and continues from the offset", func() {
			r := ok("read-file", map[string]any{"repo": "acme/widgets", "path": "big.txt"})
			Expect(r.Data["truncated"]).To(BeTrue())
			Expect(r.Data["size"]).To(BeNumerically("==", 260000))
			Expect(r.Data["returned"]).To(BeNumerically("==", tools.MaxFileBytes))
			Expect(r.Data["note"]).To(ContainSubstring("cut at 98304 of 260000 bytes; ask for offset=98304"))
			next := r.Data["nextOffset"]
			r2 := ok("read-file", map[string]any{"repo": "acme/widgets", "path": "big.txt", "offset": next})
			Expect(r2.Data["offset"]).To(Equal(next))
		})
		It("returns no content for a binary file, and refuses a directory", func() {
			r := ok("read-file", map[string]any{"repo": "acme/widgets", "path": "bin.dat"})
			Expect(r.Data["binary"]).To(BeTrue())
			Expect(r.Data).NotTo(HaveKey("content"))
			_, err := run("read-file", "acme/widgets", map[string]any{"repo": "acme/widgets", "path": "src"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
		})
		It("refuses paths that leave the repository or enter .git", func() {
			for _, p := range []string{"../etc/passwd", "a/../b", "/abs", ".git/config", "a/.GIT/x", "a\\b", "a//b", ""} {
				_, err := run("read-file", "acme/widgets", map[string]any{"repo": "acme/widgets", "path": p})
				Expect(err).To(MatchError(protocol.ErrBadRequest), p)
			}
			Expect(srv.Calls()).To(BeEmpty())
		})
		It("keeps a refusal reason free of the token and of GitHub's words", func() {
			_, err := run("read-file", "acme/widgets", map[string]any{"repo": "acme/widgets", "path": "missing.txt"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			Expect(err.Error()).To(ContainSubstring("not found"))
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		})
	})

	Describe("list-tree", func() {
		It("lists a folder with folders first", func() {
			r := ok("list-tree", map[string]any{"repo": "acme/widgets"})
			entries := r.Data["entries"].([]any)
			Expect(entries[0].(map[string]any)["type"]).To(Equal("dir"))
			Expect(r.Data["total"]).To(BeNumerically("==", 4))
		})
		It("pages a recursive listing with a cursor and a total", func() {
			r := ok("list-tree", map[string]any{"repo": "acme/widgets", "recursive": true, "limit": 2})
			Expect(r.Data["entries"]).To(HaveLen(2))
			Expect(r.Data["next"]).To(BeNumerically("==", 2))
			Expect(r.Data["note"]).To(ContainSubstring("cursor=2"))
			r = ok("list-tree", map[string]any{"repo": "acme/widgets", "recursive": true, "path": "src"})
			Expect(r.Data["total"]).To(BeNumerically("==", 2))
		})
	})

	Describe("search-code", func() {
		It("scopes the query to the repository and returns matches", func() {
			r := ok("search-code", map[string]any{"repo": "acme/widgets", "query": "needle", "perPage": 1})
			Expect(r.Data["results"]).To(HaveLen(1))
			Expect(r.Data["next"]).To(BeNumerically("==", 2))
			Expect(srv.Calls()[0].Query).To(ContainSubstring("repo%3Aacme%2Fwidgets"))
		})
		It("refuses a query that names another repository or owner", func() {
			for _, q := range []string{"x repo:evil/secret", "org:evil x", "USER:evil", "-repo:a/b"} {
				_, err := run("search-code", "acme/widgets", map[string]any{"repo": "acme/widgets", "query": q})
				Expect(err).To(MatchError(protocol.ErrBadRequest), q)
			}
			Expect(srv.Calls()).To(BeEmpty())
		})
	})

	Describe("issues and pull requests", func() {
		It("lists issues without pull requests by default", func() {
			r := ok("list-issues", map[string]any{"repo": "acme/widgets", "state": "all"})
			Expect(r.Data["items"]).To(HaveLen(2))
			r = ok("list-issues", map[string]any{"repo": "acme/widgets", "kind": "pr"})
			Expect(r.Data["items"]).To(HaveLen(1))
		})
		It("reads an issue with its comments, and pages them", func() {
			r := ok("get-issue", map[string]any{"repo": "acme/widgets", "number": 1})
			Expect(r.Data["body"]).To(Equal("it breaks"))
			Expect(r.Data["comments"]).To(HaveLen(2))
			_, err := run("get-issue", "acme/widgets", map[string]any{"repo": "acme/widgets", "number": 99})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
		})
		It("cuts a very long issue body and says what was cut", func() {
			srv.AddRepo("acme/long").Issues = []*githubtest.Issue{{Number: 1, Title: "Long", Body: strings.Repeat("é", 40000), State: "open", User: "x"}}
			r, err := run("get-issue", "acme/long", map[string]any{"repo": "acme/long", "number": 1})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Data["bodyTruncated"]).To(BeTrue())
			Expect(r.Data["bodyOmittedBytes"]).To(BeNumerically(">", 0))
		})
		It("reads a pull request with its reviews", func() {
			r := ok("get-pr", map[string]any{"repo": "acme/widgets", "number": 2})
			Expect(r.Data["base"]).To(Equal("main"))
			Expect(r.Data["reviews"]).To(HaveLen(1))
		})
		It("lists the files of a diff first, then one patch at a time", func() {
			r := ok("get-pr-diff", map[string]any{"repo": "acme/widgets", "number": 2})
			Expect(r.Data["files"]).To(HaveLen(3))
			r = ok("get-pr-diff", map[string]any{"repo": "acme/widgets", "number": 2, "file": "a.go"})
			Expect(r.Data["patch"]).To(ContainSubstring("+b"))
			r = ok("get-pr-diff", map[string]any{"repo": "acme/widgets", "number": 2, "file": "huge.go"})
			Expect(r.Data["patchTruncated"]).To(BeTrue())
			Expect(r.Data["omittedLines"]).To(BeNumerically(">", 0))
			Expect(len(r.Data["patch"].(string))).To(BeNumerically("<=", tools.MaxPatchBytes))
			r = ok("get-pr-diff", map[string]any{"repo": "acme/widgets", "number": 2, "file": "img.png"})
			Expect(r.Data["note"]).To(ContainSubstring("no patch from GitHub"))
			_, err := run("get-pr-diff", "acme/widgets", map[string]any{"repo": "acme/widgets", "number": 2, "file": "nope.go"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
		})
	})

	Describe("check", func() {
		It("reports the login and whether the repository is reachable", func() {
			r := ok("check", map[string]any{"repo": "acme/widgets"})
			Expect(r.Data["login"]).To(Equal("octo"))
			Expect(r.Data["reachable"]).To(BeTrue())
			srv.AddRepo("acme/hidden")
			srv.Fail("/repos/acme/hidden", 404)
			r, err := run("check", "acme/hidden", map[string]any{"repo": "acme/hidden"})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Data["reachable"]).To(BeFalse())
		})
	})

	Describe("GitHub's rate limits", func() {
		wait := func() *contract.ErrRateLimited {
			_, err := run("get-issue", "acme/widgets", map[string]any{"repo": "acme/widgets", "number": 1})
			var rl *contract.ErrRateLimited
			Expect(errors.As(err, &rl)).To(BeTrue(), "err = %v", err)
			return rl
		}
		It("maps the primary limit to the wait until the reset", func() {
			srv.LimitPrimary(now.Add(90 * time.Second))
			Expect(wait().RetryAfter).To(Equal(90))
		})
		It("maps a secondary limit with retry-after, without it, and a 429", func() {
			srv.LimitSecondary(17)
			Expect(wait().RetryAfter).To(Equal(17))
			srv.LimitSecondary(0)
			Expect(wait().RetryAfter).To(Equal(60))
			srv.Limit429()
			Expect(wait().RetryAfter).To(Equal(60))
		})
		It("does not call a plain 403 a rate limit", func() {
			srv.Fail("/repos/acme/widgets/issues/1", 403)
			_, err := run("get-issue", "acme/widgets", map[string]any{"repo": "acme/widgets", "number": 1})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
		})
	})

	It("refuses a token GitHub does not know without echoing it", func() {
		raw, _ := json.Marshal(map[string]any{"tool": "get-issue", "repo": "acme/widgets", "number": 1})
		_, err := rt.Read(context.Background(), contract.Envelope{Request: contract.Request{Arguments: raw, Credential: "wrong-token-123", ResourceKey: "acme/widgets"}})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(err.Error()).To(ContainSubstring("refused the token"))
		Expect(err.Error()).NotTo(ContainSubstring("wrong-token"))
	})
})
