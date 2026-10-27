// Package officeread reads an .xlsx, .docx or .pptx file and returns its
// content as plain structured text. It is the read side of the xlsx, docx
// and deck/pptx writers. It uses only the standard library.
//
// The format comes from the content types part. An xlsx file gives one
// Section per sheet, a docx file gives one Section with the body in order,
// and a pptx file gives one Section per slide in presentation order. Formula
// cells give their cached value, never the formula. Nothing is rendered and
// nothing is executed: the input is untrusted, so every entry name is
// checked and the size of what is unpacked is limited.
package officeread

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Document is the content of an office file.
type Document struct {
	// Format is "xlsx", "docx" or "pptx".
	Format string
	Parts  []Section
}

// Section is one sheet, one slide or the body of a document.
type Section struct {
	// Title is the sheet name, "Slide N" or "Document".
	Title string
	// Text is the plain text of the part.
	Text string
	// Tables holds the grid of an xlsx sheet, or the tables of a docx body.
	// Each table is a list of rows, each row a list of cell texts.
	Tables [][][]string
}

// ErrTooLarge is wrapped by the error of a package with an entry over 64 MiB
// or over 256 MiB in total.
var ErrTooLarge = errors.New("officeread: package too large")

// Limits on what is unpacked. Tests lower them.
var (
	maxEntry int64 = 64 << 20
	maxTotal int64 = 256 << 20
)

// maxGridCells bounds the grid built for one sheet, so a cell reference far
// out in a sparse sheet cannot use all the memory.
const maxGridCells = 4 << 20

const (
	contentTypesPart = "[Content_Types].xml"
	nsRels           = "/relationships"
)

// Read parses data as an .xlsx, .docx or .pptx file. It returns an error
// for a file that is not a zip, is not a recognised format, lacks a required
// part, has a part that is not well-formed XML, has an unsafe entry name or
// is too large. It never panics.
func Read(data []byte) (doc Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = Document{}, fmt.Errorf("officeread: internal error: %v", r)
		}
	}()
	p, err := unzip(data)
	if err != nil {
		return Document{}, err
	}
	format, main, err := detect(p)
	if err != nil {
		return Document{}, err
	}
	switch format {
	case "xlsx":
		doc.Parts, err = readXLSX(p, main)
	case "docx":
		doc.Parts, err = readDOCX(p, main)
	default:
		doc.Parts, err = readPPTX(p, main)
	}
	if err != nil {
		return Document{}, err
	}
	doc.Format = format
	return doc, nil
}

type parts map[string][]byte

func unzip(data []byte) (parts, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("officeread: not a zip file: %w", err)
	}
	out := parts{}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || hasDotDot(name) {
			return nil, fmt.Errorf("officeread: unsafe entry name %q", name)
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("officeread: duplicate entry %q", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("officeread: cannot open %q: %w", name, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("officeread: cannot read %q: %w", name, err)
		}
		if int64(len(b)) > maxEntry {
			return nil, fmt.Errorf("officeread: entry %q is larger than %d bytes: %w", name, maxEntry, ErrTooLarge)
		}
		total += int64(len(b))
		if total > maxTotal {
			return nil, fmt.Errorf("officeread: package is larger than %d bytes: %w", maxTotal, ErrTooLarge)
		}
		out[name] = b
	}
	return out, nil
}

func hasDotDot(name string) bool {
	for _, s := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if s == ".." {
			return true
		}
	}
	return false
}

// detect finds the main part from the content types.
func detect(p parts) (format, main string, err error) {
	raw, ok := p[contentTypesPart]
	if !ok {
		return "", "", errors.New("officeread: " + contentTypesPart + " is missing")
	}
	err = walk(raw, func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != "Override" {
			return
		}
		ct, _ := attr(se, "ContentType")
		pn, _ := attr(se, "PartName")
		switch {
		case strings.Contains(ct, "spreadsheetml.sheet.main"):
			format, main = "xlsx", strings.TrimPrefix(pn, "/")
		case strings.Contains(ct, "wordprocessingml.document.main"):
			format, main = "docx", strings.TrimPrefix(pn, "/")
		case strings.Contains(ct, "presentationml.presentation.main"):
			format, main = "pptx", strings.TrimPrefix(pn, "/")
		}
	})
	if err != nil {
		return "", "", fmt.Errorf("officeread: %s: %w", contentTypesPart, err)
	}
	if format == "" {
		return "", "", errors.New("officeread: format not recognised: no workbook, document or presentation part")
	}
	if _, ok := p[main]; !ok {
		return "", "", fmt.Errorf("officeread: required part %s is missing", main)
	}
	return format, main, nil
}

// walk passes every token of raw to fn and reports XML that is not well formed.
func walk(raw []byte, fn func(xml.Token)) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		t, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("not well-formed XML: %w", err)
		}
		fn(t)
	}
}

// walkPart is walk for a named part, with the name in the error.
func walkPart(p parts, name string, fn func(xml.Token)) error {
	raw, ok := p[name]
	if !ok {
		return fmt.Errorf("officeread: required part %s is missing", name)
	}
	if err := walk(raw, fn); err != nil {
		return fmt.Errorf("officeread: %s: %w", name, err)
	}
	return nil
}

func attr(se xml.StartElement, local string) (string, bool) {
	for _, a := range se.Attr {
		if a.Name.Local == local && a.Name.Space == "" {
			return a.Value, true
		}
	}
	return "", false
}

// relID returns the r:id attribute, which lives in the relationships namespace.
func relID(se xml.StartElement) string {
	for _, a := range se.Attr {
		if a.Name.Local == "id" && strings.HasSuffix(a.Name.Space, nsRels) {
			return a.Value
		}
	}
	return ""
}

// rel is one relationship with its target resolved to a package part name.
type rel struct{ typ, target string }

// relsOf reads the relationships of a part. A part without a rels file has none.
func relsOf(p parts, part string) (map[string]rel, error) {
	name := path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
	out := map[string]rel{}
	if _, ok := p[name]; !ok {
		return out, nil
	}
	err := walkPart(p, name, func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			return
		}
		if mode, _ := attr(se, "TargetMode"); strings.EqualFold(mode, "External") {
			return
		}
		id, _ := attr(se, "Id")
		typ, _ := attr(se, "Type")
		target, _ := attr(se, "Target")
		if strings.HasPrefix(target, "/") {
			target = path.Clean(strings.TrimPrefix(target, "/"))
		} else {
			target = path.Join(path.Dir(part), target)
		}
		out[id] = rel{typ: typ, target: target}
	})
	return out, err
}
