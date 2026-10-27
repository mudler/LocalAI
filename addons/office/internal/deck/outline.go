package deck

import "regexp"

// slideLine matches a `Slide N:` outline line. Leading markdown noise is
// tolerated: models routinely emit "## Slide 1:" or "**Slide 1:**". Mirrors
// SLIDE_LINE in web/dante/frontend/src/components/chat/SlidesOutlineDisclosure.tsx.
var slideLine = regexp.MustCompile(`(?im)^[\s*#>-]*slide\s+\d+\s*:`)

// CountOutlineSlides counts `Slide N:` lines. 0 means the text isn't shaped
// like a slide outline (e.g. a one-line "the deck is ready" sign-off).
func CountOutlineSlides(text string) int {
	return len(slideLine.FindAllStringIndex(text, -1))
}
