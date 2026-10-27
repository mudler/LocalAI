// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The schema helpers build the argument schemas the manifest declares. The
// manifest is generated from Definitions and a test keeps the two in step.

type obj = map[string]any

func str(desc string) obj { return obj{"type": "string", "description": desc} }
func integer(desc string) obj {
	return obj{"type": "integer", "description": desc}
}
func list(items obj, desc string, max int) obj {
	return obj{"type": "array", "description": desc, "items": items, "maxItems": max}
}
func enum(desc string, values ...string) obj {
	return obj{"type": "string", "description": desc, "enum": values}
}
func object(props obj, required ...string) obj {
	s := obj{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var pathDesc = "The file, as a path inside the user's files, such as /reports/plan"

func pathProp(ext string) obj { return str(pathDesc + ext + ".") }

func def(name, kind, desc string, schema obj) contract.ToolDefinition {
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return contract.ToolDefinition{Name: name, Kind: kind, Description: desc, InputSchema: raw}
}

// Definitions returns what a model sees of each tool. Reads change nothing.
// Edits and creations are action tools: they run at once, as the user, and a
// change made by someone else in the meantime is kept, never overwritten.
func Definitions() []contract.ToolDefinition {
	read, act := contract.ToolKindRead, contract.ToolKindAction
	return []contract.ToolDefinition{
		def(ToolReadDocx, read, "Read a Word file (.docx): its paragraphs with their index, and its tables. Use the index with edit-docx.",
			object(obj{"path": pathProp(".docx")}, "path")),
		def(ToolReadXlsx, read, "Read an Excel file (.xlsx): the sheet names and the cell values of each sheet, row by row. Give sheet to read one sheet.",
			object(obj{"path": pathProp(".xlsx"), "sheet": str("The name of one sheet. Default: all sheets.")}, "path")),
		def(ToolReadPptx, read, "Read a PowerPoint file (.pptx): each slide with its title and its text shapes (id, kind, paragraphs). Use the shape id with edit-pptx.",
			object(obj{"path": pathProp(".pptx")}, "path")),
		def(ToolReadPdf, read, "Read the text of a PDF file (.pdf), page by page. A scanned page without text gives no text.",
			object(obj{"path": pathProp(".pdf"), "firstPage": integer("First page, from 1. Default 1."), "lastPage": integer("Last page. Default: the end, up to 50 pages.")}, "path")),
		def(ToolEditDocx, act, "Change a Word file in place. Edits run in order and all or none are saved. Read the file first to get paragraph indexes. If someone changed the file meanwhile, your version is saved as a second file.",
			object(obj{
				"path": pathProp(".docx"),
				"edits": list(object(obj{
					"op":    enum("What to do.", "set", "insert", "delete", "replace"),
					"index": integer("set and delete: the paragraph index."),
					"after": integer("insert: insert after this paragraph index; -1 for the start."),
					"style": str("insert: a paragraph style the document defines. Optional."),
					"text":  str("set and insert: the new text. replace: the text to put in."),
					"find":  str("replace: the text to look for, everywhere."),
				}, "op"), "The changes, at most 200.", maxEdits),
			}, "path", "edits")),
		def(ToolEditXlsx, act, "Change an Excel file in place. Give sheet and cells; each cell has a ref such as B3 and exactly one of text, number, formula (such as =SUM(B2:B9)) or clear. addSheet adds an empty sheet first.",
			object(obj{
				"path":     pathProp(".xlsx"),
				"sheet":    str("The sheet the cells belong to."),
				"addSheet": str("The name of a sheet to add before the cells are set. Optional."),
				"cells": list(object(obj{
					"ref":     str("The cell, such as B3."),
					"text":    str("Set the cell to this text."),
					"number":  obj{"type": "number", "description": "Set the cell to this number."},
					"formula": str("Set the cell to this formula."),
					"clear":   obj{"type": "boolean", "description": "Empty the cell."},
				}, "ref"), "The cells to change, at most 200.", maxEdits),
			}, "path")),
		def(ToolEditPptx, act, "Change a PowerPoint file in place. Ops: title (slide, text), text (slide, shape id, paragraph, text), add (text is the title, bullets, optional layout), delete (slide). Read the file first for ids.",
			object(obj{
				"path": pathProp(".pptx"),
				"edits": list(object(obj{
					"op":        enum("What to do.", "title", "text", "add", "delete"),
					"slide":     integer("The slide, from 1."),
					"shape":     integer("text: the shape id from read-pptx."),
					"paragraph": integer("text: the paragraph number in the shape, from 0."),
					"text":      str("The new text, or for add the new slide's title."),
					"layout":    str("add: the layout name. Default Title and Content."),
					"bullets":   list(obj{"type": "string"}, "add: the bullet lines.", 30),
				}, "op"), "The changes, at most 200.", maxEdits),
			}, "path", "edits")),
		def(ToolCreateDocx, act, "Create a Word file from blocks. A block has a kind: heading (text, level 1 to 3), paragraph (text) or bullets (items). If the path is taken, the old file stays and the new one is saved next to it.",
			object(obj{
				"path":  pathProp(".docx"),
				"title": str("The document title. Optional."),
				"blocks": list(object(obj{
					"kind":  enum("The kind of block.", "heading", "paragraph", "bullets"),
					"level": integer("heading: 1 to 3."),
					"text":  str("heading and paragraph: the text."),
					"items": list(obj{"type": "string"}, "bullets: the lines.", 100),
				}, "kind"), "The blocks, in order.", 2000),
			}, "path", "blocks")),
		def(ToolCreateXlsx, act, "Create an Excel file. Each sheet has a name and rows of text values. A value starting with = is a formula, a number is stored as a number. The first row is the header. If the path is taken, the old file stays.",
			object(obj{
				"path":  pathProp(".xlsx"),
				"title": str("The workbook title. Optional."),
				"sheets": list(object(obj{
					"name": str("The sheet name, at most 31 characters."),
					"rows": list(list(obj{"type": "string"}, "One row.", 200), "The rows, top to bottom.", 5000),
				}, "name", "rows"), "The sheets.", 32),
			}, "path", "sheets")),
		def(ToolCreatePptx, act, "Create a PowerPoint file from slides, each with a title and bullet lines. A slide without bullets is a title slide. If the path is taken, the old file stays and the new one is saved next to it.",
			object(obj{
				"path":  pathProp(".pptx"),
				"title": str("The deck title. Optional."),
				"slides": list(object(obj{
					"title":   str("The slide title."),
					"bullets": list(obj{"type": "string"}, "The bullet lines.", 30),
				}, "title"), "The slides, in order.", 200),
			}, "path", "slides")),
	}
}
