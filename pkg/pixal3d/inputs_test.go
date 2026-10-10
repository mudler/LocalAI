package pixal3d

import (
	"bytes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func TestPixal3D(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Pixal3D input contract") }

var _ = Describe("canonical Pixal3D inputs", func() {
	It("validates all required inputs before model loading", func() {
		good := []string{"front", "right", "back", "left"}
		cases := []struct {
			name                string
			image               string
			views               []string
			scale               float64
			quality, background string
			bad                 bool
		}{
			{"canonical", "", good, 1, "1024", "keep", false},
			{"defaults", "", good, 0.5, "", "", false},
			{"missing views", "", nil, 1, "", "", true},
			{"three views", "", good[:3], 1, "", "", true},
			{"ambiguous", "one", good, 1, "", "", true},
			{"empty view", "", []string{"front", "", "back", "left"}, 1, "", "", true},
			{"missing scale", "", good, 0, "", "", true},
			{"negative scale", "", good, -1, "", "", true},
			{"nan scale", "", good, math.NaN(), "", "", true},
			{"infinite scale", "", good, math.Inf(1), "", "", true},
			{"wrong resolution", "", good, 1, "512", "", true},
			{"remove background", "", good, 1, "", "auto", true},
		}
		for _, c := range cases {
			err := ValidateRequest(c.image, c.views, c.scale, c.quality, c.background)
			Expect(err != nil).To(Equal(c.bad), c.name)
		}
	})
	It("requires decodable RGBA PNGs", func() {
		for _, transparent := range []bool{false, true} {
			img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
			for y := 0; y < 2; y++ {
				for x := 0; x < 2; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
				}
			}
			if transparent {
				img.SetNRGBA(0, 0, color.NRGBA{A: 0})
			}
			var b bytes.Buffer
			Expect(png.Encode(&b, img)).To(Succeed())
			Expect(ValidatePNG(b.Bytes()) == nil).To(Equal(transparent))
		}
		Expect(ValidatePNG([]byte("not PNG"))).NotTo(Succeed())
	})
})
