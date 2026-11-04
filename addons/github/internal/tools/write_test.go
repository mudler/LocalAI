// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
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

var _ = Describe("the write tools", func() {
	var (
		srv  *githubtest.Server
		repo *githubtest.Repo
		rt   *tools.Runtime
		ctx  = context.Background()
		seq  int
	)
	newRuntime := func(dir string) *tools.Runtime {
		api := &ghapi.Client{Base: srv.URL, HTTP: srv.Client(), Now: time.Now}
		r := tools.New(api)
		if dir != "" {
			r.WithDataDir(dir)
		}
		return r
	}
	BeforeEach(func() {
		seq = 0
		srv = githubtest.New()
		DeferCleanup(srv.Close)
		srv.AddToken(token, "octo")
		repo = srv.AddRepo("acme/widgets")
		repo.Issues = []*githubtest.Issue{
			{Number: 1, Title: "Bug", Body: "it breaks", State: "open", User: "ann", Labels: []string{"bug"}},
			{Number: 2, Title: "Fix", State: "open", User: "bob", IsPR: true},
			{Number: 3, Title: "Locked", State: "open", User: "ann", Locked: true},
		}
		repo.PRs = []*githubtest.PR{{Number: 2, Title: "Fix", State: "open", User: "bob", Base: "main", Head: "fix", HeadSHA: "deadbeef"}}
		rt = newRuntime("")
	})

	envelope := func(tool, key, op string, args map[string]any) contract.Envelope {
		args["tool"] = tool
		raw, _ := json.Marshal(args)
		return contract.Envelope{Request: contract.Request{AddonID: "github", OperationID: op, Arguments: raw, Credential: token, ResourceKey: key}}
	}
	opID := func() string { seq++; return "op-" + string(rune('a'+seq)) }
	posts := func() int {
		n := 0
		for _, c := range srv.Calls() {
			if c.Method != "" && c.Method != "GET" {
				n++
			}
		}
		return n
	}
	preview := func(tool string, args map[string]any) (contract.Preview, error) {
		return rt.Preview(ctx, envelope(tool, "acme/widgets", opID(), args))
	}
	// do previews then applies, as the host does, and returns the receipt.
	do := func(r *tools.Runtime, tool string, args map[string]any) contract.Receipt {
		env := envelope(tool, "acme/widgets", opID(), args)
		p, err := r.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		rc, err := r.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		return rc
	}

	It("previews a comment with plain facts and changes nothing", func() {
		p, err := preview(tools.ToolCommentIssue, map[string]any{"repo": "acme/widgets", "number": 1, "body": strings.Repeat("hello ", 400)})
		Expect(err).NotTo(HaveOccurred())
		Expect(posts()).To(BeZero())
		var s struct {
			Count  int            `json:"count"`
			Sample map[string]any `json:"sample"`
		}
		Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
		Expect(s.Count).To(Equal(1))
		Expect(s.Sample).To(HaveKeyWithValue("targetTitle", "Bug"))
		Expect(s.Sample).To(HaveKeyWithValue("bodyCut", true))
		Expect(s.Sample).To(HaveKeyWithValue("public", true))
		Expect(len(p.Summary)).To(BeNumerically("<", 2200), "short enough for a chat")
	})

	It("posts a comment once, with the token in the header only, and answers with a link", func() {
		rc := do(rt, tools.ToolCommentIssue, map[string]any{"repo": "acme/widgets", "number": 1, "body": "Thanks, looking."})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues[0].Comments).To(HaveLen(1))
		Expect(repo.Issues[0].Comments[0]).To(HavePrefix("Thanks, looking."))
		Expect(repo.Issues[0].Comments[0]).To(ContainSubstring("localai-op:"))
		var r reply
		Expect(json.Unmarshal(rc.Result.Payload, &r)).To(Succeed())
		Expect(r.Untrusted).To(BeTrue())
		Expect(r.Data["url"]).To(ContainSubstring("https://github.com/acme/widgets/issues/1#issuecomment-"))
		Expect(len(rc.Result.Payload)).To(BeNumerically("<=", 512), "small enough for the chat to copy")
		for _, c := range srv.Calls() {
			Expect(c.Token).To(Equal(token))
			Expect(c.Path + c.Query + c.Body).NotTo(ContainSubstring("SECRET"))
		}
		Expect(string(rc.Result.Payload)).NotTo(ContainSubstring(token))
	})

	It("answers a repeat of the same operation from its receipt and posts nothing more", func() {
		env := envelope(tools.ToolCommentIssue, "acme/widgets", "op-same", map[string]any{"repo": "acme/widgets", "number": 1, "body": "once"})
		p, err := rt.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		first, err := rt.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		again, err := rt.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(first))
		Expect(repo.Issues[0].Comments).To(HaveLen(1))
		got, err := rt.Receipt(ctx, "op-same")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(contract.ReceiptCommitted))
	})

	It("keeps its receipts across a restart", func() {
		dir := GinkgoT().TempDir()
		r1 := newRuntime(dir)
		env := envelope(tools.ToolCommentIssue, "acme/widgets", "op-keep", map[string]any{"repo": "acme/widgets", "number": 1, "body": "once"})
		p, err := r1.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		_, err = r1.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		r2 := newRuntime(dir)
		_, err = r2.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(repo.Issues[0].Comments).To(HaveLen(1))
	})

	It("does not repeat a comment whose answer was lost: it finds its marker, and says unknown meanwhile", func() {
		env := envelope(tools.ToolCommentIssue, "acme/widgets", "op-lost", map[string]any{"repo": "acme/widgets", "number": 1, "body": "maybe twice"})
		p, err := rt.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		srv.DropWrites(true)
		_, err = rt.Apply(ctx, env, p.Revision)
		Expect(err).To(HaveOccurred())
		Expect(repo.Issues[0].Comments).To(HaveLen(1), "GitHub did get it")
		got, _ := rt.Receipt(ctx, "op-lost")
		Expect(got.State).To(Equal(contract.ReceiptUnknown))
		srv.DropWrites(false)
		rc, err := rt.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues[0].Comments).To(HaveLen(1), "found by its marker, not sent again")
	})

	It("refuses a repository other than the allowed one before any call", func() {
		_, err := rt.Preview(ctx, envelope(tools.ToolCommentIssue, "acme/other", "op-x", map[string]any{"repo": "acme/widgets", "number": 1, "body": "x"}))
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = rt.Preview(ctx, envelope(tools.ToolCommentIssue, "", "op-x", map[string]any{"repo": "acme/widgets", "number": 1, "body": "x"}))
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(srv.Calls()).To(BeEmpty())
	})

	It("refuses text with a secret, an oversize text and an empty one, before any call", func() {
		for _, body := range []string{
			"my token is " + token,
			"use ghp_abcdefghijklmnopqrstuvwxyz0123456789 here",
			"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz",
			"-----BEGIN RSA PRIVATE KEY-----\nabc",
			"Authorization: Bearer abcdef",
			strings.Repeat("a", tools.MaxWriteBody+1),
			"   ",
			"bell\x07",
		} {
			_, err := preview(tools.ToolCommentIssue, map[string]any{"repo": "acme/widgets", "number": 1, "body": body})
			Expect(err).To(MatchError(protocol.ErrBadRequest), body[:min(len(body), 20)])
			Expect(err.Error()).NotTo(ContainSubstring(token))
		}
		_, err := preview(tools.ToolCreateIssue, map[string]any{"repo": "acme/widgets", "title": "has " + token})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolAddLabels, map[string]any{"repo": "acme/widgets", "number": 1, "labels": []string{"a,b"}})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(srv.Calls()).To(BeEmpty())
	})

	It("keeps each comment tool on its own kind of target and refuses a locked conversation", func() {
		_, err := preview(tools.ToolCommentIssue, map[string]any{"repo": "acme/widgets", "number": 2, "body": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolCommentPR, map[string]any{"repo": "acme/widgets", "number": 1, "body": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolCommentIssue, map[string]any{"repo": "acme/widgets", "number": 3, "body": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolCommentPR, map[string]any{"repo": "acme/widgets", "number": 2, "body": "x"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("opens an issue with labels and a marker", func() {
		rc := do(rt, tools.ToolCreateIssue, map[string]any{"repo": "acme/widgets", "title": "New problem", "body": "details", "labels": []string{"bug", "help wanted"}})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues).To(HaveLen(4))
		i := repo.Issues[3]
		Expect(i.Title).To(Equal("New problem"))
		Expect(i.Body).To(ContainSubstring("localai-op:"))
		Expect(i.Labels).To(ConsistOf("bug", "help wanted"))
		var r reply
		Expect(json.Unmarshal(rc.Result.Payload, &r)).To(Succeed())
		Expect(r.Data["number"]).To(BeEquivalentTo(4))
	})

	It("does not open a second issue when the first answer was lost", func() {
		env := envelope(tools.ToolCreateIssue, "acme/widgets", "op-issue", map[string]any{"repo": "acme/widgets", "title": "Once", "body": "b"})
		p, err := rt.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		srv.DropWrites(true)
		_, err = rt.Apply(ctx, env, p.Revision)
		Expect(err).To(HaveOccurred())
		srv.DropWrites(false)
		rc, err := rt.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues).To(HaveLen(4))
	})

	It("closes an issue with a reason, and refuses to close what is closed", func() {
		rc := do(rt, tools.ToolEditIssue, map[string]any{"repo": "acme/widgets", "number": 1, "state": "closed", "reason": "not_planned"})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues[0].State).To(Equal("closed"))
		Expect(repo.Issues[0].Reason).To(Equal("not_planned"))
		_, err := preview(tools.ToolEditIssue, map[string]any{"repo": "acme/widgets", "number": 1, "state": "closed"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolEditIssue, map[string]any{"repo": "acme/widgets", "number": 1})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolEditIssue, map[string]any{"repo": "acme/widgets", "number": 1, "state": "open", "reason": "completed"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
	})

	It("rejects an approval whose target changed after the preview, and sends nothing", func() {
		env := envelope(tools.ToolEditIssue, "acme/widgets", "op-stale", map[string]any{"repo": "acme/widgets", "number": 1, "title": "Better title"})
		p, err := rt.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		repo.Issues[0].Title = "Someone renamed it"
		before := posts()
		rc, err := rt.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(posts()).To(Equal(before))
		Expect(repo.Issues[0].Title).To(Equal("Someone renamed it"))
	})

	It("adds labels without removing the others", func() {
		rc := do(rt, tools.ToolAddLabels, map[string]any{"repo": "acme/widgets", "number": 1, "labels": []string{"bug", "urgent"}})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues[0].Labels).To(ConsistOf("bug", "urgent"))
	})

	It("comments on a line of a pull request at the head commit, and refuses an issue number", func() {
		rc := do(rt, tools.ToolReviewCommentPR, map[string]any{"repo": "acme/widgets", "number": 2, "path": "src/a.go", "line": 7, "body": "nit"})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.PRs[0].Inline).To(HaveLen(1))
		c := repo.PRs[0].Inline[0]
		Expect([]any{c.Path, c.Line, c.Side, c.CommitID}).To(Equal([]any{"src/a.go", 7, "RIGHT", "deadbeef"}))
		_, err := preview(tools.ToolReviewCommentPR, map[string]any{"repo": "acme/widgets", "number": 1, "path": "a.go", "line": 1, "body": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = preview(tools.ToolReviewCommentPR, map[string]any{"repo": "acme/widgets", "number": 2, "path": "../a.go", "line": 1, "body": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
	})

	It("turns a rate limit on apply into a refusal that says when to retry, and can try again after", func() {
		env := envelope(tools.ToolCommentIssue, "acme/widgets", "op-limit", map[string]any{"repo": "acme/widgets", "number": 1, "body": "later"})
		p, err := rt.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		srv.LimitSecondary(30)
		_, err = rt.Apply(ctx, env, p.Revision)
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(err.Error()).To(ContainSubstring("30 seconds"))
		srv.Unlimit()
		got, _ := rt.Receipt(ctx, "op-limit")
		Expect(got.State).To(Equal(contract.ReceiptUnknown))
		rc, err := rt.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(repo.Issues[0].Comments).To(HaveLen(1))
	})

	It("never puts the token in an error", func() {
		srv.Fail("/repos/acme/widgets/issues/1/comments", 403)
		env := envelope(tools.ToolCommentIssue, "acme/widgets", "op-403", map[string]any{"repo": "acme/widgets", "number": 1, "body": "x"})
		p, err := rt.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		_, err = rt.Apply(ctx, env, p.Revision)
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(err.Error()).NotTo(ContainSubstring(token))
		env.Request.Credential = "github_pat_bad"
		_, err = rt.Preview(ctx, env)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("github_pat_bad"))
	})

	It("is not reachable through the read route", func() {
		raw, _ := json.Marshal(map[string]any{"tool": tools.ToolCommentIssue, "repo": "acme/widgets", "number": 1, "body": "x"})
		_, err := rt.Read(ctx, contract.Envelope{Request: contract.Request{Arguments: raw, Credential: token, ResourceKey: "acme/widgets"}})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(posts()).To(BeZero())
	})
})
