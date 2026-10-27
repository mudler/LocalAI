package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// watermarkDesc is the pdfcpu description of a diagonal stamp: lower left
// to upper right, 48 pt, 30 % opacity, drawn on top of the page.
const watermarkDesc = "diagonal:1, points:48, opacity:0.3, scalefactor:1.0 rel"

// Merge joins PDFs in the given order into one PDF.
func Merge(parts ...[]byte) ([]byte, error) {
	if len(parts) == 0 {
		return nil, errors.New("pdf: merge: no input")
	}
	for i, p := range parts {
		if err := checkInput(p); err != nil {
			return nil, fmt.Errorf("pdf: merge: part %d: %w", i+1, err)
		}
	}
	return writeTo("merge", func(w io.Writer) error {
		rsc := make([]io.ReadSeeker, len(parts))
		for i, p := range parts {
			rsc[i] = bytes.NewReader(p)
		}
		return api.MergeRaw(rsc, w, false, newConf())
	})
}

// Split returns one PDF for each page, in page order. Each part is a valid
// one-page PDF. It runs one extraction per page, so a long document takes a
// while.
func Split(data []byte) ([][]byte, error) {
	if err := checkInput(data); err != nil {
		return nil, err
	}
	n, err := Pages(data)
	if err != nil {
		return nil, err
	}
	parts := make([][]byte, 0, n)
	for page := 1; page <= n; page++ {
		part, err := writeTo("split", func(w io.Writer) error {
			return api.Trim(bytes.NewReader(data), w, []string{strconv.Itoa(page)}, newConf())
		})
		if err != nil {
			return nil, fmt.Errorf("pdf: split: page %d: %w", page, err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// Rotate turns the given 1-based pages clockwise. degrees must be 90, 180 or
// 270. An empty pages list means every page.
func Rotate(data []byte, pages []int, degrees int) ([]byte, error) {
	if err := checkInput(data); err != nil {
		return nil, err
	}
	switch degrees {
	case 90, 180, 270:
	default:
		return nil, fmt.Errorf("pdf: rotate: degrees %d must be 90, 180 or 270", degrees)
	}
	n, err := Pages(data)
	if err != nil {
		return nil, err
	}
	var sel []string
	for _, p := range pages {
		if err := checkPage(p, n); err != nil {
			return nil, fmt.Errorf("pdf: rotate: %w", err)
		}
		sel = append(sel, strconv.Itoa(p))
	}
	return writeTo("rotate", func(w io.Writer) error {
		return api.Rotate(bytes.NewReader(data), w, degrees, sel, newConf())
	})
}

// Watermark stamps text diagonally on every page. The text must be Latin-1,
// because the stamp uses the built-in Helvetica font.
func Watermark(data []byte, text string) ([]byte, error) {
	if err := checkInput(data); err != nil {
		return nil, err
	}
	if text == "" {
		return nil, errors.New("pdf: watermark: text is empty")
	}
	if err := checkLatin1(text); err != nil {
		return nil, fmt.Errorf("pdf: watermark: %w", err)
	}
	out, err := writeTo("watermark", func(w io.Writer) error {
		wm, err := api.TextWatermark(text, watermarkDesc, true, false, types.POINTS)
		if err != nil {
			return err
		}
		return api.AddWatermarks(bytes.NewReader(data), w, nil, wm, newConf())
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
