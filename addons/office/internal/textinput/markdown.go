// SPDX-License-Identifier: MIT
package textinput

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

// MarkdownDocument contains equivalent bounded documents for both writers.
type MarkdownDocument struct {
	Docx []docx.Block
	PDF  []pdf.Block
}

type markdownParser struct {
	ctx    context.Context
	source []byte
	out    MarkdownDocument
	blocks int
}

// ParseMarkdown accepts only the documented basic-file subset of Markdown.
func ParseMarkdown(ctx context.Context, source string) (MarkdownDocument, error) {
	if err := ctx.Err(); err != nil {
		return MarkdownDocument{}, err
	}
	if len(source) > maxSourceBytes {
		return MarkdownDocument{}, errors.New("markdown: input exceeds 1 MiB")
	}
	if !validXML(source) {
		return MarkdownDocument{}, errors.New("markdown: invalid XML text")
	}
	if strings.TrimSpace(source) == "" {
		return MarkdownDocument{}, errors.New("markdown: supply nonempty text")
	}
	p := markdownParser{ctx: ctx, source: []byte(source)}
	root, err := guardedParse(ctx, p.source)
	if err != nil {
		return MarkdownDocument{}, err
	}
	// Bound the whole AST, including inline containers and semantic children.
	depth := 0
	err = ast.Walk(root, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !enter {
			depth--
			return ast.WalkContinue, nil
		}
		depth++
		if depth > 64 {
			return ast.WalkStop, p.fail(n, "nesting exceeds 64")
		}
		if n.Type() == ast.TypeBlock && n.Kind() != ast.KindDocument {
			p.blocks++
			if p.blocks > 2000 {
				return ast.WalkStop, p.fail(n, "semantic blocks exceed 2000")
			}
		}
		switch n.(type) {
		case *ast.Document, *ast.Heading, *ast.Paragraph, *ast.TextBlock, *ast.List, *ast.ListItem, *ast.FencedCodeBlock, *ast.CodeBlock, *east.Table, *east.TableHeader, *east.TableRow, *east.TableCell, *ast.Text, *ast.String, *ast.Emphasis, *ast.CodeSpan, *ast.Link, *ast.AutoLink:
		default:
			return ast.WalkStop, p.fail(n, "unsupported construct; use headings, paragraphs, lists or simple tables")
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return MarkdownDocument{}, err
	}
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		if err := p.block(n, 0); err != nil {
			return MarkdownDocument{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return MarkdownDocument{}, err
	}
	return p.out, nil
}
func (p *markdownParser) fail(n ast.Node, msg string) error {
	line := 1
	for node := n; node != nil; node = node.Parent() {
		if node.Type() == ast.TypeBlock && node.Lines().Len() > 0 {
			seg := node.Lines().At(0)
			line += strings.Count(string(p.source[:seg.Start]), "\n")
			break
		}
	}
	return fmt.Errorf("markdown: line %d: %s", line, msg)
}
func (p *markdownParser) add(d docx.Block, b pdf.Block) {
	p.out.Docx = append(p.out.Docx, d)
	p.out.PDF = append(p.out.PDF, b)
}
func pdfRuns(r []docx.Run) []pdf.Run {
	out := make([]pdf.Run, len(r))
	for i, v := range r {
		out[i] = pdf.Run{Text: v.Text, Bold: v.Bold, Italic: v.Italic}
	}
	return out
}
func runText(r []docx.Run) string {
	var b strings.Builder
	for _, v := range r {
		b.WriteString(v.Text)
	}
	return b.String()
}
func (p *markdownParser) inline(parent ast.Node) ([]docx.Run, error) {
	var runs []docx.Run
	var visible strings.Builder
	var visit func(ast.Node, bool, bool) error
	visit = func(n ast.Node, bold, italic bool) error {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		appendText := func(s string) {
			if s != "" {
				runs = append(runs, docx.Run{Text: s, Bold: bold, Italic: italic})
				visible.WriteString(s)
			}
		}
		switch v := n.(type) {
		case *ast.Text:
			s := string(v.Segment.Value(p.source))
			s = html.UnescapeString(string(util.UnescapePunctuations([]byte(s))))
			if v.SoftLineBreak() || v.HardLineBreak() {
				s += "\n"
			}
			appendText(s)
			return nil
		case *ast.String:
			appendText(string(v.Value))
			return nil
		case *ast.AutoLink:
			appendText(string(v.Label(p.source)))
			return nil
		case *ast.CodeSpan:
			var b strings.Builder
			for c := v.FirstChild(); c != nil; c = c.NextSibling() {
				t := c.(*ast.Text)
				b.Write(t.Segment.Value(p.source))
			}
			// Code is literal, not an include directive. Break detection across it.
			visible.WriteByte(0)
			runs = append(runs, docx.Run{Text: strings.ReplaceAll(b.String(), "\n", " "), Bold: bold, Italic: italic})
			visible.WriteByte(0)
			return nil
		case *ast.Emphasis:
			if v.Level == 2 {
				bold = true
			} else {
				italic = true
			}
		case *ast.Link:
		default:
			return p.fail(n, "unsupported inline construct")
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if err := visit(c, bold, italic); err != nil {
				return err
			}
		}
		return nil
	}
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if err := visit(c, false, false); err != nil {
			return nil, err
		}
	}
	// Includes are not standard Markdown nodes. Match the aggregate visible
	// text so emphasis and label-only links cannot split a directive.
	isInclude := func(s string) bool {
		return strings.Contains(s, "!include") || strings.Contains(s, "{{<") || strings.Contains(s, "{{%")
	}
	if isInclude(visible.String()) {
		return nil, p.fail(parent, "includes are unsupported; supply literal content")
	}
	// Goldmark can interpret shortcode delimiters as an autolink. Retain
	// detection of original syntax, but exclude literal inline code spans.
	var raw strings.Builder
	var rawVisit func(ast.Node)
	rawVisit = func(n ast.Node) {
		if _, code := n.(*ast.CodeSpan); code {
			raw.WriteByte(0)
			return
		}
		if t, ok := n.(*ast.Text); ok {
			raw.Write(t.Segment.Value(p.source))
		}
		if a, ok := n.(*ast.AutoLink); ok {
			raw.WriteByte('<')
			raw.Write(a.Label(p.source))
			raw.WriteByte('>')
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			rawVisit(c)
		}
	}
	rawVisit(parent)
	if isInclude(raw.String()) {
		return nil, p.fail(parent, "includes are unsupported; supply literal content")
	}
	// Parser fragments with identical formatting should share one text run so
	// the DOCX editor can find text across punctuation, labels and inline code.
	coalesced := runs[:0]
	for i := 0; i < len(runs); {
		run := runs[i]
		var text strings.Builder
		for i < len(runs) && runs[i].Bold == run.Bold && runs[i].Italic == run.Italic {
			if err := p.ctx.Err(); err != nil {
				return nil, err
			}
			text.WriteString(runs[i].Text)
			endsLine := strings.HasSuffix(runs[i].Text, "\n")
			i++
			// Keep semantic line-break boundaries in separate runs.
			if endsLine {
				break
			}
		}
		run.Text = text.String()
		coalesced = append(coalesced, run)
	}
	return coalesced, nil
}
func (p *markdownParser) block(n ast.Node, indent int) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	switch v := n.(type) {
	case *ast.Heading:
		r, e := p.inline(v)
		if e != nil {
			return e
		}
		p.add(docx.Heading{Level: v.Level, Runs: r}, pdf.Heading{Level: v.Level, Runs: pdfRuns(r)})
	case *ast.Paragraph, *ast.TextBlock:
		r, e := p.inline(n)
		if e != nil {
			return e
		}
		p.add(docx.Paragraph{Runs: r}, pdf.Paragraph{Runs: pdfRuns(r)})
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		var b strings.Builder
		for i := 0; i < n.Lines().Len(); i++ {
			seg := n.Lines().At(i)
			b.Write(seg.Value(p.source))
		}
		s := strings.TrimSuffix(b.String(), "\n")
		p.add(docx.Paragraph{Text: s}, pdf.Paragraph{Text: s})
	case *ast.List:
		if v.ChildCount() > 100 {
			return p.fail(n, "list exceeds 100 items")
		}
		number := v.Start
		for item := v.FirstChild(); item != nil; item = item.NextSibling() {
			// Reject marker-only items, including parents containing only a nested
			// list, rather than silently dropping their marker or returning no file.
			firstText := item.FirstChild()
			if firstText == nil || (firstText.Kind() != ast.KindParagraph && firstText.Kind() != ast.KindTextBlock) {
				return p.fail(item, "empty list item; supply text after each marker")
			}
			first := true
			for c := item.FirstChild(); c != nil; c = c.NextSibling() {
				if c.Kind() == ast.KindList {
					if err := p.block(c, indent+1); err != nil {
						return err
					}
					continue
				}
				if c.Kind() != ast.KindParagraph && c.Kind() != ast.KindTextBlock {
					return p.fail(c, "list items must contain text or nested lists")
				}
				r, e := p.inline(c)
				if e != nil {
					return e
				}
				if first {
					if strings.TrimSpace(runText(r)) == "" {
						return p.fail(item, "empty list item; supply text after each marker")
					}
					p.add(docx.Bullets{RichItems: [][]docx.Run{r}, Ordered: v.IsOrdered(), Start: number, StartSet: true, Indent: indent}, pdf.Bullets{RichItems: [][]pdf.Run{pdfRuns(r)}, Ordered: v.IsOrdered(), Start: number, StartSet: true, Indent: indent})
					first = false
				} else {
					p.add(docx.Paragraph{Runs: r}, pdf.Paragraph{Runs: pdfRuns(r)})
				}
			}
			number++
		}
	case *east.Table:
		cols := len(v.Alignments)
		if cols > 20 || v.ChildCount()-1 > 500 {
			return p.fail(n, "table exceeds 20 columns or 500 body rows")
		}
		d := docx.Table{}
		b := pdf.Table{}
		for row := v.FirstChild(); row != nil; row = row.NextSibling() {
			// Goldmark pads missing cells and drops excess ones. Check original row
			// delimiters so ragged input never silently loses or fabricates cells.
			if !p.rectangular(row, cols) {
				return p.fail(row, "table must be rectangular")
			}
			var values []string
			var rich [][]docx.Run
			var prich [][]pdf.Run
			for c := row.FirstChild(); c != nil; c = c.NextSibling() {
				r, e := p.inline(c)
				if e != nil {
					return e
				}
				values = append(values, runText(r))
				rich = append(rich, r)
				prich = append(prich, pdfRuns(r))
			}
			if row.Kind() == east.KindTableHeader {
				d.Header = values
				d.HeaderRuns = rich
				b.Header = values
				b.HeaderRuns = prich
			} else {
				d.Rows = append(d.Rows, values)
				d.RowRuns = append(d.RowRuns, rich)
				b.Rows = append(b.Rows, values)
				b.RowRuns = append(b.RowRuns, prich)
			}
		}
		p.add(d, b)
	default:
		return p.fail(n, "unsupported block")
	}
	return nil
}
func (p *markdownParser) rectangular(row ast.Node, cols int) bool {
	if row.ChildCount() != cols {
		return false
	}
	first := row.FirstChild()
	if first == nil || first.Lines().Len() == 0 {
		return false
	}
	seg := first.Lines().At(0)
	start := seg.Start
	for start > 0 && p.source[start-1] != '\n' {
		start--
	}
	end := seg.Start
	for end < len(p.source) && p.source[end] != '\n' {
		end++
	}
	line := strings.TrimSpace(string(p.source[start:end]))
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") && !(len(line) > 1 && line[len(line)-2] == '\\') {
		line = line[:len(line)-1]
	}
	count := 1
	for i := 0; i < len(line); i++ {
		if line[i] == '|' && (i == 0 || line[i-1] != '\\') {
			count++
		}
	}
	return count == cols
}
