// SPDX-License-Identifier: MIT
package tools_test

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func creationZip(data []byte, name string) string {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	for _, f := range z.File {
		if f.Name == name {
			r, err := f.Open()
			Expect(err).NotTo(HaveOccurred())
			defer func() { Expect(r.Close()).To(Succeed()) }()
			b, err := io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			return string(b)
		}
	}
	Fail("missing zip member: " + name)
	return ""
}

type deniedCreationHost struct{ *fakeHost }

func (h deniedCreationHost) Do(_ context.Context, _ string, op string, _, _ any) error {
	h.calls = append(h.calls, op)
	return &contract.HostError{Code: contract.HostScope, Message: "denied"}
}

var _ = Describe("text creation contracts", func() {
	It("honors cancellation and host permission denial without workspace or scratch output", func() {
		for _, tc := range []struct{ tool, path, field, text string }{{tools.ToolCreateDocx, "/a.docx", "markdown", "hello"}, {tools.ToolCreatePdf, "/a.pdf", "markdown", "hello"}, {tools.ToolCreateXlsx, "/a.xlsx", "csv", "00123"}} {
			for _, cancelled := range []bool{true, false} {
				h := newHost()
				var engine tools.Engine = deniedCreationHost{h}
				ctx := context.Background()
				if cancelled {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					engine = h
				}
				rt := tools.New(engine, h.dir).(contract.ActRuntime)
				res, err := rt.Act(ctx, env(tc.tool, map[string]any{"path": tc.path, tc.field: tc.text}))
				if cancelled {
					Expect(err).To(MatchError(context.Canceled))
					Expect(h.calls).To(BeEmpty())
				} else {
					Expect(err).To(MatchError(ContainSubstring(protocol.ErrForbidden.Error())))
					Expect(h.calls).To(ConsistOf("workspace.import"))
				}
				Expect(res.Payload).To(BeEmpty())
				Expect(h.files).To(BeEmpty())
				entries, err := os.ReadDir(filepath.Join(h.dir, "work"))
				if !os.IsNotExist(err) {
					Expect(err).NotTo(HaveOccurred())
					Expect(entries).To(BeEmpty())
				}
			}
		}
	})

	It("creates styled DOCX and PDF from Markdown in the workspace", func() {
		h := newHost()
		md := "# Summary\n\nA **bold** and *italic* paragraph.\n\n1. first\n2. second\n\n| Name | Value |\n| --- | --- |\n| item | 00123 |\n"
		for _, tc := range []struct{ tool, path string }{{tools.ToolCreateDocx, "/text.docx"}, {tools.ToolCreatePdf, "/text.pdf"}} {
			o, raw := call(h, tc.tool, map[string]any{"path": tc.path, "markdown": md, "title": "Metadata only"})
			Expect(o.OK).To(BeTrue(), raw)
		}
		xml := creationZip(h.files["/text.docx"], "word/document.xml")
		for _, s := range []string{"Heading1", "<w:b", "<w:i", "<w:numPr", "<w:tbl", "00123"} {
			Expect(xml).To(ContainSubstring(s))
		}
		_, raw := call(h, tools.ToolReadPdf, map[string]any{"path": "/text.pdf"})
		for _, s := range []string{"Summary", "bold", "italic", "first", "item", "00123"} {
			Expect(raw).To(ContainSubstring(s))
		}
	})
	It("creates exactly one string-only sheet without formulas or header styling", func() {
		h := newHost()
		o, raw := call(h, tools.ToolCreateXlsx, map[string]any{"path": "/text.xlsx", "csv": "00123,=SUM(A1),+x,-x,@x\n"})
		Expect(o.OK).To(BeTrue(), raw)
		sheet := creationZip(h.files["/text.xlsx"], "xl/worksheets/sheet1.xml")
		Expect(sheet).NotTo(ContainSubstring("<f"))
		Expect(sheet).To(ContainSubstring(`t="s"`))
		Expect(sheet).NotTo(ContainSubstring(`s="1"`))
		ss := creationZip(h.files["/text.xlsx"], "xl/sharedStrings.xml")
		for _, s := range []string{"00123", "=SUM(A1)", "+x", "-x", "@x"} {
			Expect(ss).To(ContainSubstring(s))
		}
		Expect(creationZip(h.files["/text.xlsx"], "xl/workbook.xml")).To(ContainSubstring("Sheet1"))
	})
	It("rejects legacy arguments with actionable content-free errors and no output", func() {
		for _, tc := range []struct{ tool, path, legacy, field string }{{tools.ToolCreateDocx, "/a.docx", "blocks", "markdown"}, {tools.ToolCreatePdf, "/a.pdf", "blocks", "markdown"}, {tools.ToolCreateXlsx, "/a.xlsx", "sheets", "csv"}} {
			h := newHost()
			o, raw := call(h, tc.tool, map[string]any{"path": tc.path, tc.legacy: []any{map[string]any{"secret": "PRIVATE"}}})
			Expect(o.OK).To(BeFalse(), raw)
			Expect(o.Error).To(ContainSubstring(tc.field))
			Expect(raw).NotTo(ContainSubstring("PRIVATE"))
			Expect(h.files).To(BeEmpty())
		}
	})
	It("rejects malformed, oversized and unrenderable text without changing existing files", func() {
		for _, args := range []map[string]any{{"markdown": "![private](secret)"}, {"markdown": "你好"}, {"markdown": strings.Repeat("x", 1<<20)}, {"markdown": []string{"PRIVATE"}}} {
			h := newHost()
			h.put("/a.pdf", []byte("old"))
			args["path"] = "/a.pdf"
			o, raw := call(h, tools.ToolCreatePdf, args)
			Expect(o.OK).To(BeFalse(), raw)
			Expect(h.files).To(HaveLen(1))
			Expect(string(h.files["/a.pdf"])).To(Equal("old"))
		}
	})
	It("keeps old files and reports sibling conflicts for all three text tools", func() {
		for _, tc := range []struct{ tool, path, field, text string }{{tools.ToolCreateDocx, "/a.docx", "markdown", "hello"}, {tools.ToolCreatePdf, "/a.pdf", "markdown", "hello"}, {tools.ToolCreateXlsx, "/a.xlsx", "csv", "00123"}} {
			h := newHost()
			h.put(tc.path, []byte("old"))
			o, raw := call(h, tc.tool, map[string]any{"path": tc.path, tc.field: tc.text})
			Expect(o.OK).To(BeTrue(), raw)
			Expect(o.Conflict).To(BeTrue())
			Expect(o.Path).NotTo(Equal(tc.path))
			Expect(string(h.files[tc.path])).To(Equal("old"))
			Expect(h.files).To(HaveLen(2))
		}
	})
})
