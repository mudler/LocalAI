package deck

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// The Slides studio keeps a richer slide model than the presenter deck: each
// claim carries its evidence and an honesty flag, and each slide carries its
// build status. StudioToDeck converts a studio deck into the presenter Deck
// so canvases, sharing and publishing keep working unchanged. The types
// match the slide model of the web client.

// StudioStyle is the visual style of a studio deck.
type StudioStyle string

const (
	StylePaper  StudioStyle = "paper"
	StyleNight  StudioStyle = "night"
	StyleStudio StudioStyle = "studio"
	StylePlain  StudioStyle = "plain"
)

// Valid reports whether s is a known style.
func (s StudioStyle) Valid() bool {
	switch s {
	case StylePaper, StyleNight, StyleStudio, StylePlain:
		return true
	}
	return false
}

// StudioLayout is the layout of one studio slide.
type StudioLayout string

const (
	LayoutTitle  StudioLayout = "title"
	LayoutText   StudioLayout = "text"
	LayoutImage  StudioLayout = "image"
	LayoutChart  StudioLayout = "chart"
	LayoutNumber StudioLayout = "number"
)

// Valid reports whether l is a known layout.
func (l StudioLayout) Valid() bool {
	switch l {
	case LayoutTitle, LayoutText, LayoutImage, LayoutChart, LayoutNumber:
		return true
	}
	return false
}

// ClaimFlag marks a claim that needs the user's attention.
type ClaimFlag string

const (
	FlagNone     ClaimFlag = ""
	FlagNoSource ClaimFlag = "nosrc"
	FlagWeak     ClaimFlag = "weak"
	FlagOwn      ClaimFlag = "own"
)

func (f ClaimFlag) valid() bool {
	switch f {
	case FlagNone, FlagNoSource, FlagWeak, FlagOwn:
		return true
	}
	return false
}

// SlideStatus is the build status of one studio slide.
type SlideStatus string

const (
	SlideQueued  SlideStatus = "queued"
	SlideWorking SlideStatus = "working"
	SlideOK      SlideStatus = "ok"
	SlideFailed  SlideStatus = "failed"
)

func (s SlideStatus) valid() bool {
	switch s {
	case SlideQueued, SlideWorking, SlideOK, SlideFailed:
		return true
	}
	return false
}

// StudioBullet is one claim on a slide with the evidence ids that support it.
type StudioBullet struct {
	Text     string    `json:"text"`
	Evidence []string  `json:"evidence"`
	Flag     ClaimFlag `json:"flag,omitempty"`
}

// StudioImage is an image taken from a source document. Ref is an internal
// media reference such as /api/documents/<id>/file.
type StudioImage struct {
	Ref      string `json:"ref"`
	Label    string `json:"label"`
	Evidence string `json:"evidence"`
	Weak     bool   `json:"weak,omitempty"`
}

// ChartKind is the kind of a studio chart.
type ChartKind string

const (
	ChartBar  ChartKind = "bar"
	ChartLine ChartKind = "line"
)

// ChartPoint is one category and its value.
type ChartPoint struct {
	K string  `json:"k"`
	V float64 `json:"v"`
}

// StudioChart is a small chart built from numbers found in the sources.
type StudioChart struct {
	Kind     ChartKind    `json:"kind"`
	Label    string       `json:"label"`
	Unit     string       `json:"unit,omitempty"`
	Data     []ChartPoint `json:"data"`
	Evidence string       `json:"evidence"`
}

// BigNumber is the headline number of a number slide.
type BigNumber struct {
	Num      string `json:"num"`
	Label    string `json:"label"`
	Evidence string `json:"evidence"`
}

// StudioSlide is one slide of a studio deck. Sources are evidence ids;
// SourceHashes maps a document id to the content hash the slide was built from.
type StudioSlide struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Layout       StudioLayout      `json:"layout"`
	Sub          string            `json:"sub,omitempty"`
	Bullets      []StudioBullet    `json:"bullets"`
	Image        *StudioImage      `json:"image,omitempty"`
	Chart        *StudioChart      `json:"chart,omitempty"`
	Big          *BigNumber        `json:"big,omitempty"`
	Notes        string            `json:"notes,omitempty"`
	Sources      []string          `json:"sources"`
	Status       SlideStatus       `json:"status"`
	Error        string            `json:"error,omitempty"`
	SourceHashes map[string]string `json:"sourceHashes,omitempty"`
	// ErrorKey is the reason a slide failed, as a key of the interface
	// texts (it starts with SlideErrorKeyPrefix), so the viewer reads it in
	// their own language. Error keeps the English as a fallback.
	ErrorKey string `json:"errorKey,omitempty"`
}

// SlideErrorKeyPrefix starts every StudioSlide.ErrorKey.
const SlideErrorKeyPrefix = "studio.server.slideError."

// OutlineItem is one planned slide of the outline.
type OutlineItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
	Merged   int      `json:"merged,omitempty"`
}

// DeckLength is the requested size of a deck.
type DeckLength string

const (
	LengthShort    DeckLength = "short"
	LengthStandard DeckLength = "standard"
	LengthLong     DeckLength = "long"
)

// Slides returns the number of slides for the length, or 0 when unknown.
func (l DeckLength) Slides() int {
	switch l {
	case LengthShort:
		return 5
	case LengthStandard:
		return 7
	case LengthLong:
		return 8
	}
	return 0
}

// Tone is the writing tone of a deck.
type Tone string

const (
	TonePlain  Tone = "plain"
	ToneWarm   Tone = "warm"
	ToneFormal Tone = "formal"
	TonePunchy Tone = "punchy"
)

func (t Tone) valid() bool {
	switch t {
	case TonePlain, ToneWarm, ToneFormal, TonePunchy:
		return true
	}
	return false
}

// Brief is what the user asks for. Sources are the selected document ids.
type Brief struct {
	Title    string      `json:"title"`
	Goal     string      `json:"goal"`
	Audience string      `json:"audience"`
	Length   DeckLength  `json:"length"`
	Tone     Tone        `json:"tone"`
	Style    StudioStyle `json:"style"`
	Sources  []string    `json:"sources"`
}

// Text limits, in runes.
const (
	maxBriefTitle  = 200
	maxBriefGoal   = 2000
	maxAudience    = 500
	maxBriefSrcs   = 50
	maxSlideTitle  = 200
	maxSub         = 300
	maxBullets     = 8
	maxBulletText  = 500
	maxNotes       = 5000
	maxLabel       = 200
	maxChartPoints = 12
	maxChartKey    = 60
	maxUnit        = 20
	maxNumber      = 40
	maxError       = 1000
)

func tooLong(s string, n int) bool { return utf8.RuneCountInString(s) > n }

// Validate returns human-readable errors; an empty slice means the brief is valid.
func (b Brief) Validate() []string {
	var errs []string
	switch {
	case strings.TrimSpace(b.Title) == "":
		errs = append(errs, "title is required")
	case tooLong(b.Title, maxBriefTitle):
		errs = append(errs, fmt.Sprintf("title must be at most %d characters", maxBriefTitle))
	}
	if tooLong(b.Goal, maxBriefGoal) {
		errs = append(errs, fmt.Sprintf("goal must be at most %d characters", maxBriefGoal))
	}
	if tooLong(b.Audience, maxAudience) {
		errs = append(errs, fmt.Sprintf("audience must be at most %d characters", maxAudience))
	}
	if b.Length.Slides() == 0 {
		errs = append(errs, "length must be one of short, standard, long")
	}
	if !b.Tone.valid() {
		errs = append(errs, "tone must be one of plain, warm, formal, punchy")
	}
	if !b.Style.Valid() {
		errs = append(errs, "style must be one of paper, night, studio, plain")
	}
	if len(b.Sources) > maxBriefSrcs {
		errs = append(errs, fmt.Sprintf("at most %d sources can be selected", maxBriefSrcs))
	}
	return errs
}

// ValidateStudioSlides returns human-readable errors; an empty slice means
// the slides are valid.
func ValidateStudioSlides(slides []StudioSlide) []string {
	var errs []string
	seen := make(map[string]bool, len(slides))
	for i, s := range slides {
		add := func(format string, a ...any) {
			errs = append(errs, fmt.Sprintf("slide %d: ", i)+fmt.Sprintf(format, a...))
		}
		switch {
		case s.ID == "":
			add("id is required")
		case seen[s.ID]:
			add("id %q is used twice", s.ID)
		}
		seen[s.ID] = true
		if !s.Layout.Valid() {
			add("layout must be one of title, text, image, chart, number")
		}
		if !s.Status.valid() {
			add("status must be one of queued, working, ok, failed")
		}
		if tooLong(s.Title, maxSlideTitle) {
			add("title must be at most %d characters", maxSlideTitle)
		}
		if tooLong(s.Sub, maxSub) {
			add("subtitle must be at most %d characters", maxSub)
		}
		if tooLong(s.Notes, maxNotes) {
			add("notes must be at most %d characters", maxNotes)
		}
		if tooLong(s.Error, maxError) {
			add("error must be at most %d characters", maxError)
		}
		if s.ErrorKey != "" && !strings.HasPrefix(s.ErrorKey, SlideErrorKeyPrefix) {
			add("errorKey must start with %q", SlideErrorKeyPrefix)
		}
		if len(s.Bullets) > maxBullets {
			add("at most %d bullets are allowed", maxBullets)
		}
		for j, b := range s.Bullets {
			if tooLong(b.Text, maxBulletText) {
				add("bullet %d must be at most %d characters", j, maxBulletText)
			}
			if !b.Flag.valid() {
				add("bullet %d: flag must be one of nosrc, weak, own", j)
			}
		}
		if s.Image != nil {
			if !internalMediaRe.MatchString(s.Image.Ref) {
				add("%s", mediaRefMsg)
			}
			if tooLong(s.Image.Label, maxLabel) {
				add("image label must be at most %d characters", maxLabel)
			}
		}
		if s.Chart != nil {
			for _, e := range validateChart(*s.Chart) {
				add("%s", e)
			}
		}
		if s.Big != nil {
			if tooLong(s.Big.Num, maxNumber) {
				add("number must be at most %d characters", maxNumber)
			}
			if tooLong(s.Big.Label, maxLabel) {
				add("number label must be at most %d characters", maxLabel)
			}
		}
	}
	return errs
}

func validateChart(c StudioChart) []string {
	var errs []string
	if c.Kind != ChartBar && c.Kind != ChartLine {
		errs = append(errs, "chart kind must be bar or line")
	}
	if tooLong(c.Label, maxLabel) {
		errs = append(errs, fmt.Sprintf("chart label must be at most %d characters", maxLabel))
	}
	if tooLong(c.Unit, maxUnit) {
		errs = append(errs, fmt.Sprintf("chart unit must be at most %d characters", maxUnit))
	}
	if len(c.Data) == 0 || len(c.Data) > maxChartPoints {
		errs = append(errs, fmt.Sprintf("chart must have 1 to %d points", maxChartPoints))
	}
	for j, p := range c.Data {
		if math.IsNaN(p.V) || math.IsInf(p.V, 0) {
			errs = append(errs, fmt.Sprintf("chart point %d must be a finite number", j))
		}
		if tooLong(p.K, maxChartKey) {
			errs = append(errs, fmt.Sprintf("chart point %d: name must be at most %d characters", j, maxChartKey))
		}
	}
	return errs
}

// styleThemes maps a studio style to the presenter theme and accent.
var styleThemes = map[StudioStyle][2]string{
	StylePaper:  {"paper", "#FF6B3D"},
	StyleNight:  {"midnight", "#FFB26B"},
	StyleStudio: {"paper", "#B23610"},
	StylePlain:  {"mono", "#1B4A98"},
}

// StudioToDeck converts finished studio slides into a presenter Deck. Slides
// whose status is not ok are skipped. Claim flags stay in the studio.
func StudioToDeck(title string, style StudioStyle, slides []StudioSlide) Deck {
	th, ok := styleThemes[style]
	if !ok {
		th = styleThemes[StylePaper]
	}
	d := Deck{Version: 1, Theme: th[0], Accent: th[1], Title: title, Slides: []Slide{}}
	for _, s := range slides {
		if s.Status != SlideOK {
			continue
		}
		d.Slides = append(d.Slides, studioSlideToSlide(s))
	}
	return d
}

func studioSlideToSlide(s StudioSlide) Slide {
	blocks := []map[string]any{}
	if s.Title != "" {
		blocks = append(blocks, map[string]any{"type": "heading", "text": s.Title})
	}
	bullets := func() {
		if len(s.Bullets) == 0 {
			return
		}
		items := make([]string, 0, len(s.Bullets))
		for _, b := range s.Bullets {
			items = append(items, b.Text)
		}
		blocks = append(blocks, map[string]any{"type": "bullets", "items": items})
	}
	withCaption := func(b map[string]any, caption string) map[string]any {
		if caption != "" {
			b["caption"] = caption
		}
		return b
	}
	layout := "single"
	switch {
	case s.Layout == LayoutTitle:
		layout = "title"
		if s.Sub != "" {
			blocks = append(blocks, map[string]any{"type": "subtitle", "text": s.Sub})
		}
	case s.Layout == LayoutImage && s.Image != nil:
		layout = "media-right"
		bullets()
		caption := s.Image.Label
		if s.Image.Weak {
			caption += " (low confidence)"
		}
		blocks = append(blocks, withCaption(map[string]any{"type": "image", "ref": s.Image.Ref}, strings.TrimSpace(caption)))
	case s.Layout == LayoutChart && s.Chart != nil:
		layout = "media-right"
		bullets()
		blocks = append(blocks, withCaption(map[string]any{"type": "svg", "svg": ChartSVG(*s.Chart)}, s.Chart.Label))
	case s.Layout == LayoutNumber && s.Big != nil && s.Big.Num != "":
		layout = "stat"
		stat := map[string]any{"type": "stat", "value": s.Big.Num}
		if s.Big.Label != "" {
			stat["label"] = s.Big.Label
		}
		blocks = append(blocks, stat)
		bullets()
	default:
		bullets()
	}
	return Slide{Layout: layout, Notes: s.Notes, Blocks: blocks}
}

// StudioToDeckJSON converts with StudioToDeck and returns the deck JSON. It
// returns an error when the result does not pass Validate, for example when
// no slide is finished yet.
func StudioToDeckJSON(title string, style StudioStyle, slides []StudioSlide) ([]byte, error) {
	raw, err := json.Marshal(StudioToDeck(title, style, slides))
	if err != nil {
		return nil, err
	}
	if errs := Validate(raw); len(errs) != 0 {
		return nil, errors.New("invalid deck: " + strings.Join(errs, "; "))
	}
	return raw, nil
}
