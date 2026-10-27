package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Limits for ExtractImages. Input over maxImageInputBytes is refused. An
// extraction that would return more than maxImages images, or more than
// maxImageOutput bytes of Data, is an error. An image wider or higher than
// maxImageDim is skipped.
const (
	maxImageInputBytes = 256 << 20
	maxImages          = 200
	maxImageOutput     = 256 << 20
	maxFormDepth       = 16
	maxImageDim        = 65535
)

// Image is one image XObject found on a page. It is either returned in its
// original encoding or decoded and re-encoded as PNG. When Skipped is set,
// Data is nil and the caller should render the page instead.
//
// Format is one of:
//   - "jpeg": Data is the original JPEG stream (DCTDecode, DeviceRGB or DeviceGray).
//   - "jbig2": Data is the original JBIG2 stream, not decoded.
//   - "jpx": Data is the original JPEG 2000 stream, not decoded.
//   - "png": Data is a PNG that this package decoded from a Flate stream.
//
// HasSMask reports that the image has an /SMask. The soft mask is not
// applied: Data holds the colour samples only.
type Image struct {
	Page             int
	Name             string
	Format           string
	Width, Height    int
	BitsPerComponent int
	ColorSpace       string
	Data             []byte
	HasSMask         bool
	Skipped          string
}

// ExtractImages returns the image XObjects of the given pages. Pages are
// 1-based. An empty pages list selects every page. Resources are read
// through each page, including the resources of nested form XObjects.
// Results are in page order, then in resource-name order.
//
// The image bytes are read straight from the PDF streams, so pdfcpu's image
// helper, which fails on image masks and mishandles CMYK JPEGs, is not used.
// Images that cannot be returned as-is or decoded here get a Skipped reason.
// Such images have no Data, so the caller can render that page.
//
// The soft mask of an image is not applied. See Image.HasSMask.
//
// A form XObject is walked once per page, however many times that page refers
// to it. The set of walked forms is reset at the start of each page. A form
// used by two requested pages therefore reports its images under both pages,
// and each page gives the same result as when it is requested alone. The image
// count and output size limits apply to the whole call.
//
// Only in-memory readers are used. Nothing is written to disk.
func ExtractImages(data []byte, pages []int) (out []Image, err error) {
	if len(data) == 0 {
		return nil, errEmptyInput
	}
	if len(data) > maxImageInputBytes {
		return nil, fmt.Errorf("pdf: extract images: input is %d bytes, the limit is %d", len(data), maxImageInputBytes)
	}
	err = guard("extract images", func() error {
		ctx, err := api.ReadContext(bytes.NewReader(data), newConf())
		if err != nil {
			return fmt.Errorf("pdf: extract images: %w", err)
		}
		if err := ctx.EnsurePageCount(); err != nil {
			return fmt.Errorf("pdf: extract images: %w", err)
		}
		sel, err := selectPages(pages, ctx.PageCount)
		if err != nil {
			return err
		}
		x := &imageExtractor{ctx: ctx}
		for _, p := range sel {
			if err := x.page(p); err != nil {
				return fmt.Errorf("pdf: extract images: page %d: %w", p, err)
			}
		}
		out = x.out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// selectPages returns the sorted, de-duplicated pages to read. An empty
// request means every page of a document with n pages.
func selectPages(pages []int, n int) ([]int, error) {
	if len(pages) == 0 {
		sel := make([]int, n)
		for i := range sel {
			sel[i] = i + 1
		}
		return sel, nil
	}
	seen := make(map[int]bool, len(pages))
	var sel []int
	for _, p := range pages {
		if err := checkPage(p, n); err != nil {
			return nil, err
		}
		if !seen[p] {
			seen[p] = true
			sel = append(sel, p)
		}
	}
	sort.Ints(sel)
	return sel, nil
}

// imageExtractor holds the state of one ExtractImages call.
type imageExtractor struct {
	ctx   *model.Context
	out   []Image
	total int
	// visited holds the object numbers of the forms walked on the current
	// page. It is reset for each page. A form is walked at most once per
	// page, so cycles end and a form reached again on that page costs nothing.
	visited map[int]bool
}

// page walks the resources of one page, with a fresh set of walked forms.
func (x *imageExtractor) page(p int) error {
	x.visited = map[int]bool{}
	_, _, attrs, err := x.ctx.PageDict(p, false)
	if err != nil {
		return err
	}
	if attrs == nil || attrs.Resources == nil {
		return nil
	}
	return x.resources(attrs.Resources, p, 0)
}

// resources visits the XObjects of one resource dictionary. Images are
// added to the result. Forms are walked recursively, each one once per call.
func (x *imageExtractor) resources(res types.Dict, page int, depth int) error {
	xobjs, err := x.dict(res, "XObject")
	if err != nil || xobjs == nil {
		return err
	}
	names := make([]string, 0, len(xobjs))
	for name := range xobjs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		objNr := 0
		if ref, ok := xobjs[name].(types.IndirectRef); ok {
			objNr = ref.ObjectNumber.Value()
		}
		sd, _, err := x.ctx.DereferenceStreamDict(xobjs[name])
		if err != nil {
			return fmt.Errorf("xobject %s: %w", name, err)
		}
		if sd == nil {
			continue
		}
		subtype, _ := x.name(sd.Dict, "Subtype")
		switch subtype {
		case "Image":
			if err := x.addImage(page, name, sd); err != nil {
				return err
			}
		case "Form":
			if objNr != 0 {
				if x.visited[objNr] {
					continue // walked already, and its images were emitted then
				}
				x.visited[objNr] = true
			}
			if depth >= maxFormDepth {
				return fmt.Errorf("forms nested more than %d levels", maxFormDepth)
			}
			formRes, err := x.dict(sd.Dict, "Resources")
			if err != nil {
				return err
			}
			if formRes == nil {
				formRes = res // a form without its own resources uses the caller's
			}
			err = x.resources(formRes, page, depth+1)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// addImage decodes one image XObject and appends it to the result.
func (x *imageExtractor) addImage(page int, name string, sd *types.StreamDict) error {
	if len(x.out) >= maxImages {
		return fmt.Errorf("more than %d images", maxImages)
	}
	img := Image{Page: page, Name: name}
	if err := x.describe(sd, &img); err != nil {
		return err
	}
	if img.Data != nil {
		if x.total+len(img.Data) > maxImageOutput {
			return fmt.Errorf("image data is over the %d byte limit", maxImageOutput)
		}
		x.total += len(img.Data)
	}
	x.out = append(x.out, img)
	return nil
}

// describe fills img from the stream dictionary. Its fields are set, and
// either Data and Format are set or Skipped is set.
func (x *imageExtractor) describe(sd *types.StreamDict, img *Image) error {
	d := sd.Dict
	img.Width, _ = x.intEntry(d, "Width")
	img.Height, _ = x.intEntry(d, "Height")
	img.BitsPerComponent, _ = x.intEntry(d, "BitsPerComponent")
	cs, comps, err := x.colorSpace(d)
	if err != nil {
		return err
	}
	img.ColorSpace = cs
	if _, ok := d.Find("SMask"); ok {
		img.HasSMask = true
	}

	if mask, err := x.boolEntry(d, "ImageMask"); err != nil {
		return err
	} else if mask {
		img.Skipped = "image mask"
		return nil
	}

	filters, parms, err := x.filters(d)
	if err != nil {
		return err
	}
	if len(filters) > 1 {
		img.Skipped = "filter chain " + strings.Join(filters, "+")
		return nil
	}
	filter := ""
	if len(filters) == 1 {
		filter = filters[0]
	}

	switch filter {
	case "DCTDecode":
		switch {
		case cs == "DeviceCMYK":
			img.Skipped = "CMYK JPEG needs conversion"
			return nil
		case cs != "DeviceRGB" && cs != "DeviceGray":
			img.Skipped = unsupportedCS(cs)
			return nil
		}
		return x.passthrough(sd, img, "jpeg")
	case "JBIG2Decode":
		if _, ok := parms.Find("JBIG2Globals"); ok {
			img.Skipped = "JBIG2 needs globals"
			return nil
		}
		return x.passthrough(sd, img, "jbig2")
	case "JPXDecode":
		return x.passthrough(sd, img, "jpx")
	case "FlateDecode":
		return x.flate(sd, img, parms, comps, true)
	case "":
		return x.flate(sd, img, parms, comps, false)
	default:
		img.Skipped = "unsupported filter " + filter
		return nil
	}
}

// passthrough returns the original stream bytes for a format that is not
// decoded here. A Decode array other than the default would change the
// colours, so such an image is skipped.
func (x *imageExtractor) passthrough(sd *types.StreamDict, img *Image, format string) error {
	ok, err := x.defaultDecode(sd.Dict)
	if err != nil {
		return err
	}
	if !ok {
		img.Skipped = "non-default Decode array"
		return nil
	}
	if len(sd.Raw) == 0 {
		img.Skipped = "empty stream"
		return nil
	}
	img.Format = format
	img.Data = append([]byte(nil), sd.Raw...)
	return nil
}

// flate decodes a Flate stream of DeviceRGB or DeviceGray samples with 1 or
// 8 bits per component, applies a PNG predictor if there is one, and encodes
// the pixels as PNG. Other kinds are skipped with a reason.
func (x *imageExtractor) flate(sd *types.StreamDict, img *Image, parms types.Dict, comps int, compressed bool) error {
	if img.ColorSpace != "DeviceRGB" && img.ColorSpace != "DeviceGray" {
		img.Skipped = unsupportedCS(img.ColorSpace)
		return nil
	}
	if ok, err := x.defaultDecode(sd.Dict); err != nil {
		return err
	} else if !ok {
		img.Skipped = "non-default Decode array"
		return nil
	}
	bpc := img.BitsPerComponent
	if bpc != 1 && bpc != 8 {
		img.Skipped = fmt.Sprintf("unsupported bits per component %d", bpc)
		return nil
	}
	if img.Width <= 0 || img.Height <= 0 {
		img.Skipped = "missing image size"
		return nil
	}
	if img.Width > maxImageDim || img.Height > maxImageDim {
		img.Skipped = "image dimensions too large"
		return nil
	}
	// Width is at most maxImageDim, so this product cannot overflow. The
	// total is compared by division, which cannot overflow either.
	rowBytes := (img.Width*comps*bpc + 7) / 8
	if rowBytes > maxImageOutput/img.Height {
		img.Skipped = "image dimensions too large"
		return nil
	}

	predictor, _ := x.intEntry(parms, "Predictor")
	usePNG := false
	switch {
	case predictor <= 1:
	case predictor >= 10:
		usePNG = true
		colors, ok := x.intEntry(parms, "Colors")
		if !ok {
			colors = comps
		}
		pbpc, ok := x.intEntry(parms, "BitsPerComponent")
		if !ok {
			pbpc = bpc
		}
		columns, ok := x.intEntry(parms, "Columns")
		if !ok {
			columns = img.Width
		}
		if colors != comps || pbpc != bpc || columns != img.Width {
			img.Skipped = "predictor parameters do not match the image"
			return nil
		}
	default:
		img.Skipped = fmt.Sprintf("predictor %d", predictor)
		return nil
	}

	stride := rowBytes
	if usePNG {
		stride++ // each row starts with its filter type byte
	}
	src, err := x.samples(sd, compressed, stride*img.Height)
	if err != nil {
		img.Skipped = "corrupt image data: " + err.Error()
		return nil
	}

	samples := src
	if usePNG {
		samples, err = unfilter(src, rowBytes, img.Height, bytesPerPixel(comps, bpc))
		if err != nil {
			img.Skipped = "corrupt image data: " + err.Error()
			return nil
		}
	}

	pix, err := toImage(samples, img.Width, img.Height, comps, bpc)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, pix); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	img.Format = "png"
	img.Data = buf.Bytes()
	return nil
}

// samples returns the first n bytes of the stream, inflated when the stream
// is Flate-compressed. It fails unless the stream holds exactly n bytes.
// The buffer grows with the inflated output, not with n, so a small stream
// that claims a large image costs little memory.
func (x *imageExtractor) samples(sd *types.StreamDict, compressed bool, n int) ([]byte, error) {
	if !compressed {
		if len(sd.Raw) < n {
			return nil, fmt.Errorf("stream has %d bytes, need %d", len(sd.Raw), n)
		}
		return sd.Raw[:n], nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(sd.Raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	// Read one byte past n, so output longer than n is seen without
	// reading the rest of the stream.
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(zr, int64(n)+1)); err != nil {
		return nil, err
	}
	switch {
	case buf.Len() > n:
		return nil, fmt.Errorf("stream inflates to more than %d bytes", n)
	case buf.Len() < n:
		return nil, fmt.Errorf("stream inflates to %d bytes, need %d", buf.Len(), n)
	}
	return buf.Bytes(), nil
}

// bytesPerPixel is the byte distance the PNG predictor uses to find the
// left neighbour. It is at least 1.
func bytesPerPixel(comps, bpc int) int {
	if n := comps * bpc / 8; n > 1 {
		return n
	}
	return 1
}

// unfilter reverses the PNG row filters (types 0 to 4). src holds one filter
// byte and rowBytes bytes per row.
func unfilter(src []byte, rowBytes, height, bpp int) ([]byte, error) {
	out := make([]byte, rowBytes*height)
	for y := 0; y < height; y++ {
		ft := src[y*(rowBytes+1)]
		cur := src[y*(rowBytes+1)+1 : (y+1)*(rowBytes+1)]
		row := out[y*rowBytes : (y+1)*rowBytes]
		var prev []byte
		if y > 0 {
			prev = out[(y-1)*rowBytes : y*rowBytes]
		}
		for i := 0; i < rowBytes; i++ {
			var left, up, upLeft byte
			if i >= bpp {
				left = row[i-bpp]
			}
			if prev != nil {
				up = prev[i]
				if i >= bpp {
					upLeft = prev[i-bpp]
				}
			}
			switch ft {
			case 0:
				row[i] = cur[i]
			case 1:
				row[i] = cur[i] + left
			case 2:
				row[i] = cur[i] + up
			case 3:
				row[i] = cur[i] + byte((int(left)+int(up))/2)
			case 4:
				row[i] = cur[i] + paeth(left, up, upLeft)
			default:
				return nil, fmt.Errorf("predictor filter type %d", ft)
			}
		}
	}
	return out, nil
}

// paeth is the PNG Paeth predictor.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	default:
		return c
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// toImage packs unfiltered samples into an image. DeviceGray becomes
// image.Gray, and DeviceRGB becomes an opaque image.RGBA. A 1-bit sample is
// scaled to 0 or 255.
func toImage(samples []byte, w, h, comps, bpc int) (image.Image, error) {
	rowBytes := (w*comps*bpc + 7) / 8
	sample := func(row []byte, i int) byte {
		if bpc == 8 {
			return row[i]
		}
		v := (row[i/8] >> (7 - uint(i%8))) & 1
		return v * 255
	}
	if comps == 1 {
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			row := samples[y*rowBytes : (y+1)*rowBytes]
			for x := 0; x < w; x++ {
				g.Pix[y*g.Stride+x] = sample(row, x)
			}
		}
		return g, nil
	}
	if comps != 3 {
		return nil, fmt.Errorf("pdf: extract images: %d components", comps)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := samples[y*rowBytes : (y+1)*rowBytes]
		for x := 0; x < w; x++ {
			o := y*rgba.Stride + 4*x
			rgba.Pix[o] = sample(row, 3*x)
			rgba.Pix[o+1] = sample(row, 3*x+1)
			rgba.Pix[o+2] = sample(row, 3*x+2)
			rgba.Pix[o+3] = 0xff
		}
	}
	return rgba, nil
}

// unsupportedCS is the Skipped reason for a colour space that is not handled.
func unsupportedCS(cs string) string {
	if cs == "" {
		return "missing colour space"
	}
	return "unsupported colour space " + cs
}

// get dereferences the value of key in d. It returns nil when the key is
// absent.
func (x *imageExtractor) get(d types.Dict, key string) (types.Object, error) {
	o, found := d.Find(key)
	if !found || o == nil {
		return nil, nil
	}
	return x.ctx.Dereference(o)
}

// dict returns the dictionary value of key, or nil when it is absent or not
// a dictionary.
func (x *imageExtractor) dict(d types.Dict, key string) (types.Dict, error) {
	o, err := x.get(d, key)
	if err != nil {
		return nil, err
	}
	dd, _ := o.(types.Dict)
	return dd, nil
}

// name returns the name value of key.
func (x *imageExtractor) name(d types.Dict, key string) (string, error) {
	o, err := x.get(d, key)
	if err != nil {
		return "", err
	}
	n, _ := o.(types.Name)
	return string(n), nil
}

// intEntry returns the integer value of key.
func (x *imageExtractor) intEntry(d types.Dict, key string) (int, bool) {
	o, err := x.get(d, key)
	if err != nil {
		return 0, false
	}
	switch v := o.(type) {
	case types.Integer:
		return v.Value(), true
	case types.Float:
		// A float that is not finite or does not fit in int32 is not a
		// usable integer. It reads as absent, so the caller skips the image.
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) || f < math.MinInt32 || f > math.MaxInt32 {
			return 0, false
		}
		return int(f), true
	}
	return 0, false
}

// boolEntry returns the boolean value of key. An absent key is false.
func (x *imageExtractor) boolEntry(d types.Dict, key string) (bool, error) {
	o, err := x.get(d, key)
	if err != nil {
		return false, err
	}
	b, _ := o.(types.Boolean)
	return bool(b), nil
}

// colorSpace returns the name of the colour space and its component count.
// The name is the first name of an array colour space, such as ICCBased. The
// count is 0 for a colour space this package does not decode.
func (x *imageExtractor) colorSpace(d types.Dict) (string, int, error) {
	o, err := x.get(d, "ColorSpace")
	if err != nil {
		return "", 0, err
	}
	var cs string
	switch v := o.(type) {
	case types.Name:
		cs = string(v)
	case types.Array:
		if len(v) > 0 {
			first, err := x.ctx.Dereference(v[0])
			if err != nil {
				return "", 0, err
			}
			if n, ok := first.(types.Name); ok {
				cs = string(n)
			}
		}
	case nil:
	default:
		cs = fmt.Sprintf("%T", o)
	}
	switch cs {
	case "DeviceRGB":
		return cs, 3, nil
	case "DeviceGray":
		return cs, 1, nil
	case "DeviceCMYK":
		return cs, 4, nil
	}
	return cs, 0, nil
}

// filters returns the filter names of a stream and the DecodeParms of the
// first filter. The parameters are nil when there are none.
func (x *imageExtractor) filters(d types.Dict) ([]string, types.Dict, error) {
	o, err := x.get(d, "Filter")
	if err != nil {
		return nil, nil, err
	}
	var names []string
	switch v := o.(type) {
	case types.Name:
		names = []string{string(v)}
	case types.Array:
		for _, e := range v {
			eo, err := x.ctx.Dereference(e)
			if err != nil {
				return nil, nil, err
			}
			if n, ok := eo.(types.Name); ok {
				names = append(names, string(n))
			}
		}
	}

	po, err := x.get(d, "DecodeParms")
	if err != nil {
		return nil, nil, err
	}
	switch v := po.(type) {
	case types.Dict:
		return names, v, nil
	case types.Array:
		if len(v) > 0 {
			first, err := x.ctx.Dereference(v[0])
			if err != nil {
				return nil, nil, err
			}
			if pd, ok := first.(types.Dict); ok {
				return names, pd, nil
			}
		}
	}
	return names, nil, nil
}

// defaultDecode reports whether the image has no Decode array, or the
// identity one ([0 1] for each component). Any other Decode array inverts or
// remaps the samples, which this package does not apply.
func (x *imageExtractor) defaultDecode(d types.Dict) (bool, error) {
	o, err := x.get(d, "Decode")
	if err != nil {
		return false, err
	}
	arr, ok := o.(types.Array)
	if !ok {
		return true, nil
	}
	for i := 0; i+1 < len(arr); i += 2 {
		lo, err := x.ctx.DereferenceNumber(arr[i])
		if err != nil {
			return false, nil
		}
		hi, err := x.ctx.DereferenceNumber(arr[i+1])
		if err != nil {
			return false, nil
		}
		if lo != 0 || hi != 1 {
			return false, nil
		}
	}
	return true, nil
}
