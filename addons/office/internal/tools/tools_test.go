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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
	dir      string
	files    map[string][]byte
	versions map[string]int64
	// afterExport runs after a file was copied out, to change it behind the tool.
	afterExport func(path string)
	calls       []string
}

func newHost() *fakeHost {
	return &fakeHost{dir: GinkgoT().TempDir(), files: map[string][]byte{}, versions: map[string]int64{}}
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
		Fail("unexpected host operation " + op)
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

func call(h *fakeHost, tool string, args map[string]any) (outcome, string) {
	GinkgoHelper()
	rt := tools.New(h, h.dir)
	var res contract.Result
	var err error
	if strings.HasPrefix(tool, "read-") {
		res, err = rt.Read(context.Background(), env(tool, args))
	} else {
		res, err = rt.(contract.ActRuntime).Act(context.Background(), env(tool, args))
	}
	Expect(err).NotTo(HaveOccurred(), tool)
	var o outcome
	_ = json.Unmarshal(res.Payload, &o)
	return o, string(res.Payload)
}

func docxBytes(paras ...string) []byte {
	GinkgoHelper()
	var blocks []docx.Block
	for _, p := range paras {
		blocks = append(blocks, docx.Paragraph{Text: p})
	}
	var buf bytes.Buffer
	Expect(docx.Write(&buf, docx.Document{Title: "t", Blocks: blocks})).To(Succeed())
	return buf.Bytes()
}

func xlsxBytes() []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := xlsx.Write(&buf, xlsx.Workbook{Sheets: []xlsx.Sheet{{Name: "Sales", Cells: []xlsx.Cell{
		{Ref: "A1", Value: xlsx.Str("item")}, {Ref: "B1", Value: xlsx.Str("qty")},
		{Ref: "A2", Value: xlsx.Str("apples")}, {Ref: "B2", Value: xlsx.Num(3)},
	}}}})
	Expect(err).NotTo(HaveOccurred())
	return buf.Bytes()
}

var _ = Describe("office tools", func() {
	It("read-docx gives the paragraphs with their index", func() {
		h := newHost()
		h.put("/memo.docx", docxBytes("first line", "second line"))
		_, raw := call(h, tools.ToolReadDocx, map[string]any{"path": "/memo.docx"})
		for _, want := range []string{`"index":0`, "first line", `"index":1`, "second line"} {
			Expect(raw).To(ContainSubstring(want), "read-docx lacks %q: %s", want, raw)
		}
	})

	It("read-xlsx gives the cells of a sheet", func() {
		h := newHost()
		h.put("/sales.xlsx", xlsxBytes())
		_, raw := call(h, tools.ToolReadXlsx, map[string]any{"path": "/sales.xlsx"})
		for _, want := range []string{"Sales", "apples", "qty"} {
			Expect(raw).To(ContainSubstring(want), "read-xlsx lacks %q: %s", want, raw)
		}
		o, _ := call(h, tools.ToolReadXlsx, map[string]any{"path": "/sales.xlsx", "sheet": "Nope"})
		Expect(o.OK).To(BeFalse(), "an unknown sheet answered %+v, want a refusal that names the problem", o)
		Expect(o.Error).NotTo(BeEmpty(), "an unknown sheet answered %+v, want a refusal that names the problem", o)
	})

	It("read-pptx gives the slides", func() {
		h := newHost()
		o, _ := call(h, tools.ToolCreatePptx, map[string]any{"path": "/deck.pptx", "slides": []map[string]any{
			{"title": "Plan", "bullets": []string{"ship it", "test it"}},
		}})
		Expect(o.OK).To(BeTrue(), "create-pptx: %+v", o)
		_, raw := call(h, tools.ToolReadPptx, map[string]any{"path": "/deck.pptx"})
		for _, want := range []string{"Plan", "ship it", "test it"} {
			Expect(raw).To(ContainSubstring(want), "read-pptx lacks %q: %s", want, raw)
		}
	})

	It("read-pdf gives the page text", func() {
		h := newHost()
		data, err := pdf.Create(pdf.Spec{Title: "t", Pages: []pdf.Page{{Blocks: []pdf.Block{pdf.Paragraph{Text: "hello from the pdf"}}}}})
		Expect(err).NotTo(HaveOccurred())
		h.put("/a.pdf", data)
		_, raw := call(h, tools.ToolReadPdf, map[string]any{"path": "/a.pdf"})
		Expect(raw).To(ContainSubstring("hello from the pdf"), "read-pdf lacks the text: %s", raw)
	})

	It("edit-docx changes only what was asked", func() {
		h := newHost()
		h.put("/memo.docx", docxBytes("keep me", "change me", "keep me too"))
		o, raw := call(h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{
			{"op": "set", "index": 1, "text": "changed"},
			{"op": "insert", "after": 2, "text": "added at the end"},
		}})
		Expect(o.OK).To(BeTrue(), "edit-docx = %s", raw)
		Expect(o.Path).To(Equal("/memo.docx"), "edit-docx = %s", raw)
		Expect(o.Conflict).To(BeFalse(), "edit-docx = %s", raw)
		Expect(h.versions["/memo.docx"]).To(Equal(int64(2)), "version (one save)")
		Expect(officecheck.Check(h.files["/memo.docx"])).To(BeEmpty(), "the edited file has problems")
		_, read := call(h, tools.ToolReadDocx, map[string]any{"path": "/memo.docx"})
		for _, want := range []string{"keep me", "changed", "keep me too", "added at the end"} {
			Expect(read).To(ContainSubstring(want), "after the edit the document lacks %q: %s", want, read)
		}
		Expect(read).NotTo(ContainSubstring("change me"), "the old text is still there: %s", read)
	})

	It("saves a file changed in the meantime as a second file", func() {
		h := newHost()
		h.put("/memo.docx", docxBytes("one"))
		h.afterExport = func(path string) { h.put(path, docxBytes("someone else wrote this")) }
		o, raw := call(h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{{"op": "set", "index": 0, "text": "mine"}}})
		Expect(o.OK).To(BeTrue(), "edit-docx = %s, want a conflict copy that names the original", raw)
		Expect(o.Conflict).To(BeTrue(), "edit-docx = %s, want a conflict copy that names the original", raw)
		Expect(o.Requested).To(Equal("/memo.docx"), "edit-docx = %s, want a conflict copy that names the original", raw)
		Expect(o.Path).NotTo(Equal("/memo.docx"), "edit-docx = %s, want a conflict copy that names the original", raw)
		Expect(raw).To(ContainSubstring("Tell the user"), "the model is not told to report it: %s", raw)
		Expect(h.files["/memo.docx"]).To(ContainSubstring("PK"), "the other person's file was damaged")
		Expect(officecheck.Check(h.files["/memo.docx"])).To(BeEmpty(), "the other person's file was damaged")
	})

	It("edit-xlsx sets cells and a formula", func() {
		h := newHost()
		h.put("/sales.xlsx", xlsxBytes())
		o, raw := call(h, tools.ToolEditXlsx, map[string]any{"path": "/sales.xlsx", "sheet": "Sales", "cells": []map[string]any{
			{"ref": "A3", "text": "pears"}, {"ref": "B3", "number": 5}, {"ref": "B4", "formula": "=SUM(B2:B3)"}, {"ref": "A1", "clear": true},
		}})
		Expect(o.OK).To(BeTrue(), "edit-xlsx = %s", raw)
		_, read := call(h, tools.ToolReadXlsx, map[string]any{"path": "/sales.xlsx"})
		for _, want := range []string{"pears", "apples"} {
			Expect(read).To(ContainSubstring(want), "the sheet lacks %q: %s", want, read)
		}
		Expect(read).NotTo(ContainSubstring(`"item"`), "A1 was not cleared: %s", read)
	})

	It("edit-pptx changes a title and deletes a slide", func() {
		h := newHost()
		call(h, tools.ToolCreatePptx, map[string]any{"path": "/d.pptx", "slides": []map[string]any{{"title": "Old", "bullets": []string{"a"}}, {"title": "Two", "bullets": []string{"b"}}}})
		o, raw := call(h, tools.ToolEditPptx, map[string]any{"path": "/d.pptx", "edits": []map[string]any{{"op": "title", "slide": 1, "text": "New"}, {"op": "delete", "slide": 2}}})
		Expect(o.OK).To(BeTrue(), "edit-pptx = %s", raw)
		_, read := call(h, tools.ToolReadPptx, map[string]any{"path": "/d.pptx"})
		Expect(read).To(ContainSubstring("New"), "deck after the edit")
		Expect(read).NotTo(ContainSubstring("Two"), "deck after the edit")
		Expect(read).NotTo(ContainSubstring("Old"), "deck after the edit")
	})

	It("creates files that are valid and read back", func() {
		h := newHost()
		o, raw := call(h, tools.ToolCreateDocx, map[string]any{"path": "/new.docx", "title": "Report", "blocks": []map[string]any{
			{"kind": "heading", "level": 1, "text": "Summary"},
			{"kind": "paragraph", "text": "All good."},
			{"kind": "bullets", "items": []string{"one", "two"}},
		}})
		Expect(o.OK).To(BeTrue(), "create-docx = %s", raw)
		o, raw = call(h, tools.ToolCreateXlsx, map[string]any{"path": "/new.xlsx", "sheets": []map[string]any{
			{"name": "Data", "rows": [][]string{{"name", "n"}, {"a", "1"}, {"total", "=SUM(B2:B2)"}}},
		}})
		Expect(o.OK).To(BeTrue(), "create-xlsx = %s", raw)
		for _, p := range []string{"/new.docx", "/new.xlsx"} {
			Expect(officecheck.Check(h.files[p])).To(BeEmpty(), "%s has problems", p)
		}
		_, read := call(h, tools.ToolReadDocx, map[string]any{"path": "/new.docx"})
		for _, want := range []string{"Summary", "All good.", "one", "two"} {
			Expect(read).To(ContainSubstring(want), "created docx lacks %q: %s", want, read)
		}
	})

	It("create-pdf writes a file the read tool reads", func() {
		h := newHost()
		o, raw := call(h, tools.ToolCreatePdf, map[string]any{"path": "/new.pdf", "title": "Report", "blocks": []map[string]any{
			{"kind": "heading", "level": 1, "text": "Summary"},
			{"kind": "paragraph", "text": "All good."},
			{"kind": "bullets", "items": []string{"one", "two"}},
			{"kind": "table", "header": []string{"name", "n"}, "rows": [][]string{{"a", "1"}}},
		}})
		Expect(o.OK).To(BeTrue(), "create-pdf = %s", raw)
		Expect(string(h.files["/new.pdf"])).To(HavePrefix("%PDF-"), "/new.pdf is not a PDF")
		_, read := call(h, tools.ToolReadPdf, map[string]any{"path": "/new.pdf"})
		for _, want := range []string{"Summary", "All good.", "one", "two"} {
			Expect(read).To(ContainSubstring(want), "created pdf lacks %q: %s", want, read)
		}
	})

	It("create-pdf refuses what it cannot write", func() {
		h := newHost()
		for name, args := range map[string]map[string]any{
			"unknown kind":        {"path": "/a.pdf", "blocks": []map[string]any{{"kind": "image"}}},
			"text outside latin1": {"path": "/b.pdf", "blocks": []map[string]any{{"kind": "paragraph", "text": "\u4f60\u597d"}}},
			"wrong extension":     {"path": "/c.txt", "blocks": []map[string]any{{"kind": "paragraph", "text": "x"}}},
		} {
			o, raw := call(h, tools.ToolCreatePdf, args)
			Expect(o.OK).To(BeFalse(), "%s: create-pdf accepted it: %s", name, raw)
		}
	})

	It("keeps both files when creating over an existing file", func() {
		h := newHost()
		h.put("/new.docx", docxBytes("already here"))
		o, _ := call(h, tools.ToolCreateDocx, map[string]any{"path": "/new.docx", "blocks": []map[string]any{{"kind": "paragraph", "text": "mine"}}})
		Expect(o.OK).To(BeTrue(), "create over a file = %+v, want a conflict copy", o)
		Expect(o.Conflict).To(BeTrue(), "create over a file = %+v, want a conflict copy", o)
		Expect(o.Requested).To(Equal("/new.docx"), "create over a file = %+v, want a conflict copy", o)
	})

	It("refuses a file that is not of the type", func() {
		h := newHost()
		h.put("/fake.docx", []byte("this is plain text"))
		h.put("/memo.txt", []byte("text"))
		for name, args := range map[string]map[string]any{
			"garbage content": {"path": "/fake.docx"},
			"wrong extension": {"path": "/memo.txt"},
			"missing file":    {"path": "/absent.docx"},
			"no path":         {"path": ""},
			"escaping path":   {"path": "../x.docx"},
		} {
			o, raw := call(h, tools.ToolReadDocx, args)
			Expect(o.OK).To(BeFalse(), "%s: answered %s, want a refusal", name, raw)
			Expect(o.Error).NotTo(BeEmpty(), "%s: answered %s, want a refusal", name, raw)
		}
		o, raw := call(h, tools.ToolEditDocx, map[string]any{"path": "/fake.docx", "edits": []map[string]any{{"op": "set", "index": 0, "text": "x"}}})
		Expect(o.OK).To(BeFalse(), "edit of a fake docx answered %s", raw)
		Expect(string(h.files["/fake.docx"])).To(Equal("this is plain text"), "a refused edit changed the file")
	})

	It("changes nothing when an edit fails part way", func() {
		h := newHost()
		h.put("/memo.docx", docxBytes("one", "two"))
		before := h.versions["/memo.docx"]
		o, _ := call(h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{
			{"op": "set", "index": 0, "text": "changed"}, {"op": "set", "index": 99, "text": "nope"},
		}})
		Expect(o.OK).To(BeFalse(), "outcome = %+v, want a refusal", o)
		Expect(o.Error).NotTo(BeEmpty(), "outcome = %+v, want a refusal", o)
		Expect(h.versions["/memo.docx"]).To(Equal(before), "a half-applied edit was saved")
	})

	It("bounds the sizes", func() {
		h := newHost()
		var paras []string
		for i := range 3000 {
			paras = append(paras, fmt.Sprintf("paragraph %d %s", i, strings.Repeat("x", 80)))
		}
		h.put("/big.docx", docxBytes(paras...))
		o, raw := call(h, tools.ToolReadDocx, map[string]any{"path": "/big.docx"})
		Expect(o.Truncated).To(BeTrue(), "read-docx of a big file")
		Expect(len(raw)).To(BeNumerically("<=", 64<<10), "read-docx of a big file")
		edits := make([]map[string]any, 201)
		for i := range edits {
			edits[i] = map[string]any{"op": "replace", "find": "paragraph", "text": "p"}
		}
		o, _ = call(h, tools.ToolEditDocx, map[string]any{"path": "/big.docx", "edits": edits})
		Expect(o.OK).To(BeFalse(), "201 edits were accepted")
	})

	It("removes the scratch folder", func() {
		h := newHost()
		h.put("/memo.docx", docxBytes("one"))
		call(h, tools.ToolReadDocx, map[string]any{"path": "/memo.docx"})
		call(h, tools.ToolEditDocx, map[string]any{"path": "/memo.docx", "edits": []map[string]any{{"op": "set", "index": 0, "text": "x"}}})
		entries, _ := os.ReadDir(filepath.Join(h.dir, "work"))
		Expect(entries).To(BeEmpty(), "work folders left behind")
	})

	It("serves a tool only for the right kind of call", func() {
		h := newHost()
		rt := tools.New(h, h.dir)
		_, err := rt.Read(context.Background(), env(tools.ToolEditDocx, map[string]any{"path": "/a.docx"}))
		Expect(err).To(HaveOccurred(), "an action tool was served by Read")
		_, err = rt.(contract.ActRuntime).Act(context.Background(), env(tools.ToolReadDocx, map[string]any{"path": "/a.docx"}))
		Expect(err).To(HaveOccurred(), "a read tool was served by Act")
		_, err = rt.Read(context.Background(), env("no-such-tool", nil))
		Expect(err).To(HaveOccurred(), "an unknown tool was served")
		var _ = protocol.ErrBadRequest
	})
})
