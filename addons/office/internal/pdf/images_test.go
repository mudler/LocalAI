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
	"testing"
	"time"
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
func zlibBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// flateRGB returns an 8-bit DeviceRGB Flate image object of w by h pixels
// with the given raw samples.
func flateRGB(t *testing.T, w, h int, samples []byte, extraEntries string) pdfObj {
	t.Helper()
	return pdfObj{
		dict:   fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode %s", w, h, extraEntries),
		stream: zlibBytes(t, samples),
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

// decodePNG decodes PNG bytes and fails the test when they are not a PNG.
func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

// rgbaAt returns the colour of one pixel. Gray pixels come back with equal
// R, G and B.
func rgbaAt(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

// onlyImage returns the single image of a result or fails the test.
func onlyImage(t *testing.T, imgs []Image) Image {
	t.Helper()
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1: %+v", len(imgs), imgs)
	}
	return imgs[0]
}

func TestExtractFlateRGBWithPNGPredictorIsExact(t *testing.T) {
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
		stream: zlibBytes(t, filtered),
	}
	data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, img)

	got := onlyImage(t, mustExtract(t, data, nil))
	if got.Format != "png" || got.Skipped != "" || got.Page != 1 || got.Name != "Im1" {
		t.Fatalf("unexpected image: %+v", got)
	}
	if got.Width != w || got.Height != h || got.BitsPerComponent != 8 || got.ColorSpace != "DeviceRGB" {
		t.Errorf("metadata: %+v", got)
	}
	px := decodePNG(t, got.Data)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := rgbaAt(px, x, y)
			wantPix := color.RGBA{want[y][3*x], want[y][3*x+1], want[y][3*x+2], 0xff}
			if c != wantPix {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, c, wantPix)
			}
		}
	}
}

func TestExtractFlateOneBitGray(t *testing.T) {
	// 5 by 2 samples. Row 0 is 1 0 1 0 1 and row 1 is 1 1 1 0 0.
	// A 1 sample is white (255) and a 0 sample is black (0).
	samples := []byte{0xa8, 0xe0}
	img := pdfObj{
		dict:   "/Type /XObject /Subtype /Image /Width 5 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /FlateDecode",
		stream: zlibBytes(t, samples),
	}
	data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, img)

	got := onlyImage(t, mustExtract(t, data, nil))
	if got.Format != "png" {
		t.Fatalf("format %q, skipped %q", got.Format, got.Skipped)
	}
	px := decodePNG(t, got.Data)
	want := [][]uint8{{255, 0, 255, 0, 255}, {255, 255, 255, 0, 0}}
	for y := range want {
		for x := range want[y] {
			if v := rgbaAt(px, x, y).R; v != want[y][x] {
				t.Errorf("pixel (%d,%d) = %d, want %d", x, y, v, want[y][x])
			}
		}
	}
}

func TestExtractDCTJPEGIsReturnedUnchanged(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 5, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 5; x++ {
			src.Set(x, y, color.RGBA{uint8(x * 50), uint8(y * 60), 128, 0xff})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	jpg := pdfObj{
		dict:   "/Type /XObject /Subtype /Image /Width 5 /Height 4 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		stream: jb.Bytes(),
	}
	data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, jpg)

	got := onlyImage(t, mustExtract(t, data, nil))
	if got.Format != "jpeg" || got.Skipped != "" {
		t.Fatalf("unexpected image: %+v", got)
	}
	if !bytes.Equal(got.Data, jb.Bytes()) {
		t.Fatal("Data is not the original JPEG stream")
	}
	if _, err := jpeg.Decode(bytes.NewReader(got.Data)); err != nil {
		t.Fatalf("Data does not decode as JPEG: %v", err)
	}
}

func TestExtractCMYKJPEGIsSkipped(t *testing.T) {
	jpg := pdfObj{
		dict:   "/Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceCMYK /BitsPerComponent 8 /Filter /DCTDecode",
		stream: []byte("not decoded here"),
	}
	data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, jpg)

	got := onlyImage(t, mustExtract(t, data, nil))
	if got.Skipped != "CMYK JPEG needs conversion" {
		t.Errorf("Skipped = %q", got.Skipped)
	}
	if got.Data != nil || got.Format != "" {
		t.Errorf("skipped image has data: format %q, %d bytes", got.Format, len(got.Data))
	}
	if got.ColorSpace != "DeviceCMYK" {
		t.Errorf("ColorSpace = %q", got.ColorSpace)
	}
}

func TestExtractImageMaskIsSkippedAndPageStillProcessed(t *testing.T) {
	mask := pdfObj{
		dict:   "/Type /XObject /Subtype /Image /Width 8 /Height 2 /ImageMask true /BitsPerComponent 1 /Filter /FlateDecode",
		stream: zlibBytes(t, []byte{0xaa, 0x55}),
	}
	rgb := flateRGB(t, 2, 1, []byte{255, 0, 0, 0, 255, 0}, "")
	data := pagesDoc([]string{xobjectRes("/Mask 4 0 R", "/Im1 5 0 R")}, mask, rgb)

	imgs := mustExtract(t, data, nil)
	if len(imgs) != 2 {
		t.Fatalf("got %d images, want 2: %+v", len(imgs), imgs)
	}
	// Names are read in sorted order, so /Im1 comes before /Mask.
	byName := map[string]Image{}
	for _, im := range imgs {
		byName[im.Name] = im
	}
	if m := byName["Mask"]; m.Skipped != "image mask" || m.Data != nil {
		t.Errorf("mask: %+v", m)
	}
	im1 := byName["Im1"]
	if im1.Format != "png" || im1.Skipped != "" {
		t.Fatalf("rgb image: %+v", im1)
	}
	px := decodePNG(t, im1.Data)
	if rgbaAt(px, 0, 0) != (color.RGBA{255, 0, 0, 255}) || rgbaAt(px, 1, 0) != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("rgb pixels: %v %v", rgbaAt(px, 0, 0), rgbaAt(px, 1, 0))
	}
}

func TestExtractPageWithoutImagesIsEmpty(t *testing.T) {
	data := pagesDoc([]string{"<< >>"})
	if imgs := mustExtract(t, data, nil); len(imgs) != 0 {
		t.Errorf("got %d images, want none: %+v", len(imgs), imgs)
	}
}

func TestExtractPageRange(t *testing.T) {
	// Three pages occupy objects 3 to 5, so the images are objects 6 to 8.
	extra := []pdfObj{
		flateRGB(t, 1, 1, []byte{0, 0, 0}, ""),
		flateRGB(t, 1, 1, []byte{1, 0, 0}, ""),
		flateRGB(t, 1, 1, []byte{2, 0, 0}, ""),
	}
	data := pagesDoc([]string{
		xobjectRes("/Im1 6 0 R"),
		xobjectRes("/Im1 7 0 R"),
		xobjectRes("/Im1 8 0 R"),
	}, extra...)

	all := mustExtract(t, data, nil)
	if len(all) != 3 || all[0].Page != 1 || all[1].Page != 2 || all[2].Page != 3 {
		t.Fatalf("all pages: %+v", all)
	}
	one := mustExtract(t, data, []int{2})
	if len(one) != 1 || one[0].Page != 2 {
		t.Fatalf("page 2 only: %+v", one)
	}
	if px := decodePNG(t, one[0].Data); rgbaAt(px, 0, 0).R != 1 {
		t.Errorf("page 2 image has the wrong pixel: %v", rgbaAt(px, 0, 0))
	}
	if _, err := ExtractImages(data, []int{4}); err == nil {
		t.Error("page outside the document: want an error")
	}
	if _, err := ExtractImages(data, []int{0}); err == nil {
		t.Error("page 0: want an error")
	}
}

func TestExtractNestedFormImage(t *testing.T) {
	form := pdfObj{
		dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + xobjectRes("/Im2 5 0 R"),
		stream: []byte(""),
	}
	data := pagesDoc([]string{xobjectRes("/Fm1 4 0 R")}, form, flateRGB(t, 1, 1, []byte{1, 2, 3}, ""))

	got := onlyImage(t, mustExtract(t, data, nil))
	if got.Name != "Im2" || got.Format != "png" {
		t.Errorf("nested image: %+v", got)
	}
}

func TestExtractFormCycleTerminates(t *testing.T) {
	// The form refers to itself. The walk must stop and still find the image.
	form := pdfObj{
		dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + xobjectRes("/Self 4 0 R", "/Im2 5 0 R"),
		stream: []byte(""),
	}
	data := pagesDoc([]string{xobjectRes("/Fm1 4 0 R")}, form, flateRGB(t, 1, 1, []byte{1, 2, 3}, ""))

	got := onlyImage(t, mustExtract(t, data, nil))
	if got.Name != "Im2" {
		t.Errorf("image: %+v", got)
	}
}

// TestExtractKinds checks each image kind that is returned as is or skipped
// with a reason. Every case is a one-page document with the image as object 4
// and, when needed, an extra object 5.
func TestExtractKinds(t *testing.T) {
	gray := "/Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode"
	cases := []struct {
		name        string
		img         pdfObj
		extra       []pdfObj
		wantFormat  string
		wantSkipped string
		wantData    bool
		wantSMask   bool
	}{
		{
			name:       "JBIG2 without globals is passed through",
			img:        pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /BitsPerComponent 1 /ColorSpace /DeviceGray /Filter /JBIG2Decode", stream: []byte("jbig2 bytes")},
			wantFormat: "jbig2",
			wantData:   true,
		},
		{
			name:        "JBIG2 with globals is skipped",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /BitsPerComponent 1 /ColorSpace /DeviceGray /Filter /JBIG2Decode /DecodeParms << /JBIG2Globals 5 0 R >>", stream: []byte("jbig2 bytes")},
			extra:       []pdfObj{{dict: "/Type /XObject", stream: []byte("globals")}},
			wantSkipped: "JBIG2 needs globals",
		},
		{
			name:       "JPX is passed through",
			img:        pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /Filter /JPXDecode", stream: []byte("jpx bytes")},
			wantFormat: "jpx",
			wantData:   true,
		},
		{
			name:        "CMYK Flate is skipped with its colour space",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceCMYK /BitsPerComponent 8 /Filter /FlateDecode", stream: zlibBytes(t, []byte{1, 2, 3, 4})},
			wantSkipped: "unsupported colour space DeviceCMYK",
		},
		{
			name:        "indexed colour space is skipped",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace [/Indexed /DeviceRGB 1 <000000ff00>] /BitsPerComponent 8 /Filter /FlateDecode", stream: zlibBytes(t, []byte{0})},
			wantSkipped: "unsupported colour space Indexed",
		},
		{
			name:        "LZW filter is skipped with its name",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /LZWDecode", stream: []byte("x")},
			wantSkipped: "unsupported filter LZWDecode",
		},
		{
			name:        "filter chain is skipped",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter [/ASCIIHexDecode /FlateDecode]", stream: []byte("x")},
			wantSkipped: "filter chain ASCIIHexDecode+FlateDecode",
		},
		{
			name:        "16 bit samples are skipped",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 16 /Filter /FlateDecode", stream: zlibBytes(t, []byte{0, 1})},
			wantSkipped: "unsupported bits per component 16",
		},
		{
			name:        "inverted JPEG Decode array is skipped",
			img:         pdfObj{dict: "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Decode [1 0 1 0 1 0] /Filter /DCTDecode", stream: []byte("jpeg")},
			wantSkipped: "non-default Decode array",
		},
		{
			name:        "corrupt Flate data is skipped, not an error",
			img:         pdfObj{dict: gray, stream: []byte("garbage, not zlib")},
			wantSkipped: "corrupt image data",
		},
		{
			name:       "SMask is reported and not applied",
			img:        pdfObj{dict: gray + " /SMask 5 0 R", stream: zlibBytes(t, []byte{10, 20})},
			extra:      []pdfObj{{dict: "/Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", stream: zlibBytes(t, []byte{255, 0})}},
			wantFormat: "png",
			wantData:   true,
			wantSMask:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := pagesDoc([]string{xobjectRes("/Im1 4 0 R")}, append([]pdfObj{tc.img}, tc.extra...)...)
			got := onlyImage(t, mustExtract(t, data, nil))
			if got.Format != tc.wantFormat {
				t.Errorf("Format = %q, want %q", got.Format, tc.wantFormat)
			}
			if tc.wantSkipped == "" && got.Skipped != "" {
				t.Errorf("Skipped = %q, want none", got.Skipped)
			}
			if tc.wantSkipped != "" && !strings.HasPrefix(got.Skipped, tc.wantSkipped) {
				t.Errorf("Skipped = %q, want prefix %q", got.Skipped, tc.wantSkipped)
			}
			if (got.Data != nil) != tc.wantData {
				t.Errorf("Data present = %v, want %v", got.Data != nil, tc.wantData)
			}
			if got.HasSMask != tc.wantSMask {
				t.Errorf("HasSMask = %v, want %v", got.HasSMask, tc.wantSMask)
			}
			if tc.wantData && tc.wantFormat == "jbig2" && string(got.Data) != "jbig2 bytes" {
				t.Errorf("JBIG2 Data is not the original stream: %q", got.Data)
			}
		})
	}
}

// TestExtractImageLimit checks the image count cap. 200 images are allowed.
// The 201st is an error.
func TestExtractImageLimit(t *testing.T) {
	build := func(n int) []byte {
		var names []string
		var extra []pdfObj
		for i := 0; i < n; i++ {
			names = append(names, fmt.Sprintf("/I%d %d 0 R", i, 4+i))
			extra = append(extra, flateRGB(t, 1, 1, []byte{1, 2, 3}, ""))
		}
		return pagesDoc([]string{xobjectRes(names...)}, extra...)
	}
	if imgs := mustExtract(t, build(maxImages), nil); len(imgs) != maxImages {
		t.Errorf("got %d images, want %d", len(imgs), maxImages)
	}
	if _, err := ExtractImages(build(maxImages+1), nil); err == nil {
		t.Errorf("%d images: want an error", maxImages+1)
	}
}

func TestExtractNotPDFIsError(t *testing.T) {
	if _, err := ExtractImages([]byte("this is not a pdf file"), nil); err == nil {
		t.Error("bad input: want an error")
	}
	if _, err := ExtractImages(nil, nil); err == nil {
		t.Error("empty input: want an error")
	}
}

// TestNoPanicOnPrefixes cuts a PDF with several kinds of image at many
// lengths. Each cut must return without a panic.
func TestExtractNoPanicOnPrefixes(t *testing.T) {
	good := pagesDoc(
		[]string{xobjectRes("/Im1 4 0 R", "/Im2 5 0 R")},
		flateRGB(t, 2, 2, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, "/DecodeParms << /Predictor 2 >>"),
		pdfObj{dict: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", stream: []byte("abcdefgh")},
	)
	step := len(good) / 150
	if step < 1 {
		step = 1
	}
	for n := 0; n <= len(good); n += step {
		_, _ = ExtractImages(good[:n], nil)
	}
}

// mustExtract runs ExtractImages or fails the test.
func mustExtract(t *testing.T, data []byte, pages []int) []Image {
	t.Helper()
	imgs, err := ExtractImages(data, pages)
	if err != nil {
		t.Fatalf("ExtractImages: %v", err)
	}
	return imgs
}

func TestExtractInputOverLimitIsError(t *testing.T) {
	if _, err := ExtractImages(make([]byte, maxImageInputBytes+1), nil); err == nil {
		t.Error("input over the limit: want an error")
	}
}

// TestExtractFlateSizeClaimMismatchIsSkipped checks Flate images whose stream
// does not hold exactly the bytes the dictionary claims. The first claims
// 8000 by 8000 RGB but inflates to 3 bytes. The second claims 1 pixel but
// inflates to 6 bytes. Both are skipped, and the page's other image is kept.
func TestExtractFlateSizeClaimMismatchIsSkipped(t *testing.T) {
	short := flateRGB(t, 8000, 8000, []byte{1, 2, 3}, "")
	long := flateRGB(t, 1, 1, []byte{1, 2, 3, 4, 5, 6}, "")
	ok := flateRGB(t, 1, 1, []byte{1, 2, 3}, "")
	data := pagesDoc([]string{xobjectRes("/Short 4 0 R", "/Long 5 0 R", "/Ok 6 0 R")}, short, long, ok)

	imgs := mustExtract(t, data, nil)
	if len(imgs) != 3 {
		t.Fatalf("got %d images, want 3: %+v", len(imgs), imgs)
	}
	byName := map[string]Image{}
	for _, im := range imgs {
		byName[im.Name] = im
	}
	for _, name := range []string{"Short", "Long"} {
		if im := byName[name]; !strings.HasPrefix(im.Skipped, "corrupt image data") || im.Data != nil {
			t.Errorf("%s: %+v", name, im)
		}
	}
	if im := byName["Ok"]; im.Format != "png" || im.Skipped != "" {
		t.Errorf("Ok: %+v", im)
	}
}

// TestExtractHugeDimensionsAreSkipped checks widths that overflow the row
// arithmetic, and a float width that is not a usable integer. Each image is
// skipped alone. The page's other image is still returned.
func TestExtractHugeDimensionsAreSkipped(t *testing.T) {
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
			stream: zlibBytes(t, []byte{1, 2, 3}),
		}
		data := pagesDoc([]string{xobjectRes("/Big 4 0 R", "/Ok 5 0 R")}, big, flateRGB(t, 1, 1, []byte{1, 2, 3}, ""))

		imgs := mustExtract(t, data, nil)
		if len(imgs) != 2 {
			t.Fatalf("width %s: got %d images, want 2: %+v", c.width, len(imgs), imgs)
		}
		byName := map[string]Image{}
		for _, im := range imgs {
			byName[im.Name] = im
		}
		if im := byName["Big"]; im.Skipped != c.wantReason || im.Data != nil {
			t.Errorf("width %s: got %+v, want skipped %q", c.width, im, c.wantReason)
		}
		if im := byName["Ok"]; im.Format != "png" || im.Skipped != "" {
			t.Errorf("width %s: Ok image: %+v", c.width, im)
		}
	}
}

// TestExtractFormDAGIsWalkedOnce builds 12 forms, where each form refers to
// the next one three times. Walked as a tree, that is 3^11 visits. Each
// form must be walked once, so the call is fast and the image appears once.
func TestExtractFormDAGIsWalkedOnce(t *testing.T) {
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
	extra = append(extra, flateRGB(t, 1, 1, []byte{1, 2, 3}, ""))
	data := pagesDoc([]string{xobjectRes("/Fm 4 0 R")}, extra...)

	start := time.Now()
	got := onlyImage(t, mustExtract(t, data, nil))
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("form DAG took %v, want under 2s", d)
	}
	if got.Name != "Im" || got.Format != "png" {
		t.Errorf("image: %+v", got)
	}
}

// TestExtractFormSharedByTwoPagesReportsBoth gives two pages the same form
// (object 5), which holds one image (object 6). Each page must report the
// image under its own number, as it would when asked for alone.
func TestExtractFormSharedByTwoPagesReportsBoth(t *testing.T) {
	form := pdfObj{
		dict:   "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources " + xobjectRes("/Im 6 0 R"),
		stream: []byte(""),
	}
	data := pagesDoc([]string{xobjectRes("/Fm 5 0 R"), xobjectRes("/Fm 5 0 R")},
		form, flateRGB(t, 1, 1, []byte{1, 2, 3}, ""))

	got := mustExtract(t, data, []int{1, 2})
	if len(got) != 2 {
		t.Fatalf("got %d images, want 2: %+v", len(got), got)
	}
	for i, page := range []int{1, 2} {
		if got[i].Page != page || got[i].Name != "Im" || got[i].Format != "png" {
			t.Errorf("image %d: %+v, want page %d, name Im, format png", i, got[i], page)
		}
	}
}
