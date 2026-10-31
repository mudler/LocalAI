// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"errors"
	"github.com/mudler/LocalAI/addons/internal/pdfread"
	"github.com/mudler/LocalAI/addons/office/internal/docxedit"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
	"github.com/mudler/LocalAI/addons/office/internal/pptxedit"
)

type pathArgs struct {
	base
	Path string `json:"path"`
}

// budget counts the bytes a read answer may still use.
type budget struct{ left int }

// take reports whether n more bytes fit, and uses them when they do.
func (b *budget) take(n int) bool {
	if n > b.left {
		b.left = 0
		return false
	}
	b.left -= n
	return true
}

func (w *work) readDocx(raw json.RawMessage) (any, error) {
	var a pathArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	data, _, err := w.fetch(a.Path, ".docx")
	if err != nil {
		return nil, err
	}
	doc, err := docxedit.Open(data)
	if err != nil {
		return nil, refuse("the file is not a valid Word document")
	}
	type para struct {
		Index int    `json:"index"`
		Style string `json:"style,omitempty"`
		Text  string `json:"text"`
	}
	b := &budget{left: maxTextBytes}
	all := doc.Paragraphs()
	paras := make([]para, 0, len(all))
	truncated := false
	for _, p := range all {
		if !b.take(len(p.Text) + 32) {
			truncated = true
			break
		}
		paras = append(paras, para{Index: p.Index, Style: p.Style, Text: p.Text})
	}
	out := map[string]any{"ok": true, "paragraphs": paras, "total": len(all)}
	// Tables are not addressable by an edit; they are shown so the document
	// can be understood.
	if full, err := officeread.Read(data); err == nil && len(full.Parts) > 0 && !truncated {
		var tables [][][]string
		for _, t := range full.Parts[0].Tables {
			rows := t
			if len(rows) > maxRows {
				rows, truncated = rows[:maxRows], true
			}
			size := 0
			for _, r := range rows {
				for _, c := range r {
					size += len(c) + 4
				}
			}
			if !b.take(size) {
				truncated = true
				break
			}
			tables = append(tables, rows)
		}
		out["tables"] = tables
	}
	out["truncated"] = truncated
	return out, nil
}

func (w *work) readXlsx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Sheet string `json:"sheet"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	data, _, err := w.fetch(a.Path, ".xlsx")
	if err != nil {
		return nil, err
	}
	doc, err := officeread.Read(data)
	if err != nil || doc.Format != "xlsx" {
		return nil, refuse("the file is not a valid Excel workbook")
	}
	type sheet struct {
		Name      string     `json:"name"`
		Rows      [][]string `json:"rows"`
		RowCount  int        `json:"rowCount"`
		ColCount  int        `json:"colCount"`
		Truncated bool       `json:"truncated,omitempty"`
	}
	b := &budget{left: maxTextBytes}
	var sheets []sheet
	var names []string
	truncated := false
	for _, part := range doc.Parts {
		names = append(names, part.Title)
		if a.Sheet != "" && part.Title != a.Sheet {
			continue
		}
		var grid [][]string
		if len(part.Tables) > 0 {
			grid = part.Tables[0]
		}
		s := sheet{Name: part.Title, RowCount: len(grid)}
		for _, r := range grid {
			s.ColCount = max(s.ColCount, len(r))
		}
		for i, r := range grid {
			if i >= maxRows {
				s.Truncated = true
				break
			}
			if len(r) > maxCols {
				r, s.Truncated = r[:maxCols], true
			}
			size := 2
			for _, c := range r {
				size += len(c) + 4
			}
			if !b.take(size) {
				s.Truncated = true
				break
			}
			s.Rows = append(s.Rows, r)
		}
		truncated = truncated || s.Truncated
		sheets = append(sheets, s)
	}
	if a.Sheet != "" && len(sheets) == 0 {
		return nil, refuse("the workbook has no sheet named %q; its sheets are %v", a.Sheet, names)
	}
	return map[string]any{"ok": true, "sheets": sheets, "sheetNames": names, "truncated": truncated}, nil
}

func (w *work) readPptx(raw json.RawMessage) (any, error) {
	var a pathArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	data, _, err := w.fetch(a.Path, ".pptx")
	if err != nil {
		return nil, err
	}
	deck, err := pptxedit.Open(data)
	if err != nil {
		return nil, refuse("the file is not a valid PowerPoint deck")
	}
	type shape struct {
		ID   int      `json:"id"`
		Kind string   `json:"kind"`
		Text []string `json:"text"`
	}
	type slide struct {
		Index  int     `json:"index"`
		Title  string  `json:"title"`
		Shapes []shape `json:"shapes"`
	}
	b := &budget{left: maxTextBytes}
	var slides []slide
	truncated := false
	all := deck.Slides()
loop:
	for _, s := range all {
		sl := slide{Index: s.Index, Title: s.Title}
		shapes, err := deck.Shapes(s.Index)
		if err != nil {
			continue
		}
		for _, sh := range shapes {
			if len(sh.Text) == 0 {
				continue
			}
			size := 24
			for _, t := range sh.Text {
				size += len(t) + 4
			}
			if !b.take(size) {
				truncated = true
				slides = append(slides, sl)
				break loop
			}
			sl.Shapes = append(sl.Shapes, shape{ID: sh.ID, Kind: sh.Kind, Text: sh.Text})
		}
		slides = append(slides, sl)
	}
	return map[string]any{"ok": true, "slides": slides, "total": len(all), "truncated": truncated}, nil
}

func (w *work) readPdf(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		FirstPage int `json:"firstPage"`
		LastPage  int `json:"lastPage"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	data, _, err := w.fetch(a.Path, ".pdf")
	if err != nil {
		return nil, err
	}
	doc, err := pdfread.Open(data)
	if err != nil {
		if errors.Is(err, pdfread.ErrNotPDF) {
			return nil, refuse("the file is not a PDF")
		}
		return nil, refuse("the PDF cannot be read: it may be damaged or protected by a password")
	}
	defer func() { _ = doc.Close() }()
	pages := doc.Pages()
	first, last := a.FirstPage, a.LastPage
	if first <= 0 {
		first = 1
	}
	if last <= 0 || last > pages {
		last = pages
	}
	if first > last {
		return nil, refuse("the PDF has %d pages", pages)
	}
	if last-first+1 > maxPages {
		last = first + maxPages - 1
	}
	type page struct {
		Page int    `json:"page"`
		Text string `json:"text"`
	}
	b := &budget{left: maxTextBytes}
	var out []page
	truncated := last < pages && a.LastPage <= 0
	for p := first; p <= last; p++ {
		text, err := doc.Text(p)
		if err != nil {
			return nil, refuse("page %d cannot be read", p)
		}
		clipped, cut := clip(text, b.left)
		if cut || !b.take(len(clipped)+16) {
			if clipped != "" {
				out = append(out, page{Page: p, Text: clipped})
			}
			truncated = true
			break
		}
		out = append(out, page{Page: p, Text: clipped})
	}
	return map[string]any{"ok": true, "pages": pages, "text": out, "truncated": truncated}, nil
}
