// SPDX-License-Identifier: MIT
package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/tools"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// fakeHost is the host side of the workspace operations: a map of files with a
// version each, and the same copy-in, copy-out and conflict rules.
type fakeHost struct {
	t        *testing.T
	dir      string
	files    map[string][]byte
	versions map[string]int64
	// afterExport runs after a file was copied out, to change it behind the tool.
	afterExport func(path string)
	calls       []string
}

func newHost(t *testing.T) *fakeHost {
	return &fakeHost{t: t, dir: t.TempDir(), files: map[string][]byte{}, versions: map[string]int64{}}
}

func (h *fakeHost) put(path string, data []byte) {
	h.files[path] = data
	h.versions[path]++
}

func (h *fakeHost) Do(_ context.Context, _ string, op string, args, out any) error {
	raw, _ := json.Marshal(args)
	h.calls = append(h.calls, op)
	var in struct {
		Path        string `json:"path"`
		To          string `json:"to"`
		From        string `json:"from"`
		BaseVersion int64  `json:"baseVersion"`
	}
	_ = json.Unmarshal(raw, &in)
	bad := &contract.HostError{Code: contract.HostBadRequest, Message: "refused"}
	for _, p := range []string{in.To, in.From} {
		if p != "" && (strings.HasPrefix(p, "/") || strings.Contains(p, "..")) {
			return bad
		}
	}
	var res any
	switch op {
	case "workspace.export":
		if strings.Contains(in.Path, "..") {
			return bad
		}
		data, ok := h.files[in.Path]
		if !ok {
			return &contract.HostError{Code: contract.HostNotFound, Message: "not found"}
		}
		dst := filepath.Join(h.dir, filepath.FromSlash(in.To))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
		v := h.versions[in.Path]
		if h.afterExport != nil {
			h.afterExport(in.Path)
		}
		res = map[string]int64{"version": v}
	case "workspace.import":
		data, err := os.ReadFile(filepath.Join(h.dir, filepath.FromSlash(in.From)))
		if err != nil {
			return err
		}
		target, conflictOf := in.Path, ""
		if _, exists := h.files[in.Path]; exists && h.versions[in.Path] != in.BaseVersion {
			target, conflictOf = strings.TrimSuffix(in.Path, filepath.Ext(in.Path))+" (conflict)"+filepath.Ext(in.Path), in.Path
		}
		h.put(target, data)
		res = map[string]any{"version": h.versions[target], "path": target, "conflictOf": conflictOf}
	default:
		h.t.Fatalf("unexpected host operation %s", op)
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

func env(tool string, args map[string]any) contract.Envelope {
	if args == nil {
		args = map[string]any{}
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	return contract.Envelope{Request: contract.Request{AddonID: "office", OperationID: "t0123456789abcdef", Generation: 1, Arguments: raw}}
}

type outcome struct {
	OK        bool             `json:"ok"`
	Error     string           `json:"error"`
	Path      string           `json:"path"`
	Version   int64            `json:"version"`
	Conflict  bool             `json:"conflict"`
	Requested string           `json:"requested"`
	Truncated bool             `json:"truncated"`
	Raw       map[string]any   `json:"-"`
	Items     []map[string]any `json:"-"`
}

func call(t *testing.T, h *fakeHost, tool string, args map[string]any) (outcome, string) {
	t.Helper()
	rt := tools.New(h, h.dir)
	var res contract.Result
	var err error
	if strings.HasPrefix(tool, "read-") {
		res, err = rt.Read(context.Background(), env(tool, args))
	} else {
		res, err = rt.(contract.ActRuntime).Act(context.Background(), env(tool, args))
	}
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var o outcome
	_ = json.Unmarshal(res.Payload, &o)
	return o, string(res.Payload)
}

func docxBytes(t *testing.T, paras ...string) []byte {
	t.Helper()
	var blocks []docx.Block
	for _, p := range paras {
		blocks = append(blocks, docx.Paragraph{Text: p})
	}
	var buf bytes.Buffer
	if err := docx.Write(&buf, docx.Document{Title: "t", Blocks: blocks}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func xlsxBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := xlsx.Write(&buf, xlsx.Workbook{Sheets: []xlsx.Sheet{{Name: "Sales", Cells: []xlsx.Cell{
		{Ref: "A1", Value: xlsx.Str("item")}, {Ref: "B1", Value: xlsx.Str("qty")},
		{Ref: "A2", Value: xlsx.Str("apples")}, {Ref: "B2", Value: xlsx.Num(3)},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadDocxGivesTheParagraphsWithTheirIndex(t *testing.T) {
	h := newHost(t)
	h.put("/memo.docx", docxBytes(t, "first line", "second line"))
	_, raw := call(t, h, tools.ToolReadDocx, map[string]any{"path": "/memo.docx"})
	for _, want := range []string{`"index":0`, "first line", `"index":1`, "second line"} {
		if !strings.Contains(raw, want) {
			t.Errorf("read-docx lacks %q: %s", want, raw)
		}
	}
}

func TestReadXlsxGivesTheCellsOfASheet(t *testing.T) {
	h := newHost(t)
	h.put("/sales.xlsx", xlsxBytes(t))
	_, raw := call(t, h, tools.ToolReadXlsx, map[string]any{"path": "/sales.xlsx"})
	for _, want := range []string{"Sales", "apples", "qty"} {
		if !strings.Contains(raw, want) {
			t.Errorf("read-xlsx lacks %q: %s", want, raw)
		}
	}
	o, _ := call(t, h, tools.ToolReadXlsx, map[string]any{"path": "/sales.xlsx", "sheet": "Nope"})
	if o.OK || o.Error == "" {
		t.Errorf("an unknown sheet answered %+v, want a refusal that names the problem", o)
	}
}

func TestReadPptxGivesTheSlides(t *testing.T) {
	h := newHost(t)
	o, _ := call(t, h, tools.ToolCreatePptx, map[string]any{"path": "/deck.pptx", "slides": []map[string]any{
		{"title": "Plan", "bullets": []string{"ship it", "test it"}},
	}})
	if !o.OK {
		t.Fatalf("create-pptx: %+v", o)
	}
	_, raw := call(t, h, tools.ToolReadPptx, map[string]any{"path": "/deck.pptx"})
	for _, want := range []string{"Plan", "ship it", "test it"} {
		if !strings.Contains(raw, want) {
			t.Errorf("read-pptx lacks %q: %s", want, raw)
		}
	}
}

func TestReadPdfGivesThePageText(t *testing.T) {
	h := newHost(t)
	data, err := pdf.Create(pdf.Spec{Title: "t", Pages: []pdf.Page{{Blocks: []pdf.Block{pdf.Paragraph{Text: "hello from the pdf"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	h.put("/a.pdf", data)
	_, raw := call(t, h, tools.ToolReadPdf, map[string]any{"path": "/a.pdf"})
	if !strings.Contains(raw, "hello from the pdf") {
		t.Errorf("read-pdf lacks the text: %s", raw)
	}
}

func TestEditDocxChangesOnlyWhatWasAsked(t *testing.T) {
	h := newHost(t)
	h.put("/memo.docx", docxBytes(t, "keep me", "change me", "keep me too"))
	o, raw := call(t, h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{
		{"op": "set", "index": 1, "text": "changed"},
		{"op": "insert", "after": 2, "text": "added at the end"},
	}})
	if !o.OK || o.Path != "/memo.docx" || o.Conflict {
		t.Fatalf("edit-docx = %s", raw)
	}
	if v := h.versions["/memo.docx"]; v != 2 {
		t.Errorf("version = %d, want 2 (one save)", v)
	}
	if problems := officecheck.Check(h.files["/memo.docx"]); len(problems) != 0 {
		t.Errorf("the edited file has problems: %v", problems)
	}
	_, read := call(t, h, tools.ToolReadDocx, map[string]any{"path": "/memo.docx"})
	for _, want := range []string{"keep me", "changed", "keep me too", "added at the end"} {
		if !strings.Contains(read, want) {
			t.Errorf("after the edit the document lacks %q: %s", want, read)
		}
	}
	if strings.Contains(read, "change me") {
		t.Errorf("the old text is still there: %s", read)
	}
}

func TestAFileChangedInTheMeantimeBecomesASecondFile(t *testing.T) {
	h := newHost(t)
	h.put("/memo.docx", docxBytes(t, "one"))
	h.afterExport = func(path string) { h.put(path, docxBytes(t, "someone else wrote this")) }
	o, raw := call(t, h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{{"op": "set", "index": 0, "text": "mine"}}})
	if !o.OK || !o.Conflict || o.Requested != "/memo.docx" || o.Path == "/memo.docx" {
		t.Fatalf("edit-docx = %s, want a conflict copy that names the original", raw)
	}
	if !strings.Contains(raw, "Tell the user") {
		t.Errorf("the model is not told to report it: %s", raw)
	}
	if !bytes.Contains(h.files["/memo.docx"], []byte("PK")) || len(officecheck.Check(h.files["/memo.docx"])) != 0 {
		t.Errorf("the other person's file was damaged")
	}
}

func TestEditXlsxSetsCellsAndAFormula(t *testing.T) {
	h := newHost(t)
	h.put("/sales.xlsx", xlsxBytes(t))
	o, raw := call(t, h, tools.ToolEditXlsx, map[string]any{"path": "/sales.xlsx", "sheet": "Sales", "cells": []map[string]any{
		{"ref": "A3", "text": "pears"}, {"ref": "B3", "number": 5}, {"ref": "B4", "formula": "=SUM(B2:B3)"}, {"ref": "A1", "clear": true},
	}})
	if !o.OK {
		t.Fatalf("edit-xlsx = %s", raw)
	}
	_, read := call(t, h, tools.ToolReadXlsx, map[string]any{"path": "/sales.xlsx"})
	for _, want := range []string{"pears", "apples"} {
		if !strings.Contains(read, want) {
			t.Errorf("the sheet lacks %q: %s", want, read)
		}
	}
	if strings.Contains(read, `"item"`) {
		t.Errorf("A1 was not cleared: %s", read)
	}
}

func TestEditPptxChangesATitleAndAddsASlide(t *testing.T) {
	h := newHost(t)
	call(t, h, tools.ToolCreatePptx, map[string]any{"path": "/d.pptx", "slides": []map[string]any{{"title": "Old", "bullets": []string{"a"}}, {"title": "Two", "bullets": []string{"b"}}}})
	o, raw := call(t, h, tools.ToolEditPptx, map[string]any{"path": "/d.pptx", "edits": []map[string]any{{"op": "title", "slide": 1, "text": "New"}, {"op": "delete", "slide": 2}}})
	if !o.OK {
		t.Fatalf("edit-pptx = %s", raw)
	}
	_, read := call(t, h, tools.ToolReadPptx, map[string]any{"path": "/d.pptx"})
	if !strings.Contains(read, "New") || strings.Contains(read, "Two") || strings.Contains(read, "Old") {
		t.Errorf("deck after the edit: %s", read)
	}
}

func TestCreatedFilesAreValidAndReadBack(t *testing.T) {
	h := newHost(t)
	o, raw := call(t, h, tools.ToolCreateDocx, map[string]any{"path": "/new.docx", "title": "Report", "blocks": []map[string]any{
		{"kind": "heading", "level": 1, "text": "Summary"},
		{"kind": "paragraph", "text": "All good."},
		{"kind": "bullets", "items": []string{"one", "two"}},
	}})
	if !o.OK {
		t.Fatalf("create-docx = %s", raw)
	}
	o, raw = call(t, h, tools.ToolCreateXlsx, map[string]any{"path": "/new.xlsx", "sheets": []map[string]any{
		{"name": "Data", "rows": [][]string{{"name", "n"}, {"a", "1"}, {"total", "=SUM(B2:B2)"}}},
	}})
	if !o.OK {
		t.Fatalf("create-xlsx = %s", raw)
	}
	for _, p := range []string{"/new.docx", "/new.xlsx"} {
		if problems := officecheck.Check(h.files[p]); len(problems) != 0 {
			t.Errorf("%s has problems: %v", p, problems)
		}
	}
	_, read := call(t, h, tools.ToolReadDocx, map[string]any{"path": "/new.docx"})
	for _, want := range []string{"Summary", "All good.", "one", "two"} {
		if !strings.Contains(read, want) {
			t.Errorf("created docx lacks %q: %s", want, read)
		}
	}
}

func TestCreatePdfWritesAFileTheReadToolReads(t *testing.T) {
	h := newHost(t)
	o, raw := call(t, h, tools.ToolCreatePdf, map[string]any{"path": "/new.pdf", "title": "Report", "blocks": []map[string]any{
		{"kind": "heading", "level": 1, "text": "Summary"},
		{"kind": "paragraph", "text": "All good."},
		{"kind": "bullets", "items": []string{"one", "two"}},
		{"kind": "table", "header": []string{"name", "n"}, "rows": [][]string{{"a", "1"}}},
	}})
	if !o.OK {
		t.Fatalf("create-pdf = %s", raw)
	}
	if !strings.HasPrefix(string(h.files["/new.pdf"]), "%PDF-") {
		t.Fatalf("/new.pdf is not a PDF")
	}
	_, read := call(t, h, tools.ToolReadPdf, map[string]any{"path": "/new.pdf"})
	for _, want := range []string{"Summary", "All good.", "one", "two"} {
		if !strings.Contains(read, want) {
			t.Errorf("created pdf lacks %q: %s", want, read)
		}
	}
}

func TestCreatePdfRefusesWhatItCannotWrite(t *testing.T) {
	h := newHost(t)
	for name, args := range map[string]map[string]any{
		"unknown kind":        {"path": "/a.pdf", "blocks": []map[string]any{{"kind": "image"}}},
		"text outside latin1": {"path": "/b.pdf", "blocks": []map[string]any{{"kind": "paragraph", "text": "\u4f60\u597d"}}},
		"wrong extension":     {"path": "/c.txt", "blocks": []map[string]any{{"kind": "paragraph", "text": "x"}}},
	} {
		o, raw := call(t, h, tools.ToolCreatePdf, args)
		if o.OK {
			t.Errorf("%s: create-pdf accepted it: %s", name, raw)
		}
	}
}

func TestCreateOverAnExistingFileKeepsBoth(t *testing.T) {
	h := newHost(t)
	h.put("/new.docx", docxBytes(t, "already here"))
	o, _ := call(t, h, tools.ToolCreateDocx, map[string]any{"path": "/new.docx", "blocks": []map[string]any{{"kind": "paragraph", "text": "mine"}}})
	if !o.OK || !o.Conflict || o.Requested != "/new.docx" {
		t.Fatalf("create over a file = %+v, want a conflict copy", o)
	}
}

func TestAFileThatIsNotTheTypeIsRefused(t *testing.T) {
	h := newHost(t)
	h.put("/fake.docx", []byte("this is plain text"))
	h.put("/memo.txt", []byte("text"))
	for name, args := range map[string]map[string]any{
		"garbage content": {"path": "/fake.docx"},
		"wrong extension": {"path": "/memo.txt"},
		"missing file":    {"path": "/absent.docx"},
		"no path":         {"path": ""},
		"escaping path":   {"path": "../x.docx"},
	} {
		o, raw := call(t, h, tools.ToolReadDocx, args)
		if o.OK || o.Error == "" {
			t.Errorf("%s: answered %s, want a refusal", name, raw)
		}
	}
	o, raw := call(t, h, tools.ToolEditDocx, map[string]any{"path": "/fake.docx", "edits": []map[string]any{{"op": "set", "index": 0, "text": "x"}}})
	if o.OK {
		t.Errorf("edit of a fake docx answered %s", raw)
	}
	if string(h.files["/fake.docx"]) != "this is plain text" {
		t.Errorf("a refused edit changed the file")
	}
}

func TestAnEditThatFailsPartWayChangesNothing(t *testing.T) {
	h := newHost(t)
	h.put("/memo.docx", docxBytes(t, "one", "two"))
	before := h.versions["/memo.docx"]
	o, _ := call(t, h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{
		{"op": "set", "index": 0, "text": "changed"}, {"op": "set", "index": 99, "text": "nope"},
	}})
	if o.OK || o.Error == "" {
		t.Fatalf("outcome = %+v, want a refusal", o)
	}
	if h.versions["/memo.docx"] != before {
		t.Errorf("a half-applied edit was saved")
	}
}

func TestSizesAreBounded(t *testing.T) {
	h := newHost(t)
	var paras []string
	for i := 0; i < 3000; i++ {
		paras = append(paras, fmt.Sprintf("paragraph %d %s", i, strings.Repeat("x", 80)))
	}
	h.put("/big.docx", docxBytes(t, paras...))
	o, raw := call(t, h, tools.ToolReadDocx, map[string]any{"path": "/big.docx"})
	if !o.Truncated || len(raw) > 64<<10 {
		t.Errorf("read-docx of a big file: truncated=%v, %d bytes", o.Truncated, len(raw))
	}
	edits := make([]map[string]any, 201)
	for i := range edits {
		edits[i] = map[string]any{"op": "replace", "find": "paragraph", "text": "p"}
	}
	o, _ = call(t, h, tools.ToolEditDocx, map[string]any{"path": "/big.docx", "edits": edits})
	if o.OK {
		t.Errorf("201 edits were accepted")
	}
}

func TestTheScratchFolderIsRemoved(t *testing.T) {
	h := newHost(t)
	h.put("/memo.docx", docxBytes(t, "one"))
	call(t, h, tools.ToolReadDocx, map[string]any{"path": "/memo.docx"})
	call(t, h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{{"op": "set", "index": 0, "text": "x"}}})
	entries, _ := os.ReadDir(filepath.Join(h.dir, "work"))
	if len(entries) != 0 {
		t.Errorf("work folders left behind: %v", entries)
	}
}

func TestOnlyTheRightKindOfCallServesATool(t *testing.T) {
	h := newHost(t)
	rt := tools.New(h, h.dir)
	if _, err := rt.Read(context.Background(), env(tools.ToolEditDocx, map[string]any{"path": "/a.docx"})); err == nil {
		t.Error("an action tool was served by Read")
	}
	if _, err := rt.(contract.ActRuntime).Act(context.Background(), env(tools.ToolReadDocx, map[string]any{"path": "/a.docx"})); err == nil {
		t.Error("a read tool was served by Act")
	}
	if _, err := rt.Read(context.Background(), env("no-such-tool", nil)); err == nil {
		t.Error("an unknown tool was served")
	}
	var _ = protocol.ErrBadRequest
}
