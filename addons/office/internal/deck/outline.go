package deck

import "regexp"

// slideLine matches a `Slide N:` outline line. Leading markdown noise is
// tolerated: models routinely emit "## Slide 1:" or "**Slide 1:**". The web
// client uses the same pattern to find the slides of an outline.
var slideLine = regexp.MustCompile(`(?im)^[\s*#>-]*slide\s+\d+\s*:`)

// CountOutlineSlides counts `Slide N:` lines. 0 means the text isn't shaped
// like a slide outline (e.g. a one-line "the deck is ready" sign-off).
func CountOutlineSlides(text string) int {
	return len(slideLine.FindAllStringIndex(text, -1))
}
