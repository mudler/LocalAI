package deck

import (
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("chart SVG", func() {
	It("rounds a maximum up to a nice axis value", func() {
		cases := map[float64]float64{49: 50, 4.4: 5, 1: 1, 10: 10, 11: 20, 0.3: 0.5, 0: 1, -3: 1}
		for in, want := range cases {
			Expect(niceMax(in)).To(Equal(want), "niceMax(%v)", in)
		}
	})

	It("draws a bar chart", func() {
		svg := ChartSVG(StudioChart{
			Kind:  ChartBar,
			Label: "Cost <by> office & team",
			Unit:  "%",
			Data:  []ChartPoint{{K: "Rome", V: 12}, {K: "Milan & <Turin>", V: 49}, {K: "Naples", V: 30}},
		})
		Expect(svg).To(HavePrefix("<svg"))
		Expect(svg).To(HaveSuffix("</svg>"))
		Expect(svg).To(ContainSubstring(`viewBox="0 0 300 170"`))
		Expect(strings.Count(svg, "<rect")).To(Equal(3), "bars")
		Expect(strings.Count(svg, "<line")).To(Equal(3), "grid lines")
		Expect(svg).To(ContainSubstring("Milan &amp; &lt;Turin&gt;"), "labels not escaped")
		Expect(svg).NotTo(ContainSubstring("<Turin>"), "labels not escaped")
		Expect(svg).To(ContainSubstring("Cost &lt;by&gt; office &amp; team"), "chart label not escaped")
		Expect(svg).To(ContainSubstring(">49%<"), "expected value with unit")
		Expect(svg).To(ContainSubstring(">50<"), "expected nice max ticks 25 and 50")
		Expect(svg).To(ContainSubstring(">25<"), "expected nice max ticks 25 and 50")
		Expect(svg).To(ContainSubstring(`fill="var(--sa)"`))
		Expect(svg).To(ContainSubstring(`rx="4"`))
		assertSafeSVG(svg)
		assertInsideViewBox(svg)
	})

	It("hides a unit that has a space or a slash", func() {
		for _, u := range []string{"k EUR", "/5"} {
			svg := ChartSVG(StudioChart{Kind: ChartBar, Unit: u, Data: []ChartPoint{{K: "a", V: 3}}})
			Expect(svg).NotTo(ContainSubstring(">3"+u), "unit %q should not be printed", u)
			Expect(svg).NotTo(ContainSubstring(">3k"), "unit %q should not be printed", u)
		}
	})

	It("draws a line chart", func() {
		svg := ChartSVG(StudioChart{
			Kind: ChartLine,
			Data: []ChartPoint{{K: "W1", V: 4}, {K: "W2", V: 4.4}, {K: "W3", V: 3}},
		})
		Expect(strings.Count(svg, "<path")).To(Equal(2), "area + line paths")
		Expect(strings.Count(svg, "<circle")).To(Equal(3), "dots")
		Expect(strings.Count(svg, `r="5"`)).To(Equal(1), "last dot r 5")
		Expect(strings.Count(svg, `r="3.4"`)).To(Equal(2), "other dots r 3.4")
		Expect(svg).To(ContainSubstring(`fill-opacity=".14"`))
		Expect(svg).To(ContainSubstring(`stroke-width="2.6"`))
		Expect(svg).NotTo(ContainSubstring("<rect"), "line chart must not draw bars")
		Expect(svg).To(ContainSubstring(">5<"), "ticks of niceMax(4.4)=5")
		Expect(svg).To(ContainSubstring(">2.5<"), "ticks of niceMax(4.4)=5")
		assertSafeSVG(svg)
		assertInsideViewBox(svg)
	})

	DescribeTable("negative values stay safe and inside the viewBox",
		func(data []ChartPoint, kind ChartKind) {
			svg := ChartSVG(StudioChart{Kind: kind, Label: "x", Data: data})
			assertSafeSVG(svg)
			assertInsideViewBox(svg)
			if kind == ChartBar {
				Expect(strings.Count(svg, "<rect")).To(Equal(len(data)), "bars: %s", svg)
			}
		},
		Entry("mixed bar", []ChartPoint{{K: "a", V: 12}, {K: "b", V: -30}, {K: "c", V: 49}, {K: "d", V: 0}}, ChartBar),
		Entry("mixed line", []ChartPoint{{K: "a", V: 12}, {K: "b", V: -30}, {K: "c", V: 49}, {K: "d", V: 0}}, ChartLine),
		Entry("all-negative bar", []ChartPoint{{K: "a", V: -4}, {K: "b", V: -12}, {K: "c", V: -0.5}}, ChartBar),
		Entry("all-negative line", []ChartPoint{{K: "a", V: -4}, {K: "b", V: -12}, {K: "c", V: -0.5}}, ChartLine),
		Entry("all-zero bar", []ChartPoint{{K: "a", V: 0}, {K: "b", V: 0}}, ChartBar),
		Entry("all-zero line", []ChartPoint{{K: "a", V: 0}, {K: "b", V: 0}}, ChartLine),
	)

	It("hangs negative bars from the baseline", func() {
		svg := ChartSVG(StudioChart{Kind: ChartBar, Data: []ChartPoint{{K: "a", V: 10}, {K: "b", V: -10}}})
		rects := regexp.MustCompile(`<rect x="[^"]+" y="([^"]+)" width="[^"]+" height="([^"]+)"`).FindAllStringSubmatch(svg, -1)
		Expect(rects).To(HaveLen(2), svg)
		posY, _ := strconv.ParseFloat(rects[0][1], 64)
		posH, _ := strconv.ParseFloat(rects[0][2], 64)
		negY, _ := strconv.ParseFloat(rects[1][1], 64)
		Expect(posY+posH).To(Equal(negY), "positive bar must end where negative bar starts (baseline)")
	})

	It("is safe for an empty chart", func() {
		assertInsideViewBox(ChartSVG(StudioChart{Kind: ChartBar}))
		assertInsideViewBox(ChartSVG(StudioChart{Kind: ChartLine}))
	})
})

var (
	attrNumRe  = regexp.MustCompile(`\s(x|y|x1|x2|y1|y2|cx|cy|width|height|r)="([^"]*)"`)
	pathDRe    = regexp.MustCompile(`\sd="([^"]*)"`)
	pathNumsRe = regexp.MustCompile(`-?[0-9.]+(?:e[-+]?[0-9]+)?`)
	tagRe      = regexp.MustCompile(`<([a-zA-Z]+)`)
	attrNameRe = regexp.MustCompile(`\s([a-zA-Z:-]+)="`)
)

func assertInsideViewBox(svg string) {
	GinkgoHelper()
	for _, m := range attrNumRe.FindAllStringSubmatch(svg, -1) {
		v, err := strconv.ParseFloat(m[2], 64)
		Expect(err).NotTo(HaveOccurred(), "attribute %s=%q is not a number in %s", m[1], m[2], svg)
		switch m[1] {
		case "width", "height", "r":
			Expect(v).To(BeNumerically(">=", 0), "negative %s in %s", m[1], svg)
		case "y", "y1", "y2", "cy":
			Expect(v).To(BeNumerically(">=", 0), "%s outside the viewBox in %s", m[1], svg)
			Expect(v).To(BeNumerically("<=", 170), "%s outside the viewBox in %s", m[1], svg)
		case "x", "x1", "x2", "cx":
			Expect(v).To(BeNumerically(">=", 0), "%s outside the viewBox in %s", m[1], svg)
			Expect(v).To(BeNumerically("<=", 300), "%s outside the viewBox in %s", m[1], svg)
		}
	}
	for _, m := range pathDRe.FindAllStringSubmatch(svg, -1) {
		nums := pathNumsRe.FindAllString(m[1], -1)
		for i, s := range nums {
			v, err := strconv.ParseFloat(s, 64)
			Expect(err).NotTo(HaveOccurred(), "bad path number %q", s)
			limit := 300.0
			if i%2 == 1 {
				limit = 170
			}
			Expect(v).To(BeNumerically(">=", 0), "path coordinate outside the viewBox in %s", m[1])
			Expect(v).To(BeNumerically("<=", limit), "path coordinate outside the viewBox in %s", m[1])
		}
	}
	Expect(svg).NotTo(ContainSubstring("NaN"), "non-finite number")
	Expect(svg).NotTo(ContainSubstring("Inf"), "non-finite number")
}

// assertSafeSVG mirrors the allowlists of the presenter's sanitizeSvg so the
// chart survives it unchanged.
func assertSafeSVG(svg string) {
	GinkgoHelper()
	tags := map[string]bool{"svg": true, "g": true, "path": true, "rect": true, "circle": true, "line": true, "text": true, "title": true}
	attrs := map[string]bool{
		"xmlns": true, "viewBox": true, "width": true, "height": true, "x": true, "y": true,
		"x1": true, "y1": true, "x2": true, "y2": true, "cx": true, "cy": true, "r": true,
		"rx": true, "d": true, "fill": true, "stroke": true, "stroke-width": true,
		"stroke-linecap": true, "stroke-linejoin": true, "fill-opacity": true,
		"stroke-opacity": true, "text-anchor": true, "font-size": true, "font-weight": true,
	}
	for _, m := range tagRe.FindAllStringSubmatch(svg, -1) {
		Expect(tags).To(HaveKey(m[1]), "tag %q is not allowed: %s", m[1], svg)
	}
	for _, m := range attrNameRe.FindAllStringSubmatch(svg, -1) {
		Expect(attrs).To(HaveKey(m[1]), "attribute %q is not allowed: %s", m[1], svg)
	}
	Expect(svg).NotTo(ContainSubstring("href"), "unsafe content")
	Expect(svg).NotTo(ContainSubstring("url("), "unsafe content")
	Expect(svg).NotTo(ContainSubstring("<script"), "unsafe content")
}
