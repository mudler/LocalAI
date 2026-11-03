// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
)

type person struct {
	Login string `json:"login"`
}

type labelJSON struct {
	Name string `json:"name"`
}

func names(ls []labelJSON) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Name)
	}
	return out
}

type listIssuesArgs struct {
	Repo    string `json:"repo"`
	State   string `json:"state"`
	Kind    string `json:"kind"`
	Labels  string `json:"labels"`
	Page    int    `json:"page"`
	PerPage int    `json:"perPage"`
}

func (c call) listIssues(raw map[string]json.RawMessage) (any, error) {
	var a listIssuesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	switch a.State {
	case "":
		a.State = "open"
	case "open", "closed", "all":
	default:
		return nil, bad("state is open closed or all")
	}
	switch a.Kind {
	case "":
		a.Kind = "issue"
	case "issue", "pr", "all":
	default:
		return nil, bad("kind is issue pr or all")
	}
	if len(a.Labels) > 200 || strings.ContainsFunc(a.Labels, unicode.IsControl) {
		return nil, bad("labels is not valid")
	}
	page, per := pageOr(a.Page, 1), perPageOr(a.PerPage, 30)
	q := url.Values{"state": {a.State}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(per)}}
	if a.Labels != "" {
		q.Set("labels", a.Labels)
	}
	var list []struct {
		Number      int         `json:"number"`
		Title       string      `json:"title"`
		State       string      `json:"state"`
		User        person      `json:"user"`
		Labels      []labelJSON `json:"labels"`
		Comments    int         `json:"comments"`
		CreatedAt   string      `json:"created_at"`
		UpdatedAt   string      `json:"updated_at"`
		HTMLURL     string      `json:"html_url"`
		PullRequest *struct{}   `json:"pull_request"`
	}
	resp, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo+"/issues", q, &list)
	if err != nil {
		return nil, err
	}
	type item struct {
		Number   int      `json:"number"`
		Title    string   `json:"title"`
		State    string   `json:"state"`
		Author   string   `json:"author"`
		Labels   []string `json:"labels"`
		Comments int      `json:"comments"`
		Created  string   `json:"created"`
		Updated  string   `json:"updated"`
		URL      string   `json:"url"`
		IsPR     bool     `json:"isPullRequest"`
		TitleCut bool     `json:"titleCut,omitempty"`
	}
	items := make([]item, 0, len(list))
	for _, i := range list {
		isPR := i.PullRequest != nil
		if (a.Kind == "issue" && isPR) || (a.Kind == "pr" && !isPR) {
			continue
		}
		title, omitted := cut(i.Title, 300)
		items = append(items, item{Number: i.Number, Title: title, TitleCut: omitted > 0, State: i.State, Author: i.User.Login, Labels: names(i.Labels), Comments: i.Comments,
			Created: i.CreatedAt, Updated: i.UpdatedAt, URL: i.HTMLURL, IsPR: isPR})
	}
	res := map[string]any{"repo": a.Repo, "state": a.State, "kind": a.Kind, "page": page, "items": items}
	if resp.NextPage() {
		res["next"] = page + 1
		res["note"] = fmt.Sprintf("more on page=%d; this page lists the items of GitHub's page, so a page can hold fewer than perPage", page+1)
	}
	return res, nil
}

type getIssueArgs struct {
	Repo         string `json:"repo"`
	Number       int    `json:"number"`
	CommentsPage int    `json:"commentsPage"`
}

// commentsPerPage is how many comments one result asks GitHub for.
const commentsPerPage = 20

type commentOut struct {
	Author    string `json:"author"`
	Created   string `json:"created"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated,omitempty"`
	Omitted   int    `json:"omittedBytes,omitempty"`
}

// budget hands out the bytes of user text a result may carry.
type budget struct{ left int }

// text cuts s to max bytes and to what is left of the budget.
func (b *budget) text(s string, max int) (out string, omitted int) {
	out, omitted = cut(s, min(max, max0(b.left)))
	b.left -= len(out)
	return out, omitted
}

func max0(n int) int { return max(n, 0) }

func (c call) comments(ctx context.Context, repo string, number, page int, b *budget) (outs []commentOut, next bool, err error) {
	var list []struct {
		User      person `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	resp, err := c.r.API.GetJSON(ctx, c.token, "/repos/"+repo+"/issues/"+strconv.Itoa(number)+"/comments",
		url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(commentsPerPage)}}, &list)
	if err != nil {
		return nil, false, err
	}
	next = resp.NextPage()
	for i, cm := range list {
		if b.left <= 0 {
			// The rest of the page does not fit: the caller asks again for the
			// same page after the ones shown. Say so instead of dropping them.
			outs = append(outs, commentOut{Author: "", Body: "", Truncated: true, Omitted: len(list) - i})
			break
		}
		body, omitted := b.text(cm.Body, MaxCommentBody)
		outs = append(outs, commentOut{Author: cm.User.Login, Created: cm.CreatedAt, Body: body, Truncated: omitted > 0, Omitted: omitted})
	}
	return outs, next, nil
}

func (c call) getIssue(raw map[string]json.RawMessage) (any, error) {
	var a getIssueArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	if a.Number < 1 {
		return nil, bad("number is at least 1")
	}
	var issue struct {
		Number      int         `json:"number"`
		Title       string      `json:"title"`
		Body        string      `json:"body"`
		State       string      `json:"state"`
		User        person      `json:"user"`
		Labels      []labelJSON `json:"labels"`
		Comments    int         `json:"comments"`
		CreatedAt   string      `json:"created_at"`
		UpdatedAt   string      `json:"updated_at"`
		HTMLURL     string      `json:"html_url"`
		PullRequest *struct{}   `json:"pull_request"`
	}
	if _, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo+"/issues/"+strconv.Itoa(a.Number), nil, &issue); err != nil {
		return nil, err
	}
	b := &budget{left: MaxTextBudget}
	body, omitted := b.text(issue.Body, MaxIssueBody)
	res := map[string]any{"repo": a.Repo, "number": issue.Number, "title": issue.Title, "state": issue.State, "author": issue.User.Login, "labels": names(issue.Labels),
		"commentCount": issue.Comments, "created": issue.CreatedAt, "updated": issue.UpdatedAt, "url": issue.HTMLURL, "isPullRequest": issue.PullRequest != nil, "body": body}
	if omitted > 0 {
		res["bodyTruncated"], res["bodyOmittedBytes"] = true, omitted
	}
	page := pageOr(a.CommentsPage, 1)
	comments, next, err := c.comments(c.ctx, a.Repo, a.Number, page, b)
	if err != nil {
		return nil, err
	}
	res["comments"], res["commentsPage"] = comments, page
	if comments == nil {
		res["comments"] = []commentOut{}
	}
	if next {
		res["commentsNext"] = page + 1
		res["note"] = fmt.Sprintf("more comments: ask for commentsPage=%d", page+1)
	}
	if n := len(comments); n > 0 && comments[n-1].Truncated && comments[n-1].Author == "" {
		res["note"] = "this page of comments was cut to keep the result small; the comments marked omitted are not shown"
	}
	return res, nil
}

type getPRArgs struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

func (c call) getPR(raw map[string]json.RawMessage) (any, error) {
	var a getPRArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	if a.Number < 1 {
		return nil, bad("number is at least 1")
	}
	var pr struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		State     string `json:"state"`
		Draft     bool   `json:"draft"`
		Merged    bool   `json:"merged"`
		Mergeable *bool  `json:"mergeable"`
		User      person `json:"user"`
		Base      struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Additions      int    `json:"additions"`
		Deletions      int    `json:"deletions"`
		ChangedFiles   int    `json:"changed_files"`
		Commits        int    `json:"commits"`
		Comments       int    `json:"comments"`
		ReviewComments int    `json:"review_comments"`
		CreatedAt      string `json:"created_at"`
		UpdatedAt      string `json:"updated_at"`
		HTMLURL        string `json:"html_url"`
	}
	n := strconv.Itoa(a.Number)
	if _, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo+"/pulls/"+n, nil, &pr); err != nil {
		return nil, err
	}
	var reviews []struct {
		User  person `json:"user"`
		State string `json:"state"`
		Body  string `json:"body"`
	}
	if _, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo+"/pulls/"+n+"/reviews", url.Values{"per_page": {"30"}}, &reviews); err != nil {
		return nil, err
	}
	b := &budget{left: MaxTextBudget}
	body, omitted := b.text(pr.Body, MaxIssueBody)
	type reviewOut struct {
		Author    string `json:"author"`
		State     string `json:"state"`
		Body      string `json:"body,omitempty"`
		Truncated bool   `json:"truncated,omitempty"`
	}
	rs := make([]reviewOut, 0, len(reviews))
	for _, r := range reviews {
		text, om := b.text(r.Body, 2<<10)
		rs = append(rs, reviewOut{Author: r.User.Login, State: r.State, Body: text, Truncated: om > 0})
	}
	res := map[string]any{"repo": a.Repo, "number": pr.Number, "title": pr.Title, "state": pr.State, "draft": pr.Draft, "merged": pr.Merged, "author": pr.User.Login,
		"base": pr.Base.Ref, "head": pr.Head.Ref, "additions": pr.Additions, "deletions": pr.Deletions, "changedFiles": pr.ChangedFiles, "commits": pr.Commits,
		"conversationComments": pr.Comments, "reviewComments": pr.ReviewComments, "created": pr.CreatedAt, "updated": pr.UpdatedAt, "url": pr.HTMLURL, "body": body, "reviews": rs,
		"note": "the conversation comments are read with get-issue; the diff with get-pr-diff"}
	if pr.Mergeable != nil {
		res["mergeable"] = *pr.Mergeable
	}
	if omitted > 0 {
		res["bodyTruncated"], res["bodyOmittedBytes"] = true, omitted
	}
	return res, nil
}

type diffArgs struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Page    int    `json:"page"`
	PerPage int    `json:"perPage"`
	File    string `json:"file"`
}

// maxDiffPages bounds the pages of changed files read to find one file.
const maxDiffPages = 30

type prFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch"`
}

func (c call) getPRDiff(raw map[string]json.RawMessage) (any, error) {
	var a diffArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	if a.Number < 1 {
		return nil, bad("number is at least 1")
	}
	if a.File != "" {
		if err := checkPath(a.File, false); err != nil {
			return nil, err
		}
		return c.diffOfFile(a)
	}
	page, per := pageOr(a.Page, 1), perPageOr(a.PerPage, MaxEntries)
	files, next, err := c.prFiles(a, page, per)
	if err != nil {
		return nil, err
	}
	type fileOut struct {
		Filename  string `json:"filename"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		HasPatch  bool   `json:"hasPatch"`
	}
	list := make([]fileOut, 0, len(files))
	for _, f := range files {
		list = append(list, fileOut{Filename: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, HasPatch: f.Patch != ""})
	}
	res := map[string]any{"repo": a.Repo, "number": a.Number, "page": page, "files": list,
		"note": "this lists the changed files; ask for one file's patch with file=<filename>"}
	if next {
		res["next"] = page + 1
		res["note"] = fmt.Sprintf("more files: ask for page=%d; ask for one file's patch with file=<filename>", page+1)
	}
	return res, nil
}

func (c call) prFiles(a diffArgs, page, per int) ([]prFile, bool, error) {
	var files []prFile
	resp, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo+"/pulls/"+strconv.Itoa(a.Number)+"/files",
		url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(per)}}, &files)
	if err != nil {
		return nil, false, err
	}
	return files, resp.NextPage(), nil
}

func (c call) diffOfFile(a diffArgs) (any, error) {
	for page := 1; page <= maxDiffPages; page++ {
		files, next, err := c.prFiles(a, page, MaxEntries)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.Filename != a.File {
				continue
			}
			res := map[string]any{"repo": a.Repo, "number": a.Number, "file": f.Filename, "status": f.Status, "additions": f.Additions, "deletions": f.Deletions}
			if f.Patch == "" {
				res["patch"], res["note"] = "", "no patch from GitHub: the file is binary or the diff is too large"
				return res, nil
			}
			patch, omitted := cut(f.Patch, MaxPatchBytes)
			res["patch"] = patch
			if omitted > 0 {
				res["patchTruncated"] = true
				res["omittedLines"] = strings.Count(f.Patch[len(patch):], "\n")
				res["note"] = fmt.Sprintf("patch cut at %d of %d bytes", len(patch), len(f.Patch))
			}
			return res, nil
		}
		if !next {
			break
		}
	}
	return nil, bad("that file is not in this pull request")
}

// check answers the Test button of the settings page: who the token is and
// whether it reaches the repository. It returns no part of the token.
func (c call) check(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Repo string `json:"repo"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	var me person
	if _, err := c.r.API.GetJSON(c.ctx, c.token, "/user", nil, &me); err != nil {
		return nil, err
	}
	var repo struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Archived      bool   `json:"archived"`
		Permissions   struct {
			Pull bool `json:"pull"`
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	res := map[string]any{"login": me.Login, "repo": a.Repo}
	if _, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo, nil, &repo); err != nil {
		switch {
		case isNotFound(err):
			res["reachable"], res["reason"] = false, "not found or the token cannot see it"
			return res, nil
		}
		return nil, err
	}
	res["reachable"], res["fullName"], res["defaultBranch"], res["private"], res["archived"] = true, repo.FullName, repo.DefaultBranch, repo.Private, repo.Archived
	res["canRead"], res["canWrite"] = repo.Permissions.Pull, repo.Permissions.Push
	return res, nil
}

func isNotFound(err error) bool { return errors.Is(err, ghapi.ErrNotFound) }
