// SPDX-License-Identifier: MIT
package textinput

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type callbackBlock struct {
	parser.BlockParser
	after func()
}

func (b *callbackBlock) Open(n ast.Node, r text.Reader, c parser.Context) (ast.Node, parser.State) {
	n, s := b.BlockParser.Open(n, r, c)
	b.after()
	return n, s
}

type callbackInline struct {
	parser.InlineParser
	after func(ast.Node)
}

func (b *callbackInline) Parse(n ast.Node, r text.Reader, c parser.Context) ast.Node {
	v := b.InlineParser.Parse(n, r, c)
	b.after(v)
	return v
}

type callbackDelimiter struct {
	parser.DelimiterProcessor
	after func()
}

func (d *callbackDelimiter) OnMatch(n int) ast.Node {
	v := d.DelimiterProcessor.OnMatch(n)
	d.after()
	return v
}

type callbackParagraph struct{ called *bool }

func (p callbackParagraph) Transform(*ast.Paragraph, text.Reader, parser.Context) { *p.called = true }

func guardTable(cols, rows int, header string) string {
	if header == "" {
		header = "h"
	}
	line := "|" + strings.Repeat(header+"|", cols) + "\n"
	return line + "|" + strings.Repeat("---|", cols) + "\n" + strings.Repeat("|"+strings.Repeat("v|", cols)+"\n", rows)
}
func guardMaxDepth(root ast.Node) int {
	max, depth := 0, 0
	_ = ast.Walk(root, func(_ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			depth++
			if depth > max {
				max = depth
			}
		} else {
			depth--
		}
		return ast.WalkContinue, nil
	})
	return max
}

var _ = Describe("Synchronous Markdown parse guards", func() {
	It("observes cancellation inside actual block, inline and delimiter callbacks", func() {
		for _, phase := range []string{"block", "inline", "delimiter"} {
			ctx, cancel := context.WithCancel(context.Background())
			called := false
			root, err := runGuardedParse(ctx, []byte("*value*"), func(p parser.Parser) {
				switch phase {
				case "block":
					p.AddOptions(parser.WithBlockParsers(util.Prioritized(&guardBlock{BlockParser: &callbackBlock{BlockParser: parser.NewParagraphParser(), after: func() { called = true; cancel() }}, g: &parseGuard{ctx: ctx}}, -10)))
				case "inline":
					p.AddOptions(parser.WithInlineParsers(util.Prioritized(&guardInline{InlineParser: &callbackInline{InlineParser: parser.NewEmphasisParser(), after: func(ast.Node) { called = true; cancel() }}, g: &parseGuard{ctx: ctx}}, -10)))
				case "delimiter":
					p.AddOptions(parser.WithInlineParsers(util.Prioritized(&callbackInline{InlineParser: parser.NewEmphasisParser(), after: func(n ast.Node) {
						if d, ok := n.(*parser.Delimiter); ok {
							g := d.Processor.(*guardDelimiter)
							g.DelimiterProcessor = &callbackDelimiter{DelimiterProcessor: g.DelimiterProcessor, after: func() { called = true; cancel() }}
						}
					}}, -10)))
				}
			})
			cancel()
			Expect(called).To(BeTrue(), phase)
			Expect(root).To(BeNil(), phase)
			Expect(errors.Is(err, context.Canceled)).To(BeTrue(), phase)
		}
	})
	It("rejects the first overdepth delimiter match but accepts depth 64", func() {
		for _, marker := range []string{"*", "_"} {
			for _, depth := range []int{64, 65, 1000} {
				source := strings.Repeat(marker, 2*(depth-3)) + "x" + strings.Repeat(marker, 2*(depth-3))
				root, err := guardedParse(context.Background(), []byte(source))
				if depth == 64 {
					Expect(err).NotTo(HaveOccurred())
					Expect(guardMaxDepth(root)).To(Equal(64))
					_, err = ParseMarkdown(context.Background(), source)
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(MatchError(ContainSubstring("nesting exceeds 64")))
					Expect(root).To(BeNil())
				}
			}
		}
	})
	It("accepts flat delimiter pairs and mixed legal nesting without counting total matches as depth", func() {
		for _, source := range []string{strings.Repeat("*a* **b** _c_ __d__ ", 1000), "**a *b _c_ b* a**", "***x***", strings.Repeat("*", 121) + "x" + strings.Repeat("*", 121)} {
			root, err := guardedParse(context.Background(), []byte(source))
			Expect(err).NotTo(HaveOccurred())
			Expect(guardMaxDepth(root)).To(BeNumerically("<=", 64))
			_, err = ParseMarkdown(context.Background(), source)
			Expect(err).NotTo(HaveOccurred())
		}
	})
	It("enforces depth on block openings, including mixed block and inline nesting", func() {
		for _, depth := range []int{64, 65} {
			// Document + block quotes + paragraph + text.
			source := strings.Repeat("> ", depth-3) + "x\n"
			root, err := guardedParse(context.Background(), []byte(source))
			if depth == 64 {
				Expect(err).NotTo(HaveOccurred())
				Expect(guardMaxDepth(root)).To(Equal(depth))
			} else {
				Expect(err).To(MatchError(ContainSubstring("nesting exceeds 64")))
			}
		}
		_, err := guardedParse(context.Background(), []byte(strings.Repeat("> ", 60)+"****x****"))
		Expect(err).To(MatchError(ContainSubstring("nesting exceeds 64")))
	})
	It("rejects oversize tables before invoking the allocating table transformer", func() {
		for _, dimensions := range [][2]int{{21, 1}, {1, 501}, {20000, 1}} {
			source := []byte(guardTable(dimensions[0], dimensions[1], ""))
			root := goldmark.DefaultParser().Parse(text.NewReader(source))
			paragraph := root.FirstChild().(*ast.Paragraph)
			called := false
			g := &parseGuard{ctx: context.Background()}
			transform := &guardParagraph{ParagraphTransformer: callbackParagraph{&called}, g: g, table: true}
			Expect(func() { transform.Transform(paragraph, text.NewReader(source), parser.NewContext()) }).To(Panic())
			Expect(called).To(BeFalse())
		}
	})
	It("keeps oversized-looking non-tables as Goldmark paragraphs", func() {
		for _, source := range []string{
			"|" + strings.Repeat("heading|", 21) + "\n|---|\n",
			"|a|b|\n|---|\n" + strings.Repeat("|value|\n", 501),
			"|" + strings.Repeat("heading|", 22) + "\n|" + strings.Repeat("---|", 21) + "\n",
			// A mismatched first delimiter stops Goldmark, even when a later
			// delimiter could form an oversized table.
			"|a|b|\n|---|\n|" + strings.Repeat("---|", 21) + "\n",
			// Unicode whitespace is content, not Goldmark's row-edge whitespace.
			"\u00a0|" + strings.Repeat("h|", 20) + "\n|" + strings.Repeat("---|", 20) + "\n" + strings.Repeat("|v|\n", 501),
		} {
			plain := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader([]byte(source)))
			Expect(plain.FirstChild().Kind()).To(Equal(ast.KindParagraph), source)
			root, err := guardedParse(context.Background(), []byte(source))
			Expect(err).NotTo(HaveOccurred(), source)
			Expect(root.FirstChild().Kind()).To(Equal(ast.KindParagraph), source)
			_, err = ParseMarkdown(context.Background(), source)
			Expect(err).NotTo(HaveOccurred(), source)
		}
	})
	It("matches Goldmark row counting and short-header padding before table limits", func() {
		for _, header := range []string{"h", "|h|", " |h|| 	", `|a\|b|`, `|a\\|b|`, `|a\|`, "|", "|" + strings.Repeat("h|", 20) + "|", "|" + strings.Repeat("h|", 19) + `a\|`} {
			source := header + "\n|" + strings.Repeat("---|", 20) + "\n|v|\n"
			plain := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader([]byte(source)))
			Expect(plain.FirstChild().Kind().String()).To(Equal("Table"), source)
			Expect(plain.FirstChild().FirstChild().ChildCount()).To(Equal(20), source)
			root, err := guardedParse(context.Background(), []byte(source))
			Expect(err).NotTo(HaveOccurred(), source)
			Expect(root.FirstChild().FirstChild().ChildCount()).To(Equal(20), source)
			// Padding is parser behavior only; the converter still rejects
			// nonrectangular source instead of silently fabricating cells.
			_, err = ParseMarkdown(context.Background(), source)
			Expect(err).To(MatchError(ContainSubstring("table must be rectangular")), source)
		}
		for _, source := range []string{
			"h\n|" + strings.Repeat("---|", 21) + "\n",
			"h\n|---|---|\n" + strings.Repeat("|v|\n", 501),
		} {
			plain := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader([]byte(source)))
			Expect(plain.FirstChild().Kind().String()).To(Equal("Table"), source)
			_, err := guardedParse(context.Background(), []byte(source))
			Expect(err).To(MatchError(ContainSubstring("table exceeds")), source)
			// The callback stands in for the allocating Goldmark transformer.
			root := goldmark.DefaultParser().Parse(text.NewReader([]byte(source)))
			called := false
			transform := &guardParagraph{ParagraphTransformer: callbackParagraph{&called}, g: &parseGuard{ctx: context.Background()}, table: true}
			Expect(func() {
				transform.Transform(root.FirstChild().(*ast.Paragraph), text.NewReader([]byte(source)), parser.NewContext())
			}).To(Panic())
			Expect(called).To(BeFalse())
		}
	})
	It("accepts the exact table dimensions and escaped pipes in legal headers", func() {
		for _, source := range []string{guardTable(20, 1, ""), guardTable(1, 500, ""), guardTable(20, 1, `a\|b`)} {
			_, err := ParseMarkdown(context.Background(), source)
			Expect(err).NotTo(HaveOccurred())
		}
		// The dimension guards are independent of the later 2000 semantic-block limit.
		_, err := guardedParse(context.Background(), []byte(guardTable(20, 500, "")))
		Expect(err).NotTo(HaveOccurred())
	})
	It("preserves link CloseBlocker cleanup and label-only semantics", func() {
		for _, source := range []string{"[label](https://invalid.example)", "[label][ref]\n\n[ref]: https://invalid.example", "unmatched [label", "[outer [inner](target)](other)", "[**bold** *italic*](target)"} {
			guarded, err := guardedParse(context.Background(), []byte(source))
			Expect(err).NotTo(HaveOccurred())
			plain := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader([]byte(source)))
			var shape func(ast.Node) string
			shape = func(n ast.Node) string {
				s := n.Kind().String() + "("
				for c := n.FirstChild(); c != nil; c = c.NextSibling() {
					s += shape(c)
				}
				return s + ")"
			}
			Expect(shape(guarded)).To(Equal(shape(plain)), source)
			_, err = ParseMarkdown(context.Background(), source)
			Expect(err).NotTo(HaveOccurred())
		}
	})
	It("rethrows unrelated callback panics unchanged", func() {
		sentinel := &struct{ message string }{"not a guard abort"}
		Expect(func() {
			_, _ = runGuardedParse(context.Background(), []byte("text"), func(parser.Parser) { panic(sentinel) })
		}).To(PanicWith(sentinel))
		Expect(func() {
			_, _ = runGuardedParse(context.Background(), []byte("text"), func(p parser.Parser) {
				p.AddOptions(parser.WithBlockParsers(util.Prioritized(&callbackBlock{BlockParser: parser.NewParagraphParser(), after: func() { panic(sentinel) }}, -10)))
			})
		}).To(PanicWith(sentinel))
	})
	It("isolates cancellation, delimiter caches and parser contexts across concurrent parses", func() {
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if i%2 == 0 {
					cancel()
				}
				_, err := ParseMarkdown(ctx, "**bold *italic***\n\n"+guardTable(2, 2, `a\|b`))
				if i%2 == 0 {
					if !errors.Is(err, context.Canceled) {
						errs <- fmt.Errorf("cancelled parse: %v", err)
					}
				} else if err != nil {
					errs <- err
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		Expect(errs).To(BeEmpty())
	})
})
