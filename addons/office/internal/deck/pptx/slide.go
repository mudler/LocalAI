package pptx

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for DecodeConfig
	_ "image/png"  // register the PNG decoder for DecodeConfig
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

// palette is the content colours of one deck style (the studio's
// .sl[data-deck=…] rules): background, ink, accent, muted text, wash.
type palette struct {
	style  deck.StudioStyle
	bg     string
	ink    string
	accent string
	muted  string
	wash   string
	font   string
}

var palettes = map[deck.StudioStyle]palette{
	deck.StylePaper:  {deck.StylePaper, "FFFFFF", "141C2B", "FF6B3D", "5B687D", "FFE9DF", "Bricolage Grotesque"},
	deck.StyleNight:  {deck.StyleNight, "0F1626", "F2F5FA", "FFB26B", "A9B4C8", "1D2A44", "Bricolage Grotesque"},
	deck.StyleStudio: {deck.StyleStudio, "FFF0C4", "2A1D00", "B23610", "6A5420", "FFDC85", "Onest"},
	deck.StylePlain:  {deck.StylePlain, "EEF3FA", "0B1220", "1B4A98", "44505F", "D6E4FA", "Onest"},
}

// paletteFor returns the palette of style, paper when unknown.
func paletteFor(style deck.StudioStyle) palette {
	if p, ok := palettes[style]; ok {
		return p
	}
	return palettes[deck.StylePaper]
}

// Claim marks, as in the studio editor.
const (
	nosrcColor = "E5524B"
	weakColor  = "D19A1E"
	white      = "FFFFFF"
	capFill    = "0A0F19"
)

// maxImageBytes caps one embedded image.
const maxImageBytes = 10 << 20

// cqw is one percent of the slide width, the unit of the studio's slide CSS.
const cqw = slideW / 100

// emu converts container-width percent to EMU.
func emu(v float64) int64 { return int64(math.Round(v * cqw)) }

// fontSize converts container-width percent to hundredths of a point (the
// slide is 960 pt wide).
func fontSize(v float64) int { return int(math.Round(v * 960)) }

// Slide frame, in cqw: padding 6.2 top, 7 sides, 5 bottom.
const (
	padTop    = 6.2
	padSide   = 7.0
	padBottom = 5.0
	frameW    = 100 - 2*padSide
	frameH    = 56.25
	frameBot  = frameH - padBottom
	colGap    = 3.5
	leftColW  = (frameW - colGap) * 1.05 / 2.05
	rightColX = padSide + leftColW + colGap
	rightColW = frameW - leftColW - colGap
)

// oneLine folds line breaks into spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// slideBuilder writes the shape tree of one slide.
type slideBuilder struct {
	pal    palette
	load   ImageLoader
	media  *[]media
	id     int
	sp     strings.Builder
	rels   []rel
	titled bool // the slide has a title placeholder and a body placeholder
}

// Placeholder shape types of the Title and Content layout. Only the title and
// the one body shape of a titled slide carry them.
const (
	phTitle = `<p:ph type="title"/>`
	phBody  = `<p:ph idx="1"/>`
)

func newSlideBuilder(pal palette, load ImageLoader, m *[]media) *slideBuilder {
	return &slideBuilder{pal: pal, load: load, media: m, id: 1}
}

func (b *slideBuilder) nextID() int {
	b.id++
	return b.id
}

func (b *slideBuilder) finish() builtSlide {
	x := xmlHeader + `<p:sld ` + nsDecl + `><p:cSld><p:spTree>` + groupHeader + b.sp.String() +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
	return builtSlide{xml: x, rels: b.rels, titled: b.titled}
}

func xfrm(x, y, w, h float64, extra string) string {
	return fmt.Sprintf(`<a:xfrm%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`,
		extra, emu(x), emu(y), max(emu(w), 0), max(emu(h), 0))
}

func solid(color string) string {
	return `<a:solidFill><a:srgbClr val="` + color + `"/></a:solidFill>`
}

// shape adds a filled preset shape without text.
func (b *slideBuilder) shape(name, prst string, x, y, w, h float64, fill string) {
	id := b.nextID()
	fmt.Fprintf(&b.sp, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>`+
		`<p:spPr>%s<a:prstGeom prst="%s"><a:avLst/></a:prstGeom>%s<a:ln><a:noFill/></a:ln></p:spPr></p:sp>`,
		id, name, id, xfrm(x, y, w, h, ""), prst, fill)
}

// run is one piece of text with its look.
type run struct {
	text  string
	size  float64 // cqw
	color string
	bold  bool
	under string // "", "wavy" or "dotted"
	uclr  string
}

func (r run) xml(font string) string {
	attrs := fmt.Sprintf(` lang="en-US" sz="%d"`, fontSize(r.size))
	if r.bold {
		attrs += ` b="1"`
	}
	if r.under != "" {
		attrs += ` u="` + r.under + `"`
	}
	inner := solid(r.color)
	if r.under != "" && r.uclr != "" {
		inner += `<a:uFill>` + solid(r.uclr) + `</a:uFill>`
	}
	inner += `<a:latin typeface="` + ooxml.Escape(font) + `"/>`
	return `<a:r><a:rPr` + attrs + ` dirty="0">` + inner + `</a:rPr><a:t>` + ooxml.Escape(r.text) + `</a:t></a:r>`
}

// para is one paragraph.
type para struct {
	runs   []run
	bullet bool
	algn   string
	before float64 // space before, cqw
}

// textBox adds an editable text box.
type textBoxOpts struct {
	name   string
	anchor string // t, ctr, b
	fill   string // spPr fill, empty for none
	inset  float64
	geom   string
	line   string
	ph     string // placeholder element (phTitle or phBody), "" for a text box
}

func (b *slideBuilder) textBox(x, y, w, h float64, o textBoxOpts, paras []para) {
	id := b.nextID()
	anchor := o.anchor
	if anchor == "" {
		anchor = "t"
	}
	geom := o.geom
	if geom == "" {
		geom = "rect"
	}
	fill := o.fill
	if fill == "" {
		fill = `<a:noFill/>`
	}
	line := o.line
	if line == "" {
		line = `<a:ln><a:noFill/></a:ln>`
	}
	ins := emu(o.inset)
	nv := `<p:cNvSpPr txBox="1"/><p:nvPr/>`
	if o.ph != "" {
		nv = `<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>` + o.ph + `</p:nvPr>`
	}
	fmt.Fprintf(&b.sp, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/>%s</p:nvSpPr>`+
		`<p:spPr>%s<a:prstGeom prst="%s"><a:avLst/></a:prstGeom>%s%s</p:spPr>`+
		`<p:txBody><a:bodyPr wrap="square" lIns="%d" tIns="%d" rIns="%d" bIns="%d" anchor="%s" rtlCol="0"><a:normAutofit/></a:bodyPr><a:lstStyle/>`,
		id, o.name, id, nv, xfrm(x, y, w, h, ""), geom, fill, line, ins, ins, ins, ins, anchor)
	for _, p := range paras {
		b.sp.WriteString(b.paraXML(p))
	}
	b.sp.WriteString(`</p:txBody></p:sp>`)
}

func (b *slideBuilder) paraXML(p para) string {
	var s strings.Builder
	s.WriteString(`<a:p><a:pPr`)
	size := 2.5
	if len(p.runs) > 0 {
		size = p.runs[0].size
	}
	if p.bullet {
		fmt.Fprintf(&s, ` marL="%d" indent="%d"`, emu(3), -emu(3))
	} else {
		s.WriteString(` marL="0" indent="0"`)
	}
	if p.algn != "" {
		s.WriteString(` algn="` + p.algn + `"`)
	}
	s.WriteString(`>`)
	if p.before > 0 {
		fmt.Fprintf(&s, `<a:spcBef><a:spcPts val="%d"/></a:spcBef>`, fontSize(p.before))
	}
	if p.bullet {
		s.WriteString(`<a:buClr><a:srgbClr val="` + b.pal.accent + `"/></a:buClr><a:buSzPct val="100000"/><a:buFont typeface="Arial"/><a:buChar char="•"/>`)
	} else {
		s.WriteString(`<a:buNone/>`)
	}
	s.WriteString(`</a:pPr>`)
	for _, r := range p.runs {
		if r.text == "" {
			continue
		}
		s.WriteString(r.xml(b.pal.font))
	}
	fmt.Fprintf(&s, `<a:endParaRPr lang="en-US" sz="%d" dirty="0"/></a:p>`, fontSize(size))
	return s.String()
}

// decor draws the style's fixed decoration: the left edge of studio and the
// wash circle of night.
func (b *slideBuilder) decor() {
	switch b.pal.style {
	case deck.StyleStudio:
		b.shape("Edge", "rect", 0, 0, 2.2, frameH, solid(b.pal.accent))
	case deck.StyleNight:
		b.shape("Circle", "ellipse", 100+8-34, frameH+14-34, 34, 34,
			`<a:solidFill><a:srgbClr val="`+b.pal.wash+`"><a:alpha val="60000"/></a:srgbClr></a:solidFill>`)
	}
}

// titleLines estimates how many lines a title takes at size s in width w.
func titleLines(text string, s, w float64) int {
	perLine := max(int(w/(s*0.52)), 1)
	return min(max((utf8.RuneCountInString(text)+perLine-1)/perLine, 1), 3)
}

// title draws the slide title and, for paper, the accent bar under it. It
// returns the y (cqw) where the content below may start. A non-empty title
// makes the slide titled: the title shape becomes the title placeholder.
func (b *slideBuilder) title(text string, y, size float64) float64 {
	text = oneLine(text)
	b.titled = text != ""
	h := float64(titleLines(text, size, frameW)) * size * 1.12
	b.textBox(padSide, y, frameW, h, textBoxOpts{name: "Title", anchor: "b", ph: b.titlePh()},
		[]para{{runs: []run{{text: text, size: size, color: b.pal.ink, bold: true}}}})
	y += h
	if b.pal.style == deck.StylePaper {
		b.shape("Accent bar", "roundRect", padSide, y+1.6, 7, 0.9, solid(b.pal.accent))
		y += 1.6 + 0.9
	}
	return y + 2.2
}

// titlePh is the placeholder of the title shape: none on an untitled slide.
func (b *slideBuilder) titlePh() string {
	if b.titled {
		return phTitle
	}
	return ""
}

// bodyPh is the placeholder of the body shape of a slide: none on an
// untitled slide.
func (b *slideBuilder) bodyPh() string {
	if b.titled {
		return phBody
	}
	return ""
}

func (b *slideBuilder) bulletParas(bullets []deck.StudioBullet, size float64) []para {
	paras := make([]para, 0, len(bullets))
	for i, bl := range bullets {
		r := run{text: oneLine(bl.Text), size: size, color: b.pal.ink}
		switch bl.Flag {
		case deck.FlagNoSource:
			r.under, r.uclr = "wavy", nosrcColor
		case deck.FlagWeak:
			r.under, r.uclr = "dotted", weakColor
		}
		p := para{runs: []run{r}, bullet: true}
		if i > 0 {
			p.before = 1.7
		}
		paras = append(paras, p)
	}
	return paras
}

func (b *slideBuilder) bullets(bullets []deck.StudioBullet, x, y, w, h float64) {
	if len(bullets) == 0 || h <= 0 {
		return
	}
	b.textBox(x, y, w, h, textBoxOpts{name: "Bullets", ph: b.bodyPh()}, b.bulletParas(bullets, 2.55))
}

// studioSlide draws one finished studio slide.
func (b *slideBuilder) studioSlide(s deck.StudioSlide) {
	b.decor()
	switch {
	case s.Layout == deck.LayoutTitle:
		t := oneLine(s.Title)
		lines := titleLines(t, 6.6, frameW)
		th := float64(lines) * 6.6 * 1.12
		total := th
		if s.Sub != "" {
			total += 2.6 + 2.9*1.3
		}
		if b.pal.style == deck.StylePaper {
			total += 2.5
		}
		y := max((frameH-total)/2, padTop)
		y = b.title(t, y, 6.6) - 2.2 + 2.6
		if s.Sub != "" {
			b.textBox(padSide, y, frameW, 2.9*1.3*2, textBoxOpts{name: "Subtitle", ph: b.bodyPh()},
				[]para{{runs: []run{{text: oneLine(s.Sub), size: 2.9, color: b.pal.muted}}}})
		}
	case s.Layout == deck.LayoutNumber && s.Big != nil && s.Big.Num != "":
		y := b.title(s.Title, padTop, 4.5) - 2.2 + 1.6
		b.textBox(padSide, y, frameW, 13*1.0, textBoxOpts{name: "Number"},
			[]para{{runs: []run{{text: oneLine(s.Big.Num), size: 13, color: b.pal.accent, bold: true}}}})
		y += 13 + 1.6
		if s.Big.Label != "" {
			b.textBox(padSide, y, frameW, 2.8*1.3, textBoxOpts{name: "Number label"},
				[]para{{runs: []run{{text: oneLine(s.Big.Label), size: 2.8, color: b.pal.muted}}}})
			y += 2.8*1.3 + 1.6
		}
		b.bullets(s.Bullets, padSide, y, frameW, frameBot-y)
	case s.Layout == deck.LayoutChart && s.Chart != nil:
		y := b.title(s.Title, padTop, 4.5)
		b.bullets(s.Bullets, padSide, y, leftColW, frameBot-y)
		x, w := rightColX, rightColW
		if len(s.Bullets) == 0 {
			x, w = padSide, frameW
		}
		b.chart(*s.Chart, x, y, w, frameBot-y)
	case s.Layout == deck.LayoutImage && s.Image != nil:
		y := b.title(s.Title, padTop, 4.5)
		b.bullets(s.Bullets, padSide, y, leftColW, frameBot-y)
		x, w := rightColX, rightColW
		if len(s.Bullets) == 0 {
			x, w = padSide, frameW
		}
		b.picture(*s.Image, x, y, w, frameBot-y)
	default:
		y := b.title(s.Title, padTop, 4.5)
		b.bullets(s.Bullets, padSide, y, frameW, frameBot-y)
	}
}

// formatValue prints a chart value with its unit.
func formatValue(v float64, unit string) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + unit
}

// niceMax rounds m up to 1, 2, 5 or 10 times a power of ten.
func niceMax(m float64) float64 {
	if !(m > 0) || math.IsInf(m, 0) {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(m)))
	switch n := m / p; {
	case n <= 1:
		return p
	case n <= 2:
		return 2 * p
	case n <= 5:
		return 5 * p
	}
	return 10 * p
}

// chart draws a bar chart as rectangles or a line chart as connectors, with
// value and category labels, inside the box x, y, w, h (cqw).
func (b *slideBuilder) chart(c deck.StudioChart, x, y, w, h float64) {
	if len(c.Data) == 0 || h <= 0 {
		return
	}
	const labelH = 2.4
	const labelSize = 1.6
	if c.Label != "" {
		b.textBox(x, y, w, labelH, textBoxOpts{name: "Chart label"},
			[]para{{runs: []run{{text: oneLine(c.Label), size: 1.8, color: b.pal.muted, bold: true}}}})
		y += labelH + 0.6
		h -= labelH + 0.6
	}
	// Plot area: room for value labels on top and category labels below.
	plotY, plotH := y+labelH, h-2*labelH
	if plotH <= 0 {
		return
	}
	hiV, loV := 0.0, 0.0
	vals := make([]float64, len(c.Data))
	for i, p := range c.Data {
		v := p.V
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		vals[i] = v
		hiV, loV = max(hiV, v), min(loV, v)
	}
	hi, lo := 0.0, 0.0
	if hiV > 0 {
		hi = niceMax(hiV)
	}
	if loV < 0 {
		lo = -niceMax(-loV)
	}
	if hi == lo {
		hi = 1
	}
	vy := func(v float64) float64 { return plotY + plotH - (v-lo)/(hi-lo)*plotH }
	zero := vy(0)
	// Baseline.
	b.shape("Baseline", "rect", x, zero-0.05, w, 0.1,
		`<a:solidFill><a:srgbClr val="`+b.pal.ink+`"><a:alpha val="30000"/></a:srgbClr></a:solidFill>`)

	n := float64(len(c.Data))
	slot := w / n
	label := func(name, text string, cx, top float64, color string) {
		b.textBox(cx-slot/2, top, slot, labelH, textBoxOpts{name: name, anchor: "ctr"},
			[]para{{runs: []run{{text: text, size: labelSize, color: color}}, algn: "ctr"}})
	}
	if c.Kind == deck.ChartLine {
		pts := make([][2]float64, len(vals))
		for i, v := range vals {
			pts[i] = [2]float64{x + slot*(float64(i)+0.5), vy(v)}
		}
		for i := 1; i < len(pts); i++ {
			b.connector(pts[i-1], pts[i])
		}
		for i, p := range pts {
			b.shape("Point", "ellipse", p[0]-0.6, p[1]-0.6, 1.2, 1.2, solid(b.pal.accent))
			label("Value", formatValue(vals[i], c.Unit), p[0], p[1]-labelH-0.6, b.pal.ink)
			label("Category", oneLine(c.Data[i].K), p[0], plotY+plotH+0.3, b.pal.muted)
		}
		return
	}
	barW := slot * 0.62
	for i, v := range vals {
		cx := x + slot*(float64(i)+0.5)
		top, bottom := min(vy(v), zero), max(vy(v), zero)
		b.shape("Bar", "rect", cx-barW/2, top, barW, max(bottom-top, 0.1), solid(b.pal.accent))
		vt := top - labelH
		if v < 0 {
			vt = bottom
		}
		label("Value", formatValue(v, c.Unit), cx, vt, b.pal.ink)
		label("Category", oneLine(c.Data[i].K), cx, plotY+plotH+0.3, b.pal.muted)
	}
}

// connector draws a straight line from p to q.
func (b *slideBuilder) connector(p, q [2]float64) {
	id := b.nextID()
	x, y := min(p[0], q[0]), min(p[1], q[1])
	w, h := math.Abs(q[0]-p[0]), math.Abs(q[1]-p[1])
	flip := ""
	// A line that rises left to right (or falls right to left) runs from
	// bottom-left to top-right of its box.
	if (q[0] >= p[0]) != (q[1] >= p[1]) {
		flip = ` flipV="1"`
	}
	fmt.Fprintf(&b.sp, `<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="%d" name="Line %d"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr>`+
		`<p:spPr>%s<a:prstGeom prst="line"><a:avLst/></a:prstGeom>`+
		`<a:ln w="%d" cap="rnd">%s<a:round/></a:ln></p:spPr></p:cxnSp>`,
		id, id, xfrm(x, y, w, h, flip), emu(0.45), solid(b.pal.accent))
}

// loadImage loads and checks an image; it returns the extension and size.
func (b *slideBuilder) loadImage(ref string) ([]byte, string, image.Config, bool) {
	if b.load == nil {
		return nil, "", image.Config{}, false
	}
	data, _, err := b.load(ref)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return nil, "", image.Config{}, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", image.Config{}, false
	}
	switch format {
	case "png":
		return data, "png", cfg, true
	case "jpeg":
		return data, "jpeg", cfg, true
	}
	return nil, "", image.Config{}, false
}

// picture embeds the image, cropped to fill the box, with its caption. An
// image that cannot be loaded leaves a dashed placeholder with the caption.
func (b *slideBuilder) picture(img deck.StudioImage, x, y, w, h float64) {
	if h <= 0 {
		return
	}
	caption := strings.TrimSpace(oneLine(img.Label))
	if img.Weak {
		caption = strings.TrimSpace(caption + " (low confidence)")
	}
	data, ext, cfg, ok := b.loadImage(img.Ref)
	if !ok {
		text := "Image not available"
		if caption != "" {
			text = caption + " (image not available)"
		}
		b.textBox(x, y, w, h, textBoxOpts{
			name: "Image placeholder", anchor: "ctr", inset: 2, geom: "roundRect",
			fill: `<a:noFill/>`,
			line: `<a:ln w="` + strconv.FormatInt(emu(0.35), 10) + `"><a:solidFill><a:srgbClr val="` + b.pal.ink +
				`"><a:alpha val="35000"/></a:srgbClr></a:solidFill><a:prstDash val="dash"/></a:ln>`,
		}, []para{{runs: []run{{text: text, size: 2.1, color: b.pal.muted}}, algn: "ctr"}})
		return
	}
	name := fmt.Sprintf("image%d.%s", len(*b.media)+1, ext)
	*b.media = append(*b.media, media{name: name, ext: ext, data: data})
	rid := fmt.Sprintf("rId%d", len(b.rels)+2) // rId1 is the layout
	b.rels = append(b.rels, rel{rid, relImage, "../media/" + name})

	// Cover: crop the side that overflows the box.
	box := w / h
	ar := float64(cfg.Width) / float64(cfg.Height)
	var l, t int
	if ar > box {
		l = int(math.Round((1 - box/ar) / 2 * 100000))
	} else {
		t = int(math.Round((1 - ar/box) / 2 * 100000))
	}
	id := b.nextID()
	fmt.Fprintf(&b.sp, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="Picture %d" descr="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`+
		`<p:blipFill><a:blip r:embed="%s"/><a:srcRect l="%d" t="%d" r="%d" b="%d"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>`+
		`<p:spPr>%s<a:prstGeom prst="roundRect"><a:avLst/></a:prstGeom>%s</p:spPr></p:pic>`,
		id, id, ooxml.Escape(caption), rid, l, t, l, t, xfrm(x, y, w, h, ""), solid(b.pal.wash))
	if caption != "" {
		cw := min(float64(utf8.RuneCountInString(caption))*1.5*0.55+2, w-2.4)
		b.textBox(x+1.2, y+h-1.2-3.1, cw, 3.1, textBoxOpts{
			name: "Caption", anchor: "ctr", inset: 0.7, geom: "roundRect",
			fill: `<a:solidFill><a:srgbClr val="` + capFill + `"><a:alpha val="72000"/></a:srgbClr></a:solidFill>`,
		}, []para{{runs: []run{{text: caption, size: 1.5, color: white}}}})
	}
}

// sourceSuffix marks a line of the sources slide.
var sourceSuffix = map[SourceKind]string{
	SourceWeak:     " (low confidence)",
	SourceNoSource: " (no source)",
	SourceOwn:      " (your own claim)",
}

// sourcesSlide lists every source line, weak and unsourced claims included.
func (b *slideBuilder) sourcesSlide(lines []SourceLine) {
	b.decor()
	y := b.title("Sources", padTop, 4.5)
	if len(lines) == 0 {
		b.textBox(padSide, y, frameW, 3, textBoxOpts{name: "Sources", ph: b.bodyPh()},
			[]para{{runs: []run{{text: "No sources were cited.", size: 2, color: b.pal.muted}}}})
		return
	}
	size := 2.0
	if len(lines) > 10 {
		size = max(2.0*10/float64(len(lines)), 0.9)
	}
	paras := make([]para, 0, len(lines))
	for i, l := range lines {
		runs := []run{{text: oneLine(l.Label), size: size, color: b.pal.ink, bold: true}}
		if d := oneLine(l.Detail); d != "" {
			runs = append(runs, run{text: ": " + d, size: size, color: b.pal.muted})
		}
		if sfx, ok := sourceSuffix[l.Kind]; ok {
			c := b.pal.muted
			switch l.Kind {
			case SourceWeak:
				c = weakColor
			case SourceNoSource:
				c = nosrcColor
			}
			runs = append(runs, run{text: sfx, size: size, color: c, bold: true})
		}
		p := para{runs: runs, bullet: true}
		if i > 0 {
			p.before = size * 0.5
		}
		paras = append(paras, p)
	}
	b.textBox(padSide, y, frameW, frameBot-y, textBoxOpts{name: "Sources", ph: b.bodyPh()}, paras)
}
