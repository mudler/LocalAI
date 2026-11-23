// SPDX-License-Identifier: MIT

// Package search is the runtime of the Ollama search add-on. Ollama has no
// public JSON search API, so the add-on reads two HTML pages of ollama.com: the
// search page (GET /search) and the tags page of one model
// (GET /library/<name>/tags). It never requests a path under /api or /v1,
// which the robots.txt of the site disallows. It returns rows for the model
// search of the host; the host checks every row before it shows one.
package search

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Row is one result of the model-search slot (see the contract reference). The
// fields have the names the host reads.
type Row struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	URI          string   `json:"uri"`
	URL          string   `json:"url,omitempty"`
	Downloads    int64    `json:"downloads,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// ErrLayout is the error of a page that does not have the structure the parser
// expects. Its text names the page, so a change of the site markup is visible.
var ErrLayout = errors.New("unexpected page layout")

// maxTags is the most tags of a row that the host accepts.
const maxTags = 32

// maxTagLen is the longest tag that the host accepts, in bytes.
const maxTagLen = 64

// A model name of the library. A name with a namespace is not part of it.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// capabilityBadges are the badges that name a capability. Any other badge of a
// row is a size, such as 8b or 270m.
var capabilityBadges = []string{"tools", "vision", "thinking", "embedding", "cloud"}

// ParseSearch reads a search page. A limit of zero or less means no limit. A
// page that lists no results says so in its result count marker and gives an
// empty list; a page with no marker, or that counts results but shows none,
// is an error. Models that run only in the cloud are left out.
func ParseSearch(body string, limit int) ([]Row, error) {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("the Ollama search page could not be read: %w", ErrLayout)
	}
	marker := find(root, func(n *html.Node) bool { return n.Type == html.ElementNode && hasAttr(n, "data-result-count") })
	if marker == nil {
		return nil, fmt.Errorf("the Ollama search page has no result count (the site markup may have changed): %w", ErrLayout)
	}
	count, err := strconv.Atoi(attr(marker, "data-result-count"))
	if err != nil || count < 0 {
		return nil, fmt.Errorf("the Ollama search page has an invalid result count: %w", ErrLayout)
	}
	items := findAll(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "li" && find(n, isLibraryLink) != nil
	})
	if count == 0 {
		return []Row{}, nil
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("the Ollama search page counts %d results but lists none (the site markup may have changed): %w", count, ErrLayout)
	}
	rows := []Row{}
	for _, li := range items {
		row, cloudOnly, err := parseItem(li)
		if err != nil {
			return nil, err
		}
		if cloudOnly {
			continue
		}
		rows = append(rows, row)
		if limit > 0 && len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

func isLibraryLink(n *html.Node) bool {
	return n.Type == html.ElementNode && n.Data == "a" && strings.HasPrefix(attr(n, "href"), "/library/")
}

// parseItem reads one result item. cloudOnly is true for a model that has the
// cloud badge and no size badge.
func parseItem(li *html.Node) (row Row, cloudOnly bool, err error) {
	a := find(li, isLibraryLink)
	name := strings.TrimPrefix(attr(a, "href"), "/library/")
	if !modelName.MatchString(name) {
		return Row{}, false, fmt.Errorf("the Ollama search page lists a model with an unexpected link (the site markup may have changed): %w", ErrLayout)
	}
	row = Row{ID: name, Title: name, URI: "ollama://" + name, URL: "https://ollama.com/library/" + name}
	if h := find(li, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h2" && attr(n, "title") != "" }); h != nil {
		if t := attr(h, "title"); t == name {
			row.Title = t
		}
	}
	hasSize, hasCloud := false, false
	for _, s := range findAll(li, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "span" }) {
		title := attr(s, "title")
		switch {
		case strings.HasPrefix(title, "Updated "):
			if t, perr := time.Parse("January 2, 2006", strings.TrimPrefix(title, "Updated ")); perr == nil {
				row.LastModified = t.UTC().Format(time.RFC3339)
			}
		case strings.HasSuffix(title, " downloads"):
			if n, perr := strconv.ParseInt(strings.ReplaceAll(strings.TrimSuffix(title, " downloads"), ",", ""), 10, 64); perr == nil && n >= 0 {
				row.Downloads = n
			}
		case strings.Contains(" "+attr(s, "class")+" ", " rounded-md "):
			tag := strings.ToLower(strings.TrimSpace(text(s)))
			if tag == "" || len(tag) > maxTagLen || len(row.Tags) >= maxTags || slices.Contains(row.Tags, tag) {
				continue
			}
			row.Tags = append(row.Tags, tag)
			if tag == "cloud" {
				hasCloud = true
			} else if !slices.Contains(capabilityBadges, tag) {
				hasSize = true
			}
		}
	}
	return row, hasCloud && !hasSize, nil
}

// find returns the first node under n (n included) that matches, in document order.
func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, match); f != nil {
			return f
		}
	}
	return nil
}

func findAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text returns the text under n.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
