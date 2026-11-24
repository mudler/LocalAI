// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

type listReposArgs struct {
	Page    int `json:"page"`
	PerPage int `json:"perPage"`
}

// listRepos lists the repositories the token can reach, as GitHub says. The
// host runs it only for a connection that lists * (every repository the token
// reaches): that list is the permission, so the add-on asks GitHub and does
// not narrow the answer. The key it gets is *; any other key is a bug of the
// caller and is refused.
func (c call) listRepos(raw map[string]json.RawMessage) (any, error) {
	var a listReposArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if c.key != "*" {
		return nil, bad("the host did not allow every repository for this call")
	}
	page, per := pageOr(a.Page, 1), perPageOr(a.PerPage, 30)
	q := url.Values{"sort": {"full_name"}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(per)}}
	var list []struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Archived      bool   `json:"archived"`
		HTMLURL       string `json:"html_url"`
		Permissions   struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	resp, err := c.r.API.GetJSON(c.ctx, c.token, "/user/repos", q, &list)
	if err != nil {
		return nil, err
	}
	type item struct {
		FullName      string `json:"fullName"`
		DefaultBranch string `json:"defaultBranch"`
		Private       bool   `json:"private"`
		Archived      bool   `json:"archived"`
		CanWrite      bool   `json:"canWrite"`
		URL           string `json:"url"`
	}
	items := make([]item, 0, len(list))
	for _, r := range list {
		// A name that is not owner/name is dropped; it could not be used anyway.
		if checkRepo(r.FullName) != nil || strings.Contains(r.FullName, "*") {
			continue
		}
		items = append(items, item{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private, Archived: r.Archived, CanWrite: r.Permissions.Push, URL: r.HTMLURL})
	}
	res := map[string]any{"page": page, "items": items}
	if resp.NextPage() {
		res["next"] = page + 1
	}
	return res, nil
}
