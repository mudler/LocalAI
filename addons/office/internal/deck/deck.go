// Package deck defines the constrained slide-deck JSON contract shared by
// the slides generation path and (later) shareable canvases. The shape and
// the validation rules are the same as those of the deck validator in the
// web client, so that a deck accepted by one language is accepted by the
// other. Keep the two in lockstep.
package deck

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Deck is the top-level presentation object.
type Deck struct {
	Version    int     `json:"version"`
	Theme      string  `json:"theme"`
	Accent     string  `json:"accent,omitempty"`
	Font       string  `json:"font,omitempty"`
	Background string  `json:"background,omitempty"`
	Title      string  `json:"title,omitempty"`
	Slides     []Slide `json:"slides"`
}

// Slide is a single slide; Blocks are kept loosely typed to mirror the TS
// discriminated union without an explosion of Go types.
type Slide struct {
	Layout string           `json:"layout"`
	Notes  string           `json:"notes,omitempty"`
	Steps  bool             `json:"steps,omitempty"`
	Blocks []map[string]any `json:"blocks"`
}

var (
	themes  = map[string]bool{"midnight": true, "paper": true, "mono": true}
	bgs     = map[string]bool{"solid": true, "gradient": true, "grid": true}
	layouts = map[string]bool{
		"title": true, "section": true, "single": true, "two-col": true,
		"media-left": true, "media-right": true, "media-full": true,
		"quote": true, "stat": true, "code": true, "agenda": true, "end": true,
	}
	// required non-empty string fields per block type.
	blockStrings = map[string][]string{
		"kicker": {"text"}, "heading": {"text"}, "subtitle": {"text"},
		"paragraph": {"text"}, "quote": {"text"}, "stat": {"value"},
		"code": {"code"}, "image": {"ref"}, "video": {"ref"},
		"embed": {"url"}, "svg": {"svg"},
	}
	// required array-of-strings fields per block type (empty arrays allowed,
	// mirroring the TS validator which only rejects non-string elements).
	blockArrays = map[string][]string{"bullets": {"items"}}
	hexRe       = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	// Media refs (image/video ref, video poster) must be INTERNAL so a compiled
	// artifact fetches nothing external; embed urls must be a real http(s) site.
	// Mirrors INTERNAL_MEDIA_REF / HTTP_URL in deck.ts.
	internalMediaRe = regexp.MustCompile(`^(?:/_/image/|/api/documents/)`)
	httpURLRe       = regexp.MustCompile(`(?i)^https?://`)
)

const (
	mediaRefMsg = "image ref must be an internal image reference"
	embedURLMsg = "embed url must be an http(s) URL"
)

// Validate returns human-readable errors; an empty slice means the deck is
// valid. It is deliberately permissive about unknown OPTIONAL fields but
// strict about enums, required fields, and value shapes. The rules mirror
// validateDeck() in deck.ts.
func Validate(raw []byte) []string {
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		return []string{fmt.Sprintf("invalid JSON: %v", err)}
	}
	var errs []string
	if v, _ := d["version"].(float64); int(v) != 1 {
		errs = append(errs, "version must be 1")
	}
	if th, _ := d["theme"].(string); !themes[th] {
		errs = append(errs, "theme must be one of midnight, paper, mono")
	}
	if acc, present := d["accent"]; present {
		s, ok := acc.(string)
		if !ok || !hexRe.MatchString(s) {
			errs = append(errs, "accent must be a hex color")
		}
	}
	if bg, present := d["background"]; present {
		s, ok := bg.(string)
		if !ok || !bgs[s] {
			errs = append(errs, "background must be one of solid, gradient, grid")
		}
	}
	slides, ok := d["slides"].([]any)
	if !ok || len(slides) == 0 {
		return append(errs, "slides must be a non-empty array")
	}
	for i, s := range slides {
		sm, ok := s.(map[string]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("slide %d: must be an object", i))
			continue
		}
		if lay, _ := sm["layout"].(string); !layouts[lay] {
			errs = append(errs, fmt.Sprintf("slide %d: layout must be one of title, section, single, two-col, media-left, media-right, media-full, quote, stat, code, agenda, end", i))
		}
		blocks, ok := sm["blocks"].([]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("slide %d: blocks must be an array", i))
			continue
		}
		for j, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				errs = append(errs, fmt.Sprintf("slide %d block %d: must be an object", i, j))
				continue
			}
			bt, _ := bm["type"].(string)
			_, known := blockStrings[bt]
			_, knownArr := blockArrays[bt]
			if !known && !knownArr {
				errs = append(errs, fmt.Sprintf("slide %d block %d: unknown block type %q", i, j, bt))
				continue
			}
			for _, k := range blockStrings[bt] {
				if v, _ := bm[k].(string); v == "" {
					errs = append(errs, fmt.Sprintf("slide %d block %d (%s): %q is required", i, j, bt, k))
				}
			}
			for _, k := range blockArrays[bt] {
				arr, ok := bm[k].([]any)
				if !ok {
					errs = append(errs, fmt.Sprintf("slide %d block %d (%s): %q must be an array of strings", i, j, bt, k))
					continue
				}
				for _, x := range arr {
					if _, isStr := x.(string); !isStr {
						errs = append(errs, fmt.Sprintf("slide %d block %d (%s): %q must be an array of strings", i, j, bt, k))
						break
					}
				}
			}
			// Scheme allowlist for media/embed URLs (mirrors deck.ts). Only
			// non-empty strings are checked; empty/missing required fields keep
			// their "required" error above.
			if bt == "image" || bt == "video" {
				if ref, _ := bm["ref"].(string); ref != "" && !internalMediaRe.MatchString(ref) {
					errs = append(errs, fmt.Sprintf("slide %d block %d (%s): %s", i, j, bt, mediaRefMsg))
				}
			}
			if bt == "video" {
				if poster, _ := bm["poster"].(string); poster != "" && !internalMediaRe.MatchString(poster) {
					errs = append(errs, fmt.Sprintf("slide %d block %d (video): %s", i, j, mediaRefMsg))
				}
			}
			if bt == "embed" {
				if url, _ := bm["url"].(string); url != "" && !httpURLRe.MatchString(url) {
					errs = append(errs, fmt.Sprintf("slide %d block %d (embed): %s", i, j, embedURLMsg))
				}
			}
		}
	}
	return errs
}
