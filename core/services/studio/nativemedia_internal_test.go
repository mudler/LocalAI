package studio

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func pngOf(w, h int, alpha uint8) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: alpha})
		}
	}
	var b bytes.Buffer
	Expect(png.Encode(&b, img)).To(Succeed())
	return b.Bytes()
}

// decodedSize decodes a data: URI and returns its media type and size.
func decodedSize(uri string) (string, int, int) {
	head, data, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	Expect(ok).To(BeTrue(), "not a base64 data URI")
	raw, err := base64.StdEncoding.DecodeString(data)
	Expect(err).NotTo(HaveOccurred())
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	Expect(err).NotTo(HaveOccurred())
	return head, cfg.Width, cfg.Height
}

var _ = Describe("images in the message", func() {
	visionSvc := func() *Service {
		return &Service{d: Deps{Modalities: func(string) Modalities { return Modalities{Vision: true} }}}
	}

	It("keeps an image of a normal size as it is", func() {
		small := pngOf(640, 480, 255)
		_, parts, err := visionSvc().nativeMedia("m", []session.Upload{{Name: "a.png", Body: bytes.NewReader(small)}})
		Expect(err).NotTo(HaveOccurred())
		Expect(parts).To(HaveLen(1))
		Expect(parts[0].DataURI).To(Equal("data:image/png;base64," + base64.StdEncoding.EncodeToString(small)))
	})

	It("scales a large photo down to the longest side limit as JPEG", func() {
		big := pngOf(maxImageSide*2, maxImageSide+maxImageSide/2, 255)
		up, parts, err := visionSvc().nativeMedia("m", []session.Upload{{Name: "photo.png", Body: bytes.NewReader(big)}})
		Expect(err).NotTo(HaveOccurred())
		Expect(parts).To(HaveLen(1))
		mime, w, h := decodedSize(parts[0].DataURI)
		Expect(mime).To(Equal("image/jpeg"))
		Expect(w).To(Equal(maxImageSide))
		Expect(h).To(Equal(maxImageSide * 3 / 4))
		kept := new(bytes.Buffer)
		_, _ = kept.ReadFrom(up[0].Body)
		Expect(kept.Bytes()).To(Equal(big), "the file itself must be kept as it is")
	})

	It("keeps the transparency of a large PNG", func() {
		big := pngOf(maxImageSide+10, 100, 128)
		_, parts, err := visionSvc().nativeMedia("m", []session.Upload{{Name: "logo.png", Body: bytes.NewReader(big)}})
		Expect(err).NotTo(HaveOccurred())
		mime, w, _ := decodedSize(parts[0].DataURI)
		Expect(mime).To(Equal("image/png"))
		Expect(w).To(Equal(maxImageSide))
	})
})
