// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/pptxedit"
	"github.com/mudler/LocalAI/addons/office/internal/textinput"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

// A created file is saved as a new file (version zero). If the path is taken,
// the store keeps the old file and the new one is saved next to it.

// decodeTextCreation is deliberately local to the three string creation tools.
// Bound raw JSON before decoding, and never echo decoder errors or user content.
func decodeTextCreation(raw json.RawMessage, v any, field string) error {
	if len(raw) > 1<<20 {
		return refuse("arguments exceed 1 MiB; shorten %s", field)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return refuse("arguments must be a JSON object with path and %s strings", field)
	}
	for _, key := range []string{"path", field, "title", "sheet"} {
		value, ok := fields[key]
		if !ok {
			continue
		}
		var text string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
			return refuse("%s must be a string", key)
		}
	}
	if err := decode(raw, v); err != nil {
		return refuse("use only path, %s and the optional metadata strings", field)
	}
	return nil
}

func (w *work) createDocx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	if err := decodeTextCreation(raw, &a, "markdown"); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".docx"); err != nil {
		return nil, err
	}
	d, err := textinput.ParseMarkdown(w.ctx, a.Markdown)
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, refuse("markdown: %v", err)
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := docx.Write(&buf, docx.Document{Title: a.Title, Blocks: d.Docx}); err != nil {
		return nil, refuse("the document cannot be written: %v", err)
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	return w.store(a.Path, ".docx", buf.Bytes(), 0)
}

func (w *work) createXlsx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title string `json:"title"`
		CSV   string `json:"csv"`
		Sheet string `json:"sheet"`
	}
	if err := decodeTextCreation(raw, &a, "csv"); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".xlsx"); err != nil {
		return nil, err
	}
	wb, err := textinput.ParseCSV(w.ctx, a.CSV, a.Sheet, a.Title)
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, refuse("csv: %v", err)
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := xlsx.Write(&buf, wb); err != nil {
		return nil, refuse("the workbook cannot be written: %v", err)
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	return w.store(a.Path, ".xlsx", buf.Bytes(), 0)
}

func (w *work) createPptx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title    string       `json:"title"`
		Template string       `json:"template"`
		Slides   []slideInput `json:"slides"`
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
	if a.Template != "" {
		return w.createPptxFromTemplate(a.Path, a.Template, a.Title, a.Slides)
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

type slideInput struct {
	Title   string   `json:"title"`
	Bullets []string `json:"bullets"`
}

// createPptxFromTemplate builds the deck on a template the user has: the new
// deck keeps its slide masters, layouts, theme colors and fonts. The slides of
// the template are dropped, so the deck holds only the slides asked for. The
// template is read like any other file of the user, so the same path rules
// apply, and it is never changed. The deck title replaces the template's.
func (w *work) createPptxFromTemplate(path, template, title string, slides []slideInput) (any, error) {
	ext := strings.ToLower(filepath.Ext(template))
	if ext != ".pptx" && ext != ".potx" {
		return nil, refuse("the template must end in .pptx or .potx")
	}
	data, _, err := w.fetch(template, ext)
	if err != nil {
		return nil, err
	}
	d, err := pptxedit.OpenTemplate(data)
	if err != nil {
		return nil, refuse("the template %q is not a valid PowerPoint deck or template", template)
	}
	content, ok := d.LayoutOfType("obj", "tx")
	if !ok {
		return nil, refuse("the template %q has no Title and Content layout", template)
	}
	cover, ok := d.LayoutOfType("title")
	if !ok {
		cover = content
	}
	old := d.SlideCount()
	for i, s := range slides {
		layout := content
		if len(s.Bullets) == 0 {
			layout = cover
		}
		if err := d.AddSlide(layout, s.Title, s.Bullets); err != nil {
			return nil, refuse("slide %d: %v", i+1, err)
		}
	}
	for range old {
		if err := d.DeleteSlide(1); err != nil {
			return nil, refuse("the slides of the template cannot be removed: %v", err)
		}
	}
	d.SetDocTitle(title)
	out, err := d.Bytes()
	if err != nil {
		return nil, refuse("the deck cannot be written: %v", err)
	}
	return w.store(path, ".pptx", out, 0)
}

func (w *work) createPdf(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	if err := decodeTextCreation(raw, &a, "markdown"); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, ".pdf"); err != nil {
		return nil, err
	}
	d, err := textinput.ParseMarkdown(w.ctx, a.Markdown)
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, refuse("markdown: %v", err)
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	data, err := pdf.Create(pdf.Spec{Title: a.Title, Pages: []pdf.Page{{Blocks: d.PDF}}})
	if err != nil {
		return nil, refuse("the PDF cannot be written: %v", err)
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	return w.store(a.Path, ".pdf", data, 0)
}
