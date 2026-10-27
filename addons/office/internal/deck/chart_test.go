package deck

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestNiceMax(t *testing.T) {
	cases := map[float64]float64{49: 50, 4.4: 5, 1: 1, 10: 10, 11: 20, 0.3: 0.5, 0: 1, -3: 1}
	for in, want := range cases {
		if got := niceMax(in); got != want {
			t.Errorf("niceMax(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestChartSVGBar(t *testing.T) {
	svg := ChartSVG(StudioChart{
		Kind:  ChartBar,
		Label: "Cost <by> office & team",
		Unit:  "%",
		Data:  []ChartPoint{{K: "Rome", V: 12}, {K: "Milan & <Turin>", V: 49}, {K: "Naples", V: 30}},
	})
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not an svg: %s", svg)
	}
	if !strings.Contains(svg, `viewBox="0 0 300 170"`) {
		t.Fatalf("missing viewBox: %s", svg)
	}
	if n := strings.Count(svg, "<rect"); n != 3 {
		t.Fatalf("expected 3 bars, got %d", n)
	}
	if n := strings.Count(svg, "<line"); n != 3 {
		t.Fatalf("expected 3 grid lines, got %d", n)
	}
	if !strings.Contains(svg, "Milan &amp; &lt;Turin&gt;") || strings.Contains(svg, "<Turin>") {
		t.Fatalf("labels not escaped: %s", svg)
	}
	if !strings.Contains(svg, "Cost &lt;by&gt; office &amp; team") {
		t.Fatalf("chart label not escaped: %s", svg)
	}
	if !strings.Contains(svg, ">49%<") {
		t.Fatalf("expected value with unit: %s", svg)
	}
	if !strings.Contains(svg, ">50<") || !strings.Contains(svg, ">25<") {
		t.Fatalf("expected nice max ticks 25 and 50: %s", svg)
	}
	if !strings.Contains(svg, `fill="var(--sa)"`) || !strings.Contains(svg, `rx="4"`) {
		t.Fatalf("missing accent fill or rx: %s", svg)
	}
	assertSafeSVG(t, svg)
	assertInsideViewBox(t, svg)
}

func TestChartSVGUnitWithSpaceOrSlashIsHidden(t *testing.T) {
	for _, u := range []string{"k EUR", "/5"} {
		svg := ChartSVG(StudioChart{Kind: ChartBar, Unit: u, Data: []ChartPoint{{K: "a", V: 3}}})
		if strings.Contains(svg, ">3"+u) || strings.Contains(svg, ">3k") {
			t.Fatalf("unit %q should not be printed: %s", u, svg)
		}
	}
}

func TestChartSVGLine(t *testing.T) {
	svg := ChartSVG(StudioChart{
		Kind: ChartLine,
		Data: []ChartPoint{{K: "W1", V: 4}, {K: "W2", V: 4.4}, {K: "W3", V: 3}},
	})
	if n := strings.Count(svg, "<path"); n != 2 {
		t.Fatalf("expected area + line paths, got %d", n)
	}
	if n := strings.Count(svg, "<circle"); n != 3 {
		t.Fatalf("expected 3 dots, got %d", n)
	}
	if strings.Count(svg, `r="5"`) != 1 || strings.Count(svg, `r="3.4"`) != 2 {
		t.Fatalf("expected last dot r 5, others 3.4: %s", svg)
	}
	if !strings.Contains(svg, `fill-opacity=".14"`) || !strings.Contains(svg, `stroke-width="2.6"`) {
		t.Fatalf("missing area opacity or stroke width: %s", svg)
	}
	if strings.Contains(svg, "<rect") {
		t.Fatalf("line chart must not draw bars: %s", svg)
	}
	if !strings.Contains(svg, ">5<") || !strings.Contains(svg, ">2.5<") {
		t.Fatalf("expected ticks of niceMax(4.4)=5: %s", svg)
	}
	assertSafeSVG(t, svg)
	assertInsideViewBox(t, svg)
}

func TestChartSVGNegativeValues(t *testing.T) {
	sets := map[string][]ChartPoint{
		"mixed":        {{K: "a", V: 12}, {K: "b", V: -30}, {K: "c", V: 49}, {K: "d", V: 0}},
		"all-negative": {{K: "a", V: -4}, {K: "b", V: -12}, {K: "c", V: -0.5}},
		"all-zero":     {{K: "a", V: 0}, {K: "b", V: 0}},
	}
	for name, data := range sets {
		for _, kind := range []ChartKind{ChartBar, ChartLine} {
			t.Run(name+"/"+string(kind), func(t *testing.T) {
				svg := ChartSVG(StudioChart{Kind: kind, Label: "x", Data: data})
				assertSafeSVG(t, svg)
				assertInsideViewBox(t, svg)
				if kind == ChartBar && strings.Count(svg, "<rect") != len(data) {
					t.Fatalf("expected %d bars: %s", len(data), svg)
				}
			})
		}
	}
}

func TestChartSVGNegativeBarsHangFromBaseline(t *testing.T) {
	svg := ChartSVG(StudioChart{Kind: ChartBar, Data: []ChartPoint{{K: "a", V: 10}, {K: "b", V: -10}}})
	rects := regexp.MustCompile(`<rect x="[^"]+" y="([^"]+)" width="[^"]+" height="([^"]+)"`).FindAllStringSubmatch(svg, -1)
	if len(rects) != 2 {
		t.Fatalf("expected 2 rects: %s", svg)
	}
	posY, _ := strconv.ParseFloat(rects[0][1], 64)
	posH, _ := strconv.ParseFloat(rects[0][2], 64)
	negY, _ := strconv.ParseFloat(rects[1][1], 64)
	if posY+posH != negY {
		t.Fatalf("positive bar must end where negative bar starts (baseline): %v+%v vs %v", posY, posH, negY)
	}
}

func TestChartSVGEmptyAndNonFiniteAreSafe(t *testing.T) {
	assertInsideViewBox(t, ChartSVG(StudioChart{Kind: ChartBar}))
	assertInsideViewBox(t, ChartSVG(StudioChart{Kind: ChartLine}))
}

var (
	attrNumRe  = regexp.MustCompile(`\s(x|y|x1|x2|y1|y2|cx|cy|width|height|r)="([^"]*)"`)
	pathDRe    = regexp.MustCompile(`\sd="([^"]*)"`)
	pathNumsRe = regexp.MustCompile(`-?[0-9.]+(?:e[-+]?[0-9]+)?`)
	tagRe      = regexp.MustCompile(`<([a-zA-Z]+)`)
	attrNameRe = regexp.MustCompile(`\s([a-zA-Z:-]+)="`)
)

func assertInsideViewBox(t *testing.T, svg string) {
	t.Helper()
	for _, m := range attrNumRe.FindAllStringSubmatch(svg, -1) {
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("attribute %s=%q is not a number in %s", m[1], m[2], svg)
		}
		switch m[1] {
		case "width", "height", "r":
			if v < 0 {
				t.Fatalf("negative %s=%v in %s", m[1], v, svg)
			}
		case "y", "y1", "y2", "cy":
			if v < 0 || v > 170 {
				t.Fatalf("%s=%v outside the viewBox in %s", m[1], v, svg)
			}
		case "x", "x1", "x2", "cx":
			if v < 0 || v > 300 {
				t.Fatalf("%s=%v outside the viewBox in %s", m[1], v, svg)
			}
		}
	}
	for _, m := range pathDRe.FindAllStringSubmatch(svg, -1) {
		nums := pathNumsRe.FindAllString(m[1], -1)
		for i, s := range nums {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				t.Fatalf("bad path number %q", s)
			}
			limit := 300.0
			if i%2 == 1 {
				limit = 170
			}
			if v < 0 || v > limit {
				t.Fatalf("path coordinate %v outside the viewBox in %s", v, m[1])
			}
		}
	}
	if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
		t.Fatalf("non-finite number in %s", svg)
	}
}

// assertSafeSVG mirrors the allowlists of the presenter's sanitizeSvg so the
// chart survives it unchanged.
func assertSafeSVG(t *testing.T, svg string) {
	t.Helper()
	tags := map[string]bool{"svg": true, "g": true, "path": true, "rect": true, "circle": true, "line": true, "text": true, "title": true}
	attrs := map[string]bool{
		"xmlns": true, "viewBox": true, "width": true, "height": true, "x": true, "y": true,
		"x1": true, "y1": true, "x2": true, "y2": true, "cx": true, "cy": true, "r": true,
		"rx": true, "d": true, "fill": true, "stroke": true, "stroke-width": true,
		"stroke-linecap": true, "stroke-linejoin": true, "fill-opacity": true,
		"stroke-opacity": true, "text-anchor": true, "font-size": true, "font-weight": true,
	}
	for _, m := range tagRe.FindAllStringSubmatch(svg, -1) {
		if !tags[m[1]] {
			t.Fatalf("tag %q is not allowed: %s", m[1], svg)
		}
	}
	for _, m := range attrNameRe.FindAllStringSubmatch(svg, -1) {
		if !attrs[m[1]] {
			t.Fatalf("attribute %q is not allowed: %s", m[1], svg)
		}
	}
	if strings.Contains(svg, "href") || strings.Contains(svg, "url(") || strings.Contains(svg, "<script") {
		t.Fatalf("unsafe content: %s", svg)
	}
}
