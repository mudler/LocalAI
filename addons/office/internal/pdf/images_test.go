package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pdfObj is one numbered object of a hand-written PDF. dict is the text
// inside << >>. When stream is set the object is a stream, and its /Length
// is added here.
type pdfObj struct {
	dict   string
	stream []byte
}

// buildPDF writes objs as objects 1..n with a cross-reference table and a
// trailer whose /Root is object 1.
func buildPDF(objs []pdfObj) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		if o.stream == nil {
			fmt.Fprintf(&b, "<< %s >>\nendobj\n", o.dict)
			continue
		}
		fmt.Fprintf(&b, "<< %s /Length %d >>\nstream\n", o.dict, len(o.stream))
		b.Write(o.stream)
		b.WriteString("\nendstream\nendobj\n")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// pagesDoc builds a document with one page per resources entry. Each entry
// is the text of that page's /Resources. Object 1 is the catalog, object 2
// the page tree and page i is object 3+i. The extra objects follow the pages,
// so the first of them is object 3+len(resources).
func pagesDoc(resources []string, extra ...pdfObj) []byte {
	kids := ""
	for i := range resources {
		kids += fmt.Sprintf("%d 0 R ", 3+i)
	}
	objs := []pdfObj{
		{dict: "/Type /Catalog /Pages 2 0 R"},
		{dict: fmt.Sprintf("/Type /Pages /Kids [%s] /Count %d", kids, len(resources))},
	}
	for _, res := range resources {
		objs = append(objs, pdfObj{dict: "/Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources " + res})
	}
	return buildPDF(append(objs, extra...))
}

// zlibBytes compresses b as a zlib stream, the way a Flate filter expects.
func zlibBytes(b []byte) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err := w.Write(b)
	Expect(err).ToNot(HaveOccurred())
	Expect(w.Close()).To(Succeed())
	return buf.Bytes()
}

// flateRGB returns an 8-bit DeviceRGB Flate image object of w by h pixels
// with the given raw samples.
func flateRGB(w, h int, samples []byte, extraEntries string) pdfObj {
	GinkgoHelper()
	return pdfObj{
		dict:   fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode %s", w, h, extraEntries),
		stream: zlibBytes(samples),
	}
}

// xobjectRes returns a /Resources dictionary with the given names mapped to
// object numbers.
func xobjectRes(entries ...string) string {
	return "<< /XObject << " + strings.Join(entries, " ") + " >> >>"
}

// pngFilter applies one PNG row filter to cur. prev is the row above, or nil
// for the first row. The filter is written from the PNG specification and
// does not use the package's own code.
func pngFilter(ft byte, cur, prev []byte, bpp int) []byte {
	out := make([]byte, 1+len(cur))
	out[0] = ft
	for i := range cur {
		var a, b, c int
		if i >= bpp {
			a = int(cur[i-bpp])
		}
		if prev != nil {
			b = int(prev[i])
			if i >= bpp {
				c = int(prev[i-bpp])
			}
		}
		var pred int
		switch ft {
		case 0:
			pred = 0
		case 1:
			pred = a
		case 2:
			pred = b
		case 3:
			pred = (a + b) / 2
		case 4:
			p := a + b - c
			pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
			switch {
			case pa <= pb && pa <= pc:
				pred = a
			case pb <= pc:
				pred = b
			default:
				pred = c
			}
		}
		out[1+i] = cur[i] - byte(pred)
	}
	return out
}

// decodePNG decodes PNG bytes and fails the spec when they are not a PNG.
func decodePNG(data []byte) image.Image {
	GinkgoHelper()
	img, err := png.Decode(bytes.NewReader(data))
	Expect(err).ToNot(HaveOccurred(), "decode png")
	return img
}

// rgbaAt returns the colour of one pixel. Gray pixels come back with equal
// R, G and B.
func rgbaAt(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

// onlyImage returns the single image of a result or fails the spec.
func onlyImage(imgs []Image) Image {
	GinkgoHelper()
	Expect(imgs).To(HaveLen(1))
	return imgs[0]
}

// mustExtract runs ExtractImages or fails the spec.
func mustExtract(data []byte, pages []int) []Image {
	GinkgoHelper()
	imgs, err := ExtractImages(data, pages)
	Expect(err).ToNot(HaveOccurred(), "ExtractImages")
	return imgs
}

func imagesByName(imgs []Image) map[string]Image {
	byName := map[string]Image{}
	for _, im := range imgs {
		byName[im.Name] = im
	}
	return byName
}

var _ = Describe("ExtractImages", func() {
	It("extracts a Flate RGB image with PNG predictors exactly", func() {
		const w, h = 4, 5
		bpp := 3
		// Every PNG filter type (0 to 4) is used, one per row, so each unfilter
		// path runs. Values wrap past 255 on purpose.
		want := make([][]byte, h)
		for y := range want {
			want[y] = make([]byte, w*3)
			for x := 0; x < w; x++ {
				want[y][3*x] = byte(x*97 + y*31)
				want[y][3*x+1] = byte(y*83 + x*7)
				want[y][3*x+2] = byte(250 - x*40 - y*9)
			}
		}
		var filtered []byte
		for y := 0; y < h; y++ {
			var prev []byte
			if y > 0 {
				prev = want[y-1]
			}
			filtered = append(filtered, pngFilter(byte(y), want[y], prev, bpp)...)
		}
		img := pdfObj{
			dict:   fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << /Predictor 15 /Colors 3 /Columns %d /BitsPerComponent 8 >>", w, h, w),
			stream: zlibBytes(filtered),
		}
		data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, img)

		got := onlyImage(mustExtract(data, nil))
		Expect(got.Format).To(Equal("png"), "unexpected image: %+v", got)
		Expect(got.Skipped).To(BeEmpty(), "unexpected image: %+v", got)
		Expect(got.Page).To(Equal(1))
		Expect(got.Name).To(Equal("Im1"))
		Expect(got.Width).To(Equal(w), "metadata: %+v", got)
		Expect(got.Height).To(Equal(h), "metadata: %+v", got)
		Expect(got.BitsPerComponent).To(Equal(8), "metadata: %+v", got)
		Expect(got.ColorSpace).To(Equal("DeviceRGB"), "metadata: %+v", got)
		px := decodePNG(got.Data)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := rgbaAt(px, x, y)
				wantPix := color.RGBA{want[y][3*x], want[y][3*x+1], want[y][3*x+2], 0xff}
				Expect(c).To(Equal(wantPix), "pixel (%d,%d)", x, y)
			}
		}
	})

	It("extracts a one-bit gray image", func() {
		// 5 by 2 samples. Row 0 is 1 0 1 0 1 and row 1 is 1 1 1 0 0.
		// A 1 sample is white (255) and a 0 sample is black (0).
		samples := []byte{0xa8, 0xe0}
		img := pdfObj{
			dict:   "/Type /XObject /Subtype /Image /Width 5 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /FlateDecode",
			stream: zlibBytes(samples),
		}
		data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, img)

		got := onlyImage(mustExtract(data, nil))
		Expect(got.Format).To(Equal("png"), "skipped %q", got.Skipped)
		px := decodePNG(got.Data)
		want := [][]uint8{{255, 0, 255, 0, 255}, {255, 255, 255, 0, 0}}
		for y := range want {
			for x := range want[y] {
				Expect(rgbaAt(px, x, y).R).To(Equal(want[y][x]), "pixel (%d,%d)", x, y)
			}
		}
	})

	It("returns a DCT JPEG unchanged", func() {
		src := image.NewRGBA(image.Rect(0, 0, 5, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 5; x++ {
				src.Set(x, y, color.RGBA{uint8(x * 50), uint8(y * 60), 128, 0xff})
			}
		}
		var jb bytes.Buffer
		Expect(jpeg.Encode(&jb, src, &jpeg.Options{Quality: 90})).To(Succeed())
		jpg := pdfObj{
			dict:   "/Type /XObject /Subtype /Image /Width 5 /Height 4 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
			stream: jb.Bytes(),
		}
		data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, jpg)

		got := onlyImage(mustExtract(data, nil))
		Expect(got.Format).To(Equal("jpeg"), "unexpected image: %+v", got)
		Expect(got.Skipped).To(BeEmpty(), "unexpected image: %+v", got)
		Expect(bytes.Equal(got.Data, jb.Bytes())).To(BeTrue(), "Data is not the original JPEG stream")
		_, err := jpeg.Decode(bytes.NewReader(got.Data))
		Expect(err).ToNot(HaveOccurred(), "Data does not decode as JPEG")
	})

	It("skips a CMYK JPEG", func() {
		jpg := pdfObj{
			dict:   "/Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceCMYK /BitsPerComponent 8 /Filter /DCTDecode",
			stream: []byte("not decoded here"),
		}
		data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, jpg)

		got := onlyImage(mustExtract(data, nil))
		Expect(got.Skipped).To(Equal("CMYK JPEG needs conversion"))
		Expect(got.Data).To(BeNil(), "skipped image has data")
		Expect(got.Format).To(BeEmpty(), "skipped image has a format")
		Expect(got.ColorSpace).To(Equal("DeviceCMYK"))
	})

	It("skips an image mask and still processes the page", func() {
		mask := pdfObj{
			dict:   "/Type /XObject /Subtype /Image /Width 8 /Height 2 /ImageMask true /BitsPerComponent 1 /Filter /FlateDecode",
			stream: zlibBytes([]byte{0xaa, 0x55}),
		}
		rgb := flateRGB(2, 1, []byte{255, 0, 0, 0, 255, 0}, "")
		data := pagesDoc([]string{xobjectRes("/Mask 4 0 R", "/Im1 5 0 R")}, mask, rgb)

		imgs := mustExtract(data, nil)
		Expect(imgs).To(HaveLen(2))
		// Names are read in sorted order, so /Im1 comes before /Mask.
		byName := imagesByName(imgs)
		m := byName["Mask"]
		Expect(m.Skipped).To(Equal("image mask"), "mask: %+v", m)
		Expect(m.Data).To(BeNil(), "mask: %+v", m)
		im1 := byName["Im1"]
		Expect(im1.Format).To(Equal("png"), "rgb image: %+v", im1)
		Expect(im1.Skipped).To(BeEmpty(), "rgb image: %+v", im1)
		px := decodePNG(im1.Data)
		Expect(rgbaAt(px, 0, 0)).To(Equal(color.RGBA{255, 0, 0, 255}))
		Expect(rgbaAt(px, 1, 0)).To(Equal(color.RGBA{0, 255, 0, 255}))
	})

	It("returns nothing for a page without images", func() {
		data := pagesDoc([]string{"<< >>"})
		Expect(mustExtract(data, nil)).To(BeEmpty())
	})

	It("extracts only the requested page range", func() {
		// Three pages occupy objects 3 to 5, so the images are objects 6 to 8.
		extra := []pdfObj{
			flateRGB(1, 1, []byte{0, 0, 0}, ""),
			flateRGB(1, 1, []byte{1, 0, 0}, ""),
			flateRGB(1, 1, []byte{2, 0, 0}, ""),
		}
		data := pagesDoc([]string{
			xobjectRes("/Im1 6 0 R"),
			xobjectRes("/Im1 7 0 R"),
			xobjectRes("/Im1 8 0 R"),
		}, extra...)

		all := mustExtract(data, nil)
		Expect(all).To(HaveLen(3))
		Expect(all[0].Page).To(Equal(1))
		Expect(all[1].Page).To(Equal(2))
		Expect(all[2].Page).To(Equal(3))
		one := mustExtract(data, []int{2})
		Expect(one).To(HaveLen(1))
		Expect(one[0].Page).To(Equal(2))
		Expect(rgbaAt(decodePNG(one[0].Data), 0, 0).R).To(Equal(uint8(1)), "page 2 image pixel")
		_, err := ExtractImages(data, []int{4})
		Expect(err).To(HaveOccurred(), "page outside the document")
		_, err = ExtractImages(data, []int{0})
		Expect(err).To(HaveOccurred(), "page 0")
	})

	It("finds an image nested in a form", func() {
		form := pdfObj{
			dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + xobjectRes("/Im2 5 0 R"),
			stream: []byte(""),
		}
		data := pagesDoc([]string{xobjectRes("/Fm1 4 0 R")}, form, flateRGB(1, 1, []byte{1, 2, 3}, ""))

		got := onlyImage(mustExtract(data, nil))
		Expect(got.Name).To(Equal("Im2"), "nested image: %+v", got)
		Expect(got.Format).To(Equal("png"), "nested image: %+v", got)
	})

	It("terminates on a form that refers to itself", func() {
		// The walk must stop and still find the image.
		form := pdfObj{
			dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + xobjectRes("/Self 4 0 R", "/Im2 5 0 R"),
			stream: []byte(""),
		}
		data := pagesDoc([]string{xobjectRes("/Fm1 4 0 R")}, form, flateRGB(1, 1, []byte{1, 2, 3}, ""))

		got := onlyImage(mustExtract(data, nil))
		Expect(got.Name).To(Equal("Im2"))
	})

	// Each image kind that is returned as is or skipped with a reason. Every case
	// is a one-page document with the image as object 4 and, when needed, an
	// extra object 5.
	gray := "/Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode"
	DescribeTable("image kinds",
		func(img pdfObj, extra []pdfObj, wantFormat, wantSkipped string, wantData, wantSMask bool) {
			data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, append([]pdfObj{img}, extra...)...)
			got := onlyImage(mustExtract(data, nil))
			Expect(got.Format).To(Equal(wantFormat))
			if wantSkipped == "" {
				Expect(got.Skipped).To(BeEmpty())
			} else {
				Expect(got.Skipped).To(HavePrefix(wantSkipped))
			}
			Expect(got.Data != nil).To(Equal(wantData), "Data present")
			Expect(got.HasSMask).To(Equal(wantSMask))
			if wantData && wantFormat == "jbig2" {
				Expect(string(got.Data)).To(Equal("jbig2 bytes"), "JBIG2 Data is the original stream")
			}
		},
		Entry("JBIG2 without globals is passed through",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /BitsPerComponent 1 /ColorSpace /DeviceGray /Filter /JBIG2Decode", stream: []byte("jbig2 bytes")},
			[]pdfObj(nil), "jbig2", "", true, false),
		Entry("JBIG2 with globals is skipped",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /BitsPerComponent 1 /ColorSpace /DeviceGray /Filter /JBIG2Decode /DecodeParms << /JBIG2Globals 5 0 R >>", stream: []byte("jbig2 bytes")},
			[]pdfObj{{dict: "/Type /XObject", stream: []byte("globals")}}, "", "JBIG2 needs globals", false, false),
		Entry("JPX is passed through",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /Filter /JPXDecode", stream: []byte("jpx bytes")},
			[]pdfObj(nil), "jpx", "", true, false),
		Entry("CMYK Flate is skipped with its colour space",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceCMYK /BitsPerComponent 8 /Filter /FlateDecode", stream: zlibBytes([]byte{1, 2, 3, 4})},
			[]pdfObj(nil), "", "unsupported colour space DeviceCMYK", false, false),
		Entry("indexed colour space is skipped",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace [/Indexed /DeviceRGB 1 <000000ff00>] /BitsPerComponent 8 /Filter /FlateDecode", stream: zlibBytes([]byte{0})},
			[]pdfObj(nil), "", "unsupported colour space Indexed", false, false),
		Entry("LZW filter is skipped with its name",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /LZWDecode", stream: []byte("x")},
			[]pdfObj(nil), "", "unsupported filter LZWDecode", false, false),
		Entry("filter chain is skipped",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter [/ASCIIHexDecode /FlateDecode]", stream: []byte("x")},
			[]pdfObj(nil), "", "filter chain ASCIIHexDecode+FlateDecode", false, false),
		Entry("16 bit samples are skipped",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 16 /Filter /FlateDecode", stream: zlibBytes([]byte{0, 1})},
			[]pdfObj(nil), "", "unsupported bits per component 16", false, false),
		Entry("inverted JPEG Decode array is skipped",
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Decode [1 0 1 0 1 0] /Filter /DCTDecode", stream: []byte("jpeg")},
			[]pdfObj(nil), "", "non-default Decode array", false, false),
		Entry("corrupt Flate data is skipped, not an error",
			pdfObj{dict: gray, stream: []byte("garbage, not zlib")},
			[]pdfObj(nil), "", "corrupt image data", false, false),
		Entry("SMask is reported and not applied",
			pdfObj{dict: gray + " /SMask 5 0 R", stream: zlibBytes([]byte{10, 20})},
			[]pdfObj{{dict: "/Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", stream: zlibBytes([]byte{255, 0})}},
			"png", "", true, true),
	)

	// 200 images are allowed. The 201st is an error.
	It("enforces the image count cap", func() {
		build := func(n int) []byte {
			var names []string
			var extra []pdfObj
			for i := 0; i < n; i++ {
				names = append(names, fmt.Sprintf("/I%d %d 0 R", i, 4+i))
				extra = append(extra, flateRGB(1, 1, []byte{1, 2, 3}, ""))
			}
			return pagesDoc([]string{xobjectRes(names...)}, extra...)
		}
		Expect(mustExtract(build(maxImages), nil)).To(HaveLen(maxImages))
		_, err := ExtractImages(build(maxImages+1), nil)
		Expect(err).To(HaveOccurred(), "%d images", maxImages+1)
	})

	It("returns an error for input that is not a PDF", func() {
		_, err := ExtractImages([]byte("this is not a pdf file"), nil)
		Expect(err).To(HaveOccurred(), "bad input")
		_, err = ExtractImages(nil, nil)
		Expect(err).To(HaveOccurred(), "empty input")
	})

	// Cuts a PDF with several kinds of image at many lengths. Each cut must
	// return without a panic.
	It("does not panic on truncated prefixes", func() {
		good := pagesDoc(
			[]string{xobjectRes("/Im1 4 0 R", "/Im2 5 0 R")},
			flateRGB(2, 2, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, "/DecodeParms << /Predictor 2 >>"),
			pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", stream: []byte("abcdefgh")},
		)
		step := len(good) / 150
		if step < 1 {
			step = 1
		}
		for n := 0; n <= len(good); n += step {
			_, _ = ExtractImages(good[:n], nil)
		}
	})

	It("returns an error for input over the limit", func() {
		_, err := ExtractImages(make([]byte, maxImageInputBytes+1), nil)
		Expect(err).To(HaveOccurred())
	})

	// The first image claims 8000 by 8000 RGB but inflates to 3 bytes. The
	// second claims 1 pixel but inflates to 6 bytes. Both are skipped, and the
	// page's other image is kept.
	It("skips Flate images whose size claim does not match the data", func() {
		short := flateRGB(8000, 8000, []byte{1, 2, 3}, "")
		long := flateRGB(1, 1, []byte{1, 2, 3, 4, 5, 6}, "")
		ok := flateRGB(1, 1, []byte{1, 2, 3}, "")
		data := pagesDoc([]string{xobjectRes("/Short 4 0 R", "/Long 5 0 R", "/Ok 6 0 R")}, short, long, ok)

		imgs := mustExtract(data, nil)
		Expect(imgs).To(HaveLen(3))
		byName := imagesByName(imgs)
		for _, name := range []string{"Short", "Long"} {
			im := byName[name]
			Expect(im.Skipped).To(HavePrefix("corrupt image data"), "%s: %+v", name, im)
			Expect(im.Data).To(BeNil(), "%s: %+v", name, im)
		}
		im := byName["Ok"]
		Expect(im.Format).To(Equal("png"), "Ok: %+v", im)
		Expect(im.Skipped).To(BeEmpty(), "Ok: %+v", im)
	})

	// Widths that overflow the row arithmetic, and a float width that is not a
	// usable integer. Each image is skipped alone. The page's other image is
	// still returned.
	It("skips images with huge dimensions", func() {
		cases := []struct {
			width      string
			wantReason string
		}{
			{"3074457345618258603", "image dimensions too large"},
			{"9223372036854775807", "image dimensions too large"},
			{"100000000000.5", "missing image size"},
		}
		for _, c := range cases {
			big := pdfObj{
				dict:   "/Type /XObject /Subtype /Image /Width " + c.width + " /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
				stream: zlibBytes([]byte{1, 2, 3}),
			}
			data := pagesDoc([]string{xobjectRes("/Big 4 0 R", "/Ok 5 0 R")}, big, flateRGB(1, 1, []byte{1, 2, 3}, ""))

			imgs := mustExtract(data, nil)
			Expect(imgs).To(HaveLen(2), "width %s", c.width)
			byName := imagesByName(imgs)
			b := byName["Big"]
			Expect(b.Skipped).To(Equal(c.wantReason), "width %s: %+v", c.width, b)
			Expect(b.Data).To(BeNil(), "width %s: %+v", c.width, b)
			ok := byName["Ok"]
			Expect(ok.Format).To(Equal("png"), "width %s: Ok image: %+v", c.width, ok)
			Expect(ok.Skipped).To(BeEmpty(), "width %s: Ok image: %+v", c.width, ok)
		}
	})

	// 12 forms, where each form refers to the next one three times. Walked as a
	// tree, that is 3^11 visits. Each form must be walked once, so the call is
	// fast and the image appears once.
	It("walks a form DAG once", func() {
		const levels = 12
		var extra []pdfObj
		for i := 0; i < levels; i++ {
			res := xobjectRes("/Im 16 0 R") // the last form holds the image, object 16
			if i < levels-1 {
				next := fmt.Sprintf("%d 0 R", 5+i) // form i+1 is object 5+i
				res = xobjectRes("/A "+next, "/B "+next, "/C "+next)
			}
			extra = append(extra, pdfObj{
				dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + res,
				stream: []byte(""),
			})
		}
		extra = append(extra, flateRGB(1, 1, []byte{1, 2, 3}, ""))
		data := pagesDoc([]string{xobjectRes("/Fm 4 0 R")}, extra...)

		start := time.Now()
		got := onlyImage(mustExtract(data, nil))
		Expect(time.Since(start)).To(BeNumerically("<=", 2*time.Second), "form DAG duration")
		Expect(got.Name).To(Equal("Im"), "image: %+v", got)
		Expect(got.Format).To(Equal("png"), "image: %+v", got)
	})

	// Two pages share the same form (object 5), which holds one image
	// (object 6). Each page must report the image under its own number, as it
	// would when asked for alone.
	It("reports a form shared by two pages on both", func() {
		form := pdfObj{
			dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + xobjectRes("/Im 6 0 R"),
			stream: []byte(""),
		}
		data := pagesDoc([]string{xobjectRes("/Fm 5 0 R"), xobjectRes("/Fm 5 0 R")},
			form, flateRGB(1, 1, []byte{1, 2, 3}, ""))

		got := mustExtract(data, []int{1, 2})
		Expect(got).To(HaveLen(2))
		for i, page := range []int{1, 2} {
			Expect(got[i].Page).To(Equal(page), "image %d: %+v", i, got[i])
			Expect(got[i].Name).To(Equal("Im"), "image %d: %+v", i, got[i])
			Expect(got[i].Format).To(Equal("png"), "image %d: %+v", i, got[i])
		}
	})
})
