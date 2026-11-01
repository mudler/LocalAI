package deck

import (
	"math"
	"strconv"
	"strings"
)

// Chart geometry: a 300x170 view box with padding left 30, right 10, top 18
// and bottom 26. The TypeScript twin is src/studio/chart.ts; keep the two in
// lockstep.
const (
	chartW  = 300.0
	chartH  = 170.0
	chartPL = 30.0
	chartPR = 10.0
	chartPT = 18.0
	chartPB = 26.0
)

// niceMax rounds m up to 1, 2, 5 or 10 times a power of ten. It returns 1 for
// values that are not positive and finite.
func niceMax(m float64) float64 {
	if !(m > 0) || math.IsInf(m, 0) {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(m)))
	n := m / p
	switch {
	case n <= 1:
		return p
	case n <= 2:
		return 2 * p
	case n <= 5:
		return 5 * p
	}
	return 10 * p
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

func esc(s string) string { return xmlEscaper.Replace(s) }

// num prints a coordinate rounded to two decimals.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

func clampY(v float64) float64 { return min(max(v, 0), chartH) }

// ChartSVG draws a bar or line chart as an inline SVG string. It uses
// currentColor for ink and var(--sa) for the accent, so the slide style
// decides the colours. Negative values hang from the zero baseline. The
// output only uses elements and attributes that the presenter's SVG
// sanitizer keeps.
func ChartSVG(c StudioChart) string {
	iw := chartW - chartPL - chartPR
	ih := chartH - chartPT - chartPB

	vals := make([]float64, len(c.Data))
	hiV, loV := 0.0, 0.0
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
	y := func(v float64) float64 { return chartPT + ih - (v-lo)/(hi-lo)*ih }
	base := y(0)

	var s strings.Builder
	s.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 170">`)
	if c.Label != "" {
		s.WriteString("<title>" + esc(c.Label) + "</title>")
	}

	grid := []float64{0, hi / 2, hi}
	switch {
	case hi == 0:
		grid = []float64{0, lo / 2, lo}
	case lo < 0:
		grid = []float64{0, lo, hi}
	}
	for _, v := range grid {
		op := ".15"
		if v == 0 {
			op = ".4"
		}
		gy := num(y(v))
		s.WriteString(`<line x1="` + num(chartPL) + `" x2="` + num(chartW-chartPR) + `" y1="` + gy + `" y2="` + gy +
			`" stroke="currentColor" stroke-opacity="` + op + `"/>`)
		s.WriteString(`<text x="` + num(chartPL-5) + `" y="` + num(clampY(y(v)+3)) +
			`" font-size="9" text-anchor="end" fill="currentColor" fill-opacity=".65">` +
			strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + `</text>`)
	}
	if len(vals) == 0 {
		return s.String() + "</svg>"
	}

	step := iw / float64(len(vals))
	keyLabel := func(x float64, k string) string {
		return `<text x="` + num(x) + `" y="` + num(chartH-8) +
			`" font-size="9.5" text-anchor="middle" fill="currentColor" fill-opacity=".75">` + esc(k) + `</text>`
	}
	valueText := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

	if c.Kind == ChartLine {
		type pt struct{ x, y float64 }
		pts := make([]pt, len(vals))
		var line strings.Builder
		for i, v := range vals {
			pts[i] = pt{chartPL + step*(float64(i)+.5), y(v)}
			if i > 0 {
				line.WriteString("L")
			}
			line.WriteString(num(pts[i].x) + " " + num(pts[i].y))
		}
		last := pts[len(pts)-1]
		s.WriteString(`<path d="M` + line.String() + `L` + num(last.x) + ` ` + num(base) + `L` + num(pts[0].x) + ` ` + num(base) +
			`Z" fill="var(--sa)" fill-opacity=".14"/>`)
		s.WriteString(`<path d="M` + line.String() +
			`" fill="none" stroke="var(--sa)" stroke-width="2.6" stroke-linejoin="round" stroke-linecap="round"/>`)
		for i, p := range pts {
			r := "3.4"
			if i == len(pts)-1 {
				r = "5"
			}
			ly := p.y - 9
			if vals[i] < 0 {
				ly = p.y + 15
			}
			s.WriteString(`<circle cx="` + num(p.x) + `" cy="` + num(p.y) + `" r="` + r + `" fill="var(--sa)"/>`)
			s.WriteString(`<text x="` + num(p.x) + `" y="` + num(clampY(ly)) +
				`" font-size="10" font-weight="700" text-anchor="middle" fill="currentColor">` + valueText(vals[i]) + `</text>`)
			s.WriteString(keyLabel(p.x, c.Data[i].K))
		}
		return s.String() + "</svg>"
	}

	unit := ""
	if c.Unit != "" && !strings.Contains(c.Unit, " ") && !strings.HasPrefix(c.Unit, "/") {
		unit = esc(c.Unit)
	}
	bw := min(52, step*.6)
	for i, v := range vals {
		x := chartPL + step*float64(i) + (step-bw)/2
		yv := y(v)
		top, height := min(yv, base), math.Abs(yv-base)
		ly := yv - 5
		if v < 0 {
			ly = yv + 12
		}
		s.WriteString(`<rect x="` + num(x) + `" y="` + num(top) + `" width="` + num(bw) + `" height="` + num(height) +
			`" rx="4" fill="var(--sa)" fill-opacity=".92"/>`)
		s.WriteString(`<text x="` + num(x+bw/2) + `" y="` + num(clampY(ly)) +
			`" font-size="10.5" font-weight="700" text-anchor="middle" fill="currentColor">` + valueText(v) + unit + `</text>`)
		s.WriteString(keyLabel(x+bw/2, c.Data[i].K))
	}
	return s.String() + "</svg>"
}
