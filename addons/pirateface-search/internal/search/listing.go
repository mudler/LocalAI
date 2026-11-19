// SPDX-License-Identifier: MIT

// Package search is the runtime of the Pirate Face search add-on. It reads the
// plain text search page of Pirate Face (GET /s/<query>) and never calls the
// /api paths, which the site's robots.txt disallows. It returns rows for the
// model search of the host; the host checks every row before it shows one.
package search

import (
	"regexp"
	"strconv"
	"strings"
)

// Row is one result of the model-search slot (see the contract reference). The
// fields have the names the host reads.
type Row struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	URI       string   `json:"uri"`
	Magnet    string   `json:"magnet,omitempty"`
	URL       string   `json:"url,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	Seeders   int64    `json:"seeders,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// A result line: <owner/repo> <size> <unit> <n> seeds <magnet link>
var line = regexp.MustCompile(`^([\w.-]+/[\w.-]+)\s+([0-9]+(?:\.[0-9]+)?)\s*([KMGT]i?B)\s+([0-9]+)\s+seeds?\s+(magnet:\?xt=urn:btih:\S+)$`)

var units = map[string]float64{
	"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
	"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40,
}

// ParseListing reads the text listing and skips the lines it does not
// understand. A limit of zero or less means no limit. minSeeders drops the rows
// with fewer seeders before the limit is applied.
func ParseListing(body string, limit int, minSeeders int64) []Row {
	out := []Row{}
	for l := range strings.SplitSeq(body, "\n") {
		m := line.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		size, _ := strconv.ParseFloat(m[2], 64)
		seeders, _ := strconv.ParseInt(m[4], 10, 64)
		if seeders < minSeeders {
			continue
		}
		out = append(out, Row{
			ID:        m[1],
			Title:     m[1],
			URI:       "pirateface://" + m[1],
			Magnet:    m[5],
			URL:       "https://huggingface.co/" + m[1],
			SizeBytes: int64(size * units[strings.ToUpper(m[3])]),
			Seeders:   seeders,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
