// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

// A created file is saved as a new file (version zero). If the path is taken,
// the store keeps the old file and the new one is saved next to it.

func (w *work) createDocx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title  string `json:"title"`
		Blocks []struct {
			Kind  string   `json:"kind"`
			Level int      `json:"level"`
			Text  string   `json:"text"`
			Items []string `json:"items"`
		} `json:"blocks"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".docx"); err != nil {
		return nil, err
	}
	if len(a.Blocks) == 0 || len(a.Blocks) > 2000 {
		return nil, refuse("blocks must hold 1 to 2000 entries")
	}
	d := docx.Document{Title: a.Title}
	for i, b := range a.Blocks {
		switch b.Kind {
		case "heading":
			level := b.Level
			if level == 0 {
				level = 1
			}
			d.Blocks = append(d.Blocks, docx.Heading{Level: level, Text: b.Text})
		case "paragraph":
			d.Blocks = append(d.Blocks, docx.Paragraph{Text: b.Text})
		case "bullets":
			d.Blocks = append(d.Blocks, docx.Bullets{Items: b.Items})
		default:
			return nil, refuse("block %d: kind must be heading, paragraph or bullets", i+1)
		}
	}
	var buf bytes.Buffer
	if err := docx.Write(&buf, d); err != nil {
		return nil, refuse("the document cannot be written: %v", err)
	}
	s, err := w.store(a.Path, ".docx", buf.Bytes(), 0)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// cellRef is the A1 name of a zero-based row and column.
func cellRef(row, col int) string {
	name := ""
	for c := col + 1; c > 0; c = (c - 1) / 26 {
		name = string(rune('A'+(c-1)%26)) + name
	}
	return name + strconv.Itoa(row+1)
}

// cellOf reads a text the model wrote: "=..." is a formula, a number is a
// number, anything else is text.
func cellOf(ref, s string) (xlsx.Cell, bool) {
	switch {
	case s == "":
		return xlsx.Cell{}, false
	case strings.HasPrefix(s, "="):
		return xlsx.Cell{Ref: ref, Formula: s}, true
	}
	if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return xlsx.Cell{Ref: ref, Value: xlsx.Num(n)}, true
	}
	return xlsx.Cell{Ref: ref, Value: xlsx.Str(s)}, true
}

func (w *work) createXlsx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title  string `json:"title"`
		Sheets []struct {
			Name string     `json:"name"`
			Rows [][]string `json:"rows"`
		} `json:"sheets"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".xlsx"); err != nil {
		return nil, err
	}
	if len(a.Sheets) == 0 || len(a.Sheets) > 32 {
		return nil, refuse("sheets must hold 1 to 32 entries")
	}
	wb := xlsx.Workbook{Title: a.Title, HeaderStyle: xlsx.Style{Bold: true}}
	cells := 0
	for _, s := range a.Sheets {
		sh := xlsx.Sheet{Name: s.Name}
		if len(s.Rows) > 1 {
			sh.HeaderRow = 1
		}
		for r, row := range s.Rows {
			for c, text := range row {
				if cell, ok := cellOf(cellRef(r, c), text); ok {
					sh.Cells = append(sh.Cells, cell)
					cells++
				}
			}
		}
		if cells > 100000 {
			return nil, refuse("the workbook has more than 100000 cells")
		}
		wb.Sheets = append(wb.Sheets, sh)
	}
	var buf bytes.Buffer
	if err := xlsx.Write(&buf, wb); err != nil {
		return nil, refuse("the workbook cannot be written: %v", err)
	}
	return w.store(a.Path, ".xlsx", buf.Bytes(), 0)
}

func (w *work) createPptx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title  string `json:"title"`
		Slides []struct {
			Title   string   `json:"title"`
			Bullets []string `json:"bullets"`
		} `json:"slides"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".pptx"); err != nil {
		return nil, err
	}
	if len(a.Slides) == 0 || len(a.Slides) > 200 {
		return nil, refuse("slides must hold 1 to 200 entries")
	}
	var slides []deck.StudioSlide
	for i, s := range a.Slides {
		sl := deck.StudioSlide{
			ID: fmt.Sprintf("s%d", i+1), Title: s.Title, Layout: deck.LayoutText,
			Bullets: []deck.StudioBullet{}, Sources: []string{}, Status: deck.SlideOK,
		}
		if len(s.Bullets) == 0 {
			sl.Layout = deck.LayoutTitle
		}
		for _, b := range s.Bullets {
			sl.Bullets = append(sl.Bullets, deck.StudioBullet{Text: b, Evidence: []string{}})
		}
		slides = append(slides, sl)
	}
	var buf bytes.Buffer
	if err := pptx.Write(&buf, slides, pptx.Options{Title: a.Title, Style: deck.StylePaper}, nil); err != nil {
		return nil, refuse("the deck cannot be written: %v", err)
	}
	return w.store(a.Path, ".pptx", buf.Bytes(), 0)
}

func (w *work) createPdf(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title  string `json:"title"`
		Blocks []struct {
			Kind   string     `json:"kind"`
			Level  int        `json:"level"`
			Text   string     `json:"text"`
			Items  []string   `json:"items"`
			Header []string   `json:"header"`
			Rows   [][]string `json:"rows"`
		} `json:"blocks"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".pdf"); err != nil {
		return nil, err
	}
	if len(a.Blocks) == 0 || len(a.Blocks) > 2000 {
		return nil, refuse("blocks must hold 1 to 2000 entries")
	}
	blocks := make([]pdf.Block, 0, len(a.Blocks))
	for i, b := range a.Blocks {
		switch b.Kind {
		case "heading":
			level := b.Level
			if level == 0 {
				level = 1
			}
			blocks = append(blocks, pdf.Heading{Level: level, Text: b.Text})
		case "paragraph":
			blocks = append(blocks, pdf.Paragraph{Text: b.Text})
		case "bullets":
			blocks = append(blocks, pdf.Bullets{Items: b.Items})
		case "table":
			blocks = append(blocks, pdf.Table{Header: b.Header, Rows: b.Rows})
		default:
			return nil, refuse("block %d: kind must be heading, paragraph, bullets or table", i+1)
		}
	}
	data, err := pdf.Create(pdf.Spec{Title: a.Title, Pages: []pdf.Page{{Blocks: blocks}}})
	if err != nil {
		return nil, refuse("the PDF cannot be written: %v", err)
	}
	return w.store(a.Path, ".pdf", data, 0)
}
