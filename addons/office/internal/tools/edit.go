// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/addons/office/internal/docxedit"
	"github.com/mudler/LocalAI/addons/office/internal/pptxedit"
	"github.com/mudler/LocalAI/addons/office/internal/xlsxedit"
)

// An edit is applied in memory, in order. Any one that fails stops the call
// and nothing is saved: the file the user sees is either unchanged or fully
// edited.

func (w *work) editDocx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Edits []struct {
			Op    string `json:"op"`
			Index *int   `json:"index"`
			After *int   `json:"after"`
			Style string `json:"style"`
			Text  string `json:"text"`
			Find  string `json:"find"`
		} `json:"edits"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Edits) == 0 || len(a.Edits) > maxEdits {
		return nil, refuse("edits must hold 1 to %d changes", maxEdits)
	}
	data, version, err := w.fetch(a.Path, ".docx")
	if err != nil {
		return nil, err
	}
	doc, err := docxedit.Open(data)
	if err != nil {
		return nil, refuse("the file is not a valid Word document")
	}
	replaced := 0
	for i, e := range a.Edits {
		fail := func(err error) error { return refuse("edit %d (%s): %v", i+1, e.Op, err) }
		switch e.Op {
		case "set":
			if e.Index == nil {
				return nil, fail(fmt.Errorf("index is required"))
			}
			if err := doc.SetText(*e.Index, e.Text); err != nil {
				return nil, fail(err)
			}
		case "insert":
			if e.After == nil {
				return nil, fail(fmt.Errorf("after is required (-1 for the start)"))
			}
			if err := doc.InsertParagraph(*e.After, e.Style, e.Text); err != nil {
				return nil, fail(err)
			}
		case "delete":
			if e.Index == nil {
				return nil, fail(fmt.Errorf("index is required"))
			}
			if err := doc.DeleteParagraph(*e.Index); err != nil {
				return nil, fail(err)
			}
		case "replace":
			if e.Find == "" {
				return nil, fail(fmt.Errorf("find is required"))
			}
			n, err := doc.ReplaceAll(e.Find, e.Text)
			if err != nil {
				return nil, fail(err)
			}
			if n == 0 {
				return nil, fail(fmt.Errorf("the text %q is not in the document", e.Find))
			}
			replaced += n
		default:
			return nil, fail(fmt.Errorf("op must be set, insert, delete or replace"))
		}
	}
	out, err := doc.Bytes()
	if err != nil {
		return nil, refuse("the edited document cannot be written: %v", err)
	}
	s, err := w.store(a.Path, ".docx", out, version)
	if err != nil {
		return nil, err
	}
	return withCount(s, len(a.Edits), replaced), nil
}

// withCount adds what an edit call did to the answer.
func withCount(s saved, edits, replaced int) any {
	type answer struct {
		saved
		Edits    int `json:"edits"`
		Replaced int `json:"replaced,omitempty"`
	}
	return answer{saved: s, Edits: edits, Replaced: replaced}
}

func (w *work) editXlsx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Sheet    string `json:"sheet"`
		AddSheet string `json:"addSheet"`
		Cells    []struct {
			Ref     string   `json:"ref"`
			Text    *string  `json:"text"`
			Number  *float64 `json:"number"`
			Formula *string  `json:"formula"`
			Clear   bool     `json:"clear"`
		} `json:"cells"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Cells) > maxEdits || (len(a.Cells) == 0 && a.AddSheet == "") {
		return nil, refuse("cells must hold 1 to %d changes, or addSheet must name a sheet", maxEdits)
	}
	if len(a.Cells) > 0 && a.Sheet == "" {
		return nil, refuse("sheet is required to change cells")
	}
	data, version, err := w.fetch(a.Path, ".xlsx")
	if err != nil {
		return nil, err
	}
	wb, err := xlsxedit.Open(data)
	if err != nil {
		return nil, refuse("the file is not a valid Excel workbook")
	}
	if a.AddSheet != "" {
		if err := wb.AddSheet(a.AddSheet); err != nil {
			return nil, refuse("the sheet cannot be added: %v", err)
		}
	}
	for i, c := range a.Cells {
		set := 0
		for _, on := range []bool{c.Text != nil, c.Number != nil, c.Formula != nil, c.Clear} {
			if on {
				set++
			}
		}
		if set != 1 {
			return nil, refuse("cell %d (%s): give exactly one of text, number, formula or clear", i+1, c.Ref)
		}
		var err error
		switch {
		case c.Text != nil:
			err = wb.SetCell(a.Sheet, c.Ref, xlsxedit.Str(*c.Text))
		case c.Number != nil:
			err = wb.SetCell(a.Sheet, c.Ref, xlsxedit.Num(*c.Number))
		case c.Formula != nil:
			err = wb.SetCell(a.Sheet, c.Ref, xlsxedit.Formula(*c.Formula))
		default:
			err = wb.ClearCell(a.Sheet, c.Ref)
		}
		if err != nil {
			return nil, refuse("cell %d (%s): %v", i+1, c.Ref, err)
		}
	}
	out, err := wb.Bytes()
	if err != nil {
		return nil, refuse("the edited workbook cannot be written: %v", err)
	}
	s, err := w.store(a.Path, ".xlsx", out, version)
	if err != nil {
		return nil, err
	}
	return withCount(s, len(a.Cells), 0), nil
}

func (w *work) editPptx(raw json.RawMessage) (any, error) {
	var a struct {
		pathArgs
		Edits []struct {
			Op        string   `json:"op"`
			Slide     int      `json:"slide"`
			Shape     int      `json:"shape"`
			Paragraph int      `json:"paragraph"`
			Text      string   `json:"text"`
			Layout    string   `json:"layout"`
			Bullets   []string `json:"bullets"`
		} `json:"edits"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Edits) == 0 || len(a.Edits) > maxEdits {
		return nil, refuse("edits must hold 1 to %d changes", maxEdits)
	}
	data, version, err := w.fetch(a.Path, ".pptx")
	if err != nil {
		return nil, err
	}
	deck, err := pptxedit.Open(data)
	if err != nil {
		return nil, refuse("the file is not a valid PowerPoint deck")
	}
	for i, e := range a.Edits {
		var err error
		switch e.Op {
		case "title":
			err = deck.SetTitle(e.Slide, e.Text)
		case "text":
			err = deck.SetShapeText(e.Slide, e.Shape, e.Paragraph, e.Text)
		case "add":
			layout := e.Layout
			if layout == "" {
				layout = "Title and Content"
			}
			err = deck.AddSlide(layout, e.Text, e.Bullets)
		case "delete":
			err = deck.DeleteSlide(e.Slide)
		default:
			err = fmt.Errorf("op must be title, text, add or delete")
		}
		if err != nil {
			return nil, refuse("edit %d (%s): %v", i+1, e.Op, err)
		}
	}
	out, err := deck.Bytes()
	if err != nil {
		return nil, refuse("the edited deck cannot be written: %v", err)
	}
	s, err := w.store(a.Path, ".pptx", out, version)
	if err != nil {
		return nil, err
	}
	return withCount(s, len(a.Edits), 0), nil
}
