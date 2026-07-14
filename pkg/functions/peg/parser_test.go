package peg_test

import (
	"github.com/mudler/LocalAI/pkg/functions/peg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func extractTags(ast *peg.AstArena, result *peg.ParseResult) map[string]string {
	tags := make(map[string]string)
	ast.VisitResult(result, func(node *peg.AstNode) {
		if node.Tag != "" {
			tags[node.Tag] = node.Text
		}
	})
	return tags
}

var _ = Describe("PEG Parser", func() {
	Context("LiteralParser", func() {
		It("succeeds on exact match", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Literal("hello")
			})
			ctx := peg.NewParseContext("hello world", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.Start).To(Equal(0))
			Expect(r.End).To(Equal(5))
		})

		It("fails on mismatch", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Literal("hello")
			})
			ctx := peg.NewParseContext("world", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Fail))
		})

		It("returns NeedMoreInput in partial mode", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Literal("hello")
			})
			ctx := peg.NewParseContext("hel", true)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.NeedMoreInput))
		})

		It("fails on partial input when not in partial mode", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Literal("hello")
			})
			ctx := peg.NewParseContext("hel", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Fail))
		})
	})

	Context("SequenceParser", func() {
		It("matches full sequence", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("hello"), b.Literal(" "), b.Literal("world"))
			})
			ctx := peg.NewParseContext("hello world", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(11))
		})

		It("fails midway", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("hello"), b.Literal("X"))
			})
			ctx := peg.NewParseContext("hello world", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Fail))
		})

		It("returns NeedMoreInput at boundary in partial mode", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("hello"), b.Literal(" world"))
			})
			ctx := peg.NewParseContext("hello wo", true)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.NeedMoreInput))
		})
	})

	Context("ChoiceParser", func() {
		It("matches first alternative", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Choice(b.Literal("hello"), b.Literal("world"))
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})

		It("matches second alternative", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Choice(b.Literal("hello"), b.Literal("world"))
			})
			ctx := peg.NewParseContext("world", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})

		It("fails when all alternatives fail", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Choice(b.Literal("hello"), b.Literal("world"))
			})
			ctx := peg.NewParseContext("foo", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Fail))
		})
	})

	Context("RepetitionParser", func() {
		It("handles zero or more matches", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.ZeroOrMore(b.Literal("ab"))
			})

			ctx := peg.NewParseContext("ababab", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(6))

			ctx2 := peg.NewParseContext("xyz", false)
			r2 := arena.Parse(ctx2)
			Expect(r2.Type).To(Equal(peg.Success))
			Expect(r2.End).To(Equal(0))
		})

		It("handles one or more matches", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.OneOrMore(b.Literal("ab"))
			})

			ctx := peg.NewParseContext("ababab", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(6))

			ctx2 := peg.NewParseContext("xyz", false)
			r2 := arena.Parse(ctx2)
			Expect(r2.Type).To(Equal(peg.Fail))
		})

		It("handles optional matches", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Optional(b.Literal("hello")), b.Literal("world"))
			})

			ctx := peg.NewParseContext("helloworld", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(10))

			ctx2 := peg.NewParseContext("world", false)
			r2 := arena.Parse(ctx2)
			Expect(r2.Type).To(Equal(peg.Success))
			Expect(r2.End).To(Equal(5))
		})

		It("respects bounded repetition", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Repeat(b.Literal("a"), 2, 4)
			})

			ctx := peg.NewParseContext("aaaaa", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(4))

			ctx2 := peg.NewParseContext("a", false)
			r2 := arena.Parse(ctx2)
			Expect(r2.Type).To(Equal(peg.Fail))
		})
	})

	Context("Lookahead", func() {
		It("succeeds with positive lookahead", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Peek(b.Literal("hello")), b.Literal("hello"))
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})

		It("fails with positive lookahead mismatch", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Peek(b.Literal("world")), b.Literal("hello"))
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Fail))
		})

		It("succeeds with negative lookahead", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Negate(b.Literal("world")), b.Literal("hello"))
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})

		It("fails with negative lookahead match", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Negate(b.Literal("hello")), b.Literal("hello"))
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Fail))
		})
	})

	Context("UntilParser", func() {
		It("consumes until single delimiter", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Until("<end>"), b.Literal("<end>"))
			})
			ctx := peg.NewParseContext("content<end>", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(12))
		})

		It("consumes until first of multiple delimiters", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.UntilOneOf("<a>", "<b>")
			})
			ctx := peg.NewParseContext("content<b>more", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(7))
		})

		It("consumes rest of input", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Rest()
			})
			ctx := peg.NewParseContext("everything", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(10))
		})

		It("returns NeedMoreInput in partial mode", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Until("<end>")
			})
			ctx := peg.NewParseContext("content", true)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.NeedMoreInput))
		})
	})

	Context("JSONParser", func() {
		It("parses objects", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			ctx := peg.NewParseContext(`{"key": "value", "num": 42}`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(27))
		})

		It("parses arrays", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			ctx := peg.NewParseContext(`[1, "two", true, null]`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(22))
		})

		It("parses strings with escapes", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			ctx := peg.NewParseContext(`"hello \"world\""`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(17))
		})

		It("parses numbers", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			ctx := peg.NewParseContext(`-123.45e10`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(10))
		})

		It("parses booleans", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			ctx := peg.NewParseContext(`true`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(4))
		})

		It("parses null", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			ctx := peg.NewParseContext(`null`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(4))
		})

		It("parses nested structures", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.JSON()
			})
			input := `{"a": [1, {"b": true}], "c": null}`
			ctx := peg.NewParseContext(input, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(len(input)))
		})
	})

	Context("Tag extraction", func() {
		It("extracts basic tags", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(
					b.Tag("greeting", b.Until(" ")),
					b.Literal(" "),
					b.Tag("name", b.Rest()),
				)
			})
			ctx := peg.NewParseContext("Hello World", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))

			tags := extractTags(&ctx.Ast, &r)
			Expect(tags["greeting"]).To(Equal("Hello"))
			Expect(tags["name"]).To(Equal("World"))
		})

		It("extracts structured tags", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(
					b.Tag("header", b.Until("\n")),
					b.Literal("\n"),
					b.Tag("body", b.Rest()),
				)
			})
			ctx := peg.NewParseContext("Title\nBody content here", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))

			tags := extractTags(&ctx.Ast, &r)
			Expect(tags["header"]).To(Equal("Title"))
			Expect(tags["body"]).To(Equal("Body content here"))
		})

		It("overwrites duplicate tags", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(
					b.Tag("item", b.Until(",")),
					b.Literal(","),
					b.Tag("item", b.Rest()),
				)
			})
			ctx := peg.NewParseContext("first,second", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))

			tags := extractTags(&ctx.Ast, &r)
			Expect(tags["item"]).To(Equal("second"))
		})

		It("returns empty map when no tags", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Rest()
			})
			ctx := peg.NewParseContext("Hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))

			tags := extractTags(&ctx.Ast, &r)
			Expect(tags).To(HaveLen(0))
		})
	})

	Context("Rule and Ref", func() {
		It("handles named rules", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				word := b.Rule("word", b.Chars("[a-z]", 1, -1))
				return b.Seq(word, b.Literal(" "), word)
			})
			ctx := peg.NewParseContext("hello world", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(11))
		})

		It("handles forward references", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				ref := b.Ref("greeting")
				b.Rule("greeting", b.Literal("hello"))
				return ref
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})
	})

	Context("AtomicParser", func() {
		It("suppresses partial AST nodes", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Atomic(b.Tag("test", b.Literal("hello world")))
			})
			ctx := peg.NewParseContext("hello", true)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.NeedMoreInput))
			Expect(r.Nodes).To(HaveLen(0))
		})
	})

	Context("Start and End parsers", func() {
		It("matches at start of input", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Start(), b.Literal("hello"))
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})

		It("matches at end of input", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("hello"), b.End())
			})
			ctx := peg.NewParseContext("hello", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))

			ctx2 := peg.NewParseContext("hello world", false)
			r2 := arena.Parse(ctx2)
			Expect(r2.Type).To(Equal(peg.Fail))
		})
	})

	Context("Partial parsing", func() {
		It("extracts tags during partial parse", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(
					b.Tag("prefix", b.Until(":")),
					b.Literal(":"),
					b.Tag("value", b.Rest()),
				)
			})
			ctx := peg.NewParseContext("key:val", true)
			r := arena.Parse(ctx)
			Expect(r.Type).NotTo(Equal(peg.Fail))

			tags := extractTags(&ctx.Ast, &r)
			Expect(tags["prefix"]).To(Equal("key"))
		})
	})

	Context("ParseAnywhere", func() {
		It("finds pattern in middle of input", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(
					b.Choice(b.Literal("{"), b.Literal(":")),
					b.Space(),
					b.Literal("\""),
					b.Atomic(b.Literal("fun_name")),
				)
			})

			input := `This is a very long jinja template string... <tool_call>{ "fun_name" : { "arg" : 1 }</tool_call>`
			found := false
			for i := range len(input) {
				ctx := peg.NewParseContext(input, false)
				r := arena.ParseFrom(ctx, i)
				if r.Type == peg.Success {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("fails when pattern is not found", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(
					b.Choice(b.Literal("{"), b.Literal(":")),
					b.Space(),
					b.Literal("\""),
					b.Atomic(b.Literal("fun_name")),
				)
			})

			input := `This is a very long jinja template string... <tool_call><fun=fun_name><arg name=arg>1</arg></tool_call>`
			found := false
			for i := range len(input) {
				ctx := peg.NewParseContext(input, false)
				r := arena.ParseFrom(ctx, i)
				if r.Type == peg.Success {
					found = true
					break
				}
			}
			Expect(found).To(BeFalse())
		})
	})

	Context("CharsParser", func() {
		It("matches lowercase letters", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Chars("[a-z]", 1, -1)
			})
			ctx := peg.NewParseContext("hello123", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})

		It("matches negated character class", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Chars("[^0-9]", 1, -1)
			})
			ctx := peg.NewParseContext("hello123", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})
	})

	Context("JSONStringParser", func() {
		It("parses basic strings", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("\""), b.JSONString(), b.Literal("\""))
			})
			ctx := peg.NewParseContext(`"hello world"`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(13))
		})

		It("parses strings with escapes", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("\""), b.JSONString(), b.Literal("\""))
			})
			ctx := peg.NewParseContext(`"hello \"world\""`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(17))
		})

		It("parses strings with unicode escapes", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("\""), b.JSONString(), b.Literal("\""))
			})
			ctx := peg.NewParseContext(`"hello \u0041"`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(14))
		})
	})

	Context("SpaceParser", func() {
		It("matches whitespace", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("a"), b.Space(), b.Literal("b"))
			})
			ctx := peg.NewParseContext("a   b", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(5))
		})

		It("matches zero whitespace", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("a"), b.Space(), b.Literal("b"))
			})
			ctx := peg.NewParseContext("ab", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(2))
		})
	})

	Context("PythonDictStringParser", func() {
		It("parses basic single-quoted strings", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("'"), b.PythonDictString(), b.Literal("'"))
			})
			ctx := peg.NewParseContext("'hello world'", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			Expect(r.End).To(Equal(13))
		})

		It("handles escaped single quotes", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("'"), b.PythonDictString(), b.Literal("'"))
			})
			ctx := peg.NewParseContext(`'it\'s fine'`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})

		It("handles double quotes inside single-quoted strings", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("'"), b.PythonDictString(), b.Literal("'"))
			})
			ctx := peg.NewParseContext(`'He said "hi"'`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})
	})

	Context("peg.ParseCharClassChar", func() {
		It("parses \\x hex escape", func() {
			r, n := peg.ParseCharClassChar(`\x41`, 0)
			Expect(r).To(Equal('A'))
			Expect(n).To(Equal(4))
		})

		It("parses \\u unicode escape", func() {
			r, n := peg.ParseCharClassChar(`\u0041`, 0)
			Expect(r).To(Equal('A'))
			Expect(n).To(Equal(6))
		})

		It("parses \\U unicode escape", func() {
			r, n := peg.ParseCharClassChar(`\U00000041`, 0)
			Expect(r).To(Equal('A'))
			Expect(n).To(Equal(10))
		})

		It("falls back on invalid hex", func() {
			r, n := peg.ParseCharClassChar(`\xZZ`, 0)
			Expect(r).To(Equal('x'))
			Expect(n).To(Equal(2))
		})
	})

	Context("ParseAnywhere method", func() {
		It("finds pattern in middle of input", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Seq(b.Literal("needle"), b.Tag("after", b.Until(".")))
			})
			ctx := peg.NewParseContext("some hay needle found.", false)
			r := arena.ParseAnywhere(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			tags := extractTags(&ctx.Ast, &r)
			Expect(tags["after"]).To(Equal(" found"))
		})

		It("finds function tag with name", func() {
			haystack := "\n<tool_call>\n<function=foofoo>\n<parameter=first>\nXXXX\n</parameter>\n<parameter=second>\nYYYY\n</parameter>\n</function>\n</tool_call>\n"
			needle := "foofoo"

			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Tag("fun_marker", b.Choice(
					b.Seq(
						b.Tag("fun_pre", b.Seq(b.Literal("<"), b.UntilOneOf(">", needle))),
						b.Literal(needle),
						b.Tag("fun_post", b.Seq(
							b.Seq(b.Negate(b.Seq(b.Space(), b.Literal("<"))), b.Until(">"), b.Literal(">")),
						)),
						b.Space(),
					),
					b.Seq(
						b.Tag("fun_pre", b.Seq(b.Literal("["), b.UntilOneOf("]", needle))),
						b.Literal(needle),
						b.Tag("fun_post", b.Seq(
							b.Negate(b.Seq(b.Space(), b.Seq(b.Literal("["), b.Until("]"), b.Literal("]")))),
							b.Space(),
						)),
					),
				))
			})

			ctx := peg.NewParseContext(haystack, false)
			r := arena.ParseAnywhere(ctx)
			Expect(r.Type).To(Equal(peg.Success))
			tags := extractTags(&ctx.Ast, &r)
			Expect(tags["fun_pre"]).To(Equal("<function="))
			Expect(tags["fun_post"]).To(Equal(">"))
		})
	})

	Context("LazyRule", func() {
		It("handles recursive JSON-like structures", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				b.LazyRule("value", func() peg.ParserID {
					str := b.Seq(b.Literal("\""), b.JSONString(), b.Literal("\""))
					arr := b.Seq(
						b.Literal("["), b.Space(),
						b.Ref("value"),
						b.ZeroOrMore(b.Seq(b.Space(), b.Literal(","), b.Space(), b.Ref("value"))),
						b.Space(), b.Literal("]"),
					)
					return b.Choice(str, arr)
				})
				return b.Ref("value")
			})
			ctx := peg.NewParseContext(`["hello",["world","nested"]]`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})

		It("parses python dicts", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.PythonDict()
			})
			ctx := peg.NewParseContext(`{'key': 'value', 'num': 42}`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})

		It("parses nested python values", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.PythonValue()
			})
			ctx := peg.NewParseContext(`{'outer': {'inner': [1, 2, 'three']}}`, false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})

		It("parses python booleans and None", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.PythonValue()
			})
			for _, input := range []string{"True", "False", "None"} {
				ctx := peg.NewParseContext(input, false)
				r := arena.Parse(ctx)
				Expect(r.Type).To(Equal(peg.Success))
			}
		})
	})

	Context("Marker", func() {
		It("matches angle brackets", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Marker()
			})
			ctx := peg.NewParseContext("<tool_call>", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})

		It("matches square brackets", func() {
			arena := peg.BuildPegParser(func(b *peg.Builder) peg.ParserID {
				return b.Marker()
			})
			ctx := peg.NewParseContext("[TOOL]", false)
			r := arena.Parse(ctx)
			Expect(r.Type).To(Equal(peg.Success))
		})
	})
})
