// SPDX-License-Identifier: MIT
package textinput

import (
	"context"
	"errors"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Goldmark's public parser callbacks provide synchronous checkpoints, not a
// deadline on its internal scans. Keep all state local to this parse. In
// particular, allocating a ContextKey per request would mutate Goldmark globals.
type parseAbort struct{ err error }
type parseGuard struct {
	ctx context.Context
	// Only completed delimiter subtrees are cached; open blocks remain mutable.
	heights map[ast.Node]int
}

func (g *parseGuard) poll() {
	if err := g.ctx.Err(); err != nil {
		panic(parseAbort{err})
	}
}
func (g *parseGuard) reject(message string) { panic(parseAbort{errors.New("markdown: " + message)}) }
func (g *parseGuard) depth(n ast.Node) int {
	d := 0
	for ; n != nil; n = n.Parent() {
		g.poll()
		d++
		if d > 64 {
			g.reject("nesting exceeds 64")
		}
	}
	return d
}

// Iterative validation never recursively visits an unchecked subtree.
func (g *parseGuard) check(n ast.Node, parentDepth int) int {
	if n == nil {
		return 0
	}
	type entry struct {
		n     ast.Node
		depth int
	}
	stack := []entry{{n, 1}}
	height := 0
	for len(stack) > 0 {
		g.poll()
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		h := g.heights[e.n]
		if h != 0 {
			if parentDepth+e.depth+h-1 > 64 {
				g.reject("nesting exceeds 64")
			}
			if e.depth+h-1 > height {
				height = e.depth + h - 1
			}
			continue
		}
		if parentDepth+e.depth > 64 {
			g.reject("nesting exceeds 64")
		}
		if e.depth > height {
			height = e.depth
		}
		for c := e.n.FirstChild(); c != nil; c = c.NextSibling() {
			stack = append(stack, entry{c, e.depth + 1})
		}
	}
	return height
}

func guardedParse(ctx context.Context, source []byte) (root ast.Node, err error) {
	return runGuardedParse(ctx, source, nil)
}

// configure is used by package tests to exercise public callback boundaries.
func runGuardedParse(ctx context.Context, source []byte, configure func(parser.Parser)) (root ast.Node, err error) {
	defer func() {
		if v := recover(); v != nil {
			if abort, ok := v.(parseAbort); ok {
				root = nil
				err = abort.err
			} else {
				panic(v)
			}
		}
	}()
	g := &parseGuard{ctx: ctx, heights: make(map[ast.Node]int)}
	blocks := parser.DefaultBlockParsers()
	for i := range blocks {
		blocks[i].Value = &guardBlock{BlockParser: blocks[i].Value.(parser.BlockParser), g: g}
	}
	inlines := parser.DefaultInlineParsers()
	for i := range inlines {
		ip := inlines[i].Value.(parser.InlineParser)
		wrapped := &guardInline{InlineParser: ip, g: g}
		if cb, ok := ip.(parser.CloseBlocker); ok {
			inlines[i].Value = &guardCloseInline{guardInline: wrapped, close: cb}
		} else {
			inlines[i].Value = wrapped
		}
	}
	// Goldmark dispatches the space trigger at the beginning of plain-text lines.
	inlines = append(inlines, util.Prioritized(&guardText{g: g}, 0))
	paragraphs := parser.DefaultParagraphTransformers()
	for i := range paragraphs {
		paragraphs[i].Value = &guardParagraph{ParagraphTransformer: paragraphs[i].Value.(parser.ParagraphTransformer), g: g}
	}
	paragraphs = append(paragraphs, util.Prioritized(&guardParagraph{ParagraphTransformer: extension.NewTableParagraphTransformer(), g: g, table: true}, 200))
	p := parser.NewParser(parser.WithBlockParsers(blocks...), parser.WithInlineParsers(inlines...), parser.WithParagraphTransformers(paragraphs...), parser.WithASTTransformers(util.Prioritized(&guardAST{ASTTransformer: extension.NewTableASTTransformer(), g: g}, 0)))
	if configure != nil {
		configure(p)
	}
	g.poll()
	root = p.Parse(text.NewReader(source), parser.WithContext(&guardContext{Context: parser.NewContext(), g: g}))
	g.poll()
	g.check(root, 0)
	return root, nil
}

type guardBlock struct {
	parser.BlockParser
	g *parseGuard
}

func (b *guardBlock) Open(parent ast.Node, r text.Reader, pc parser.Context) (ast.Node, parser.State) {
	b.g.poll()
	n, s := b.BlockParser.Open(parent, r, pc)
	b.g.poll()
	b.g.check(n, b.g.depth(parent))
	return n, s
}
func (b *guardBlock) Continue(n ast.Node, r text.Reader, pc parser.Context) parser.State {
	b.g.poll()
	s := b.BlockParser.Continue(n, r, pc)
	b.g.poll()
	return s
}
func (b *guardBlock) Close(n ast.Node, r text.Reader, pc parser.Context) {
	b.g.poll()
	b.BlockParser.Close(n, r, pc)
	b.g.poll()
}

type guardInline struct {
	parser.InlineParser
	g *parseGuard
}

func (b *guardInline) Parse(parent ast.Node, r text.Reader, pc parser.Context) ast.Node {
	b.g.poll()
	n := b.InlineParser.Parse(parent, r, pc)
	b.g.poll()
	b.g.check(n, b.g.depth(parent))
	return n
}

type guardCloseInline struct {
	*guardInline
	close parser.CloseBlocker
}

func (b *guardCloseInline) CloseBlock(parent ast.Node, r text.Reader, pc parser.Context) {
	b.g.poll()
	b.close.CloseBlock(parent, r, pc)
	b.g.poll()
	b.g.check(parent, b.g.depth(parent.Parent()))
}

type guardText struct{ g *parseGuard }

func (b *guardText) Trigger() []byte { return []byte{' '} }
func (b *guardText) Parse(parent ast.Node, r text.Reader, pc parser.Context) ast.Node {
	b.g.poll()
	if b.g.depth(parent)+1 > 64 {
		b.g.reject("nesting exceeds 64")
	}
	return nil
}

type guardContext struct {
	parser.Context
	g *parseGuard
}

func (c *guardContext) SetOpenedBlocks(blocks []parser.Block) {
	c.g.poll()
	if len(blocks) > 64 {
		c.g.reject("nesting exceeds 64")
	}
	if len(blocks) > 0 {
		c.g.depth(blocks[len(blocks)-1].Node)
	}
	c.Context.SetOpenedBlocks(blocks)
}
func (c *guardContext) PushDelimiter(d *parser.Delimiter) {
	c.g.poll()
	d.Processor = &guardDelimiter{DelimiterProcessor: d.Processor, g: c.g}
	c.Context.PushDelimiter(d)
}

type guardDelimiter struct {
	parser.DelimiterProcessor
	g              *parseGuard
	opener, closer *parser.Delimiter
}

func (d *guardDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	d.g.poll()
	ok := d.DelimiterProcessor.CanOpenCloser(opener, closer)
	if ok {
		d.opener = opener
		d.closer = closer
	}
	return ok
}
func (d *guardDelimiter) OnMatch(consumes int) ast.Node {
	d.g.poll()
	parentDepth := d.g.depth(d.opener.Parent())
	height := 0
	for c := d.opener.NextSibling(); c != nil && c != d.closer; c = c.NextSibling() {
		h := d.g.check(c, parentDepth+1)
		if h > height {
			height = h
		}
	}
	if parentDepth+1+height > 64 {
		d.g.reject("nesting exceeds 64")
	}
	n := d.DelimiterProcessor.OnMatch(consumes)
	d.g.check(n, parentDepth)
	d.g.heights[n] = height + 1
	return n
}

type guardParagraph struct {
	parser.ParagraphTransformer
	g     *parseGuard
	table bool
}

func (b *guardParagraph) Transform(n *ast.Paragraph, r text.Reader, pc parser.Context) {
	b.g.poll()
	parent, prev, next := n.Parent(), n.PreviousSibling(), n.NextSibling()
	if b.table {
		b.preflight(n, r)
	}
	b.ParagraphTransformer.Transform(n, r, pc)
	b.g.poll()
	// The transformer may detach n; anchor the changed sibling range first.
	if parent == nil {
		return
	}
	first := parent.FirstChild()
	if prev != nil {
		first = prev.NextSibling()
	}
	depth := b.g.depth(parent)
	for c := first; c != nil && c != next; c = c.NextSibling() {
		b.g.check(c, depth)
	}
}

// Recognize only Goldmark's table-delimiter syntax, before its parseRow pads
// every body row to the delimiter width. Do not allocate a split of huge rows.
func (b *guardParagraph) preflight(n *ast.Paragraph, r text.Reader) {
	source := r.Source()
	lines := n.Lines()
	for i := 1; i < lines.Len(); i++ {
		b.g.poll()
		segment := lines.At(i)
		line := segment.Value(source)
		if w, _ := util.IndentWidth(line, 0); w > 3 {
			continue
		}
		valid := true
		for _, c := range line {
			if !(util.IsSpace(c) || c == '-' || c == '|' || c == ':') {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		s := strings.Trim(string(line), " \t\n\r")
		s = strings.TrimPrefix(s, "|")
		s = strings.TrimSuffix(s, "|")
		cols := 0
		for {
			cell, rest, more := strings.Cut(s, "|")
			cell = strings.Trim(cell, " \t\n\r")
			cell = strings.TrimPrefix(cell, ":")
			cell = strings.TrimSuffix(cell, ":")
			if cell == "" {
				valid = false
				break
			}
			for _, c := range cell {
				if c != '-' {
					valid = false
					break
				}
			}
			if !valid {
				break
			}
			cols++
			if !more {
				break
			}
			s = rest
		}
		if !valid {
			continue
		}
		headerSegment := lines.At(i - 1)
		headerSegment = headerSegment.TrimLeftSpace(source)
		headerSegment = headerSegment.TrimRightSpace(source)
		header := headerSegment.Value(source)
		pos, limit := 0, len(header)
		if limit > 0 && header[0] == '|' {
			pos++
		}
		if limit > 0 && header[limit-1] == '|' {
			limit--
		}
		// Mirror Goldmark's parseRow, including its unconditional trailing-pipe
		// removal and lack of an extra cell after a final separator. It pads
		// short headers, but a wider header stops the transformer altogether.
		headerCols := 0
		for pos < limit {
			b.g.poll()
			headerCols++
			for pos < limit && (header[pos] != '|' || (pos > 0 && header[pos-1] == '\\')) {
				pos++
			}
			pos++
		}
		if headerCols > cols {
			return
		}
		if cols > 20 || lines.Len()-i-1 > 500 {
			b.g.reject("table exceeds 20 columns or 500 body rows")
		}
		if b.g.depth(n.Parent())+3 > 64 {
			b.g.reject("nesting exceeds 64")
		}
		return
	}
}

type guardAST struct {
	parser.ASTTransformer
	g *parseGuard
}

func (b *guardAST) Transform(n *ast.Document, r text.Reader, pc parser.Context) {
	b.g.poll()
	b.g.check(n, 0)
	b.ASTTransformer.Transform(n, r, pc)
	b.g.poll()
	b.g.check(n, 0)
}
