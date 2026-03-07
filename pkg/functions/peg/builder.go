package peg

import "regexp"

var invalidRuleCharsRe = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

// Builder provides a fluent API for constructing parsers.
type Builder struct {
	arena Arena
}

func NewBuilder() *Builder {
	return &Builder{
		arena: Arena{
			rules: make(map[string]ParserID),
			root:  InvalidParserID,
		},
	}
}

func (b *Builder) add(p Parser) ParserID {
	return b.arena.addParser(p)
}

// Eps matches nothing, always succeeds.
func (b *Builder) Eps() ParserID {
	return b.add(&EpsilonParser{})
}

// Start matches start of input.
func (b *Builder) Start() ParserID {
	return b.add(&StartParser{})
}

// End matches end of input.
func (b *Builder) End() ParserID {
	return b.add(&EndParser{})
}

// Literal matches an exact string.
func (b *Builder) Literal(s string) ParserID {
	return b.add(&LiteralParser{Literal: s})
}

// Seq matches a sequence of parsers in order.
func (b *Builder) Seq(children ...ParserID) ParserID {
	// Flatten nested sequences
	var flattened []ParserID
	for _, id := range children {
		if seq, ok := b.arena.parsers[id].(*SequenceParser); ok {
			flattened = append(flattened, seq.Children...)
		} else {
			flattened = append(flattened, id)
		}
	}
	return b.add(&SequenceParser{Children: flattened})
}

// Choice tries alternatives until one succeeds.
func (b *Builder) Choice(children ...ParserID) ParserID {
	// Flatten nested choices
	var flattened []ParserID
	for _, id := range children {
		if ch, ok := b.arena.parsers[id].(*ChoiceParser); ok {
			flattened = append(flattened, ch.Children...)
		} else {
			flattened = append(flattened, id)
		}
	}
	return b.add(&ChoiceParser{Children: flattened})
}

// Optional matches zero or one occurrence.
func (b *Builder) Optional(child ParserID) ParserID {
	return b.Repeat(child, 0, 1)
}

// ZeroOrMore matches zero or more occurrences.
func (b *Builder) ZeroOrMore(child ParserID) ParserID {
	return b.Repeat(child, 0, -1)
}

// OneOrMore matches one or more occurrences.
func (b *Builder) OneOrMore(child ParserID) ParserID {
	return b.Repeat(child, 1, -1)
}

// Repeat matches between min and max times. Use -1 for unbounded max.
func (b *Builder) Repeat(child ParserID, min, max int) ParserID {
	return b.add(&RepetitionParser{Child: child, MinCount: min, MaxCount: max})
}

// Peek is a positive lookahead — succeeds if child succeeds, consumes nothing.
func (b *Builder) Peek(child ParserID) ParserID {
	return b.add(&AndParser{Child: child})
}

// Negate is a negative lookahead — succeeds if child fails, consumes nothing.
func (b *Builder) Negate(child ParserID) ParserID {
	return b.add(&NotParser{Child: child})
}

// Any matches a single UTF-8 codepoint.
func (b *Builder) Any() ParserID {
	return b.add(&AnyParser{})
}

// Space matches zero or more whitespace characters.
func (b *Builder) Space() ParserID {
	return b.add(&SpaceParser{})
}

// Chars matches characters from a character class expression like "[a-z]".
func (b *Builder) Chars(classes string, min, max int) ParserID {
	ranges, negated := parseCharClasses(classes)
	return b.add(&CharsParser{
		Pattern:  classes,
		Ranges:   ranges,
		Negated:  negated,
		MinCount: min,
		MaxCount: max,
	})
}

// Until matches all characters until a delimiter is found (not consumed).
func (b *Builder) Until(delimiter string) ParserID {
	return b.add(&UntilParser{Delimiters: []string{delimiter}})
}

// UntilOneOf matches until any of the delimiters is found.
func (b *Builder) UntilOneOf(delimiters ...string) ParserID {
	return b.add(&UntilParser{Delimiters: delimiters})
}

// Rest matches everything to end of input.
func (b *Builder) Rest() ParserID {
	return b.add(&UntilParser{Delimiters: nil})
}

// JSONString matches JSON string content (without surrounding quotes).
func (b *Builder) JSONString() ParserID {
	return b.add(&JSONStringParser{})
}

// JSON matches a complete JSON value.
func (b *Builder) JSON() ParserID {
	return b.add(&JSONParser{})
}

// JSONNumber matches a JSON number.
func (b *Builder) JSONNumber() ParserID {
	// We implement this as a dedicated parser entry that delegates to parseJSONNumber
	return b.add(&jsonNumberParser{})
}

// PythonDictString matches single-quoted string content (without quotes).
func (b *Builder) PythonDictString() ParserID {
	return b.add(&PythonDictStringParser{})
}

// DoubleQuotedString matches a double-quoted string: "content" + space
func (b *Builder) DoubleQuotedString() ParserID {
	return b.LazyRule("dq-string", func() ParserID {
		return b.Seq(b.Literal(`"`), b.JSONString(), b.Literal(`"`), b.Space())
	})
}

// SingleQuotedString matches a single-quoted string: 'content' + space
func (b *Builder) SingleQuotedString() ParserID {
	return b.LazyRule("sq-string", func() ParserID {
		return b.Seq(b.Literal("'"), b.PythonDictString(), b.Literal("'"), b.Space())
	})
}

// FlexibleString matches either a double or single-quoted string.
func (b *Builder) FlexibleString() ParserID {
	return b.LazyRule("flexible-string", func() ParserID {
		return b.Choice(b.DoubleQuotedString(), b.SingleQuotedString())
	})
}

// Marker matches <...> or [...] delimited text.
func (b *Builder) Marker() ParserID {
	return b.Choice(
		b.Seq(b.Literal("<"), b.Until(">"), b.Literal(">")),
		b.Seq(b.Literal("["), b.Until("]"), b.Literal("]")),
	)
}

// PythonValue matches a Python-style value (dict, array, string, number, bool, None).
func (b *Builder) PythonValue() ParserID {
	return b.LazyRule("python-value", func() ParserID {
		return b.Choice(
			b.PythonDict(), b.PythonArray(), b.PythonString(),
			b.JSONNumber(), b.PythonBool(), b.PythonNull(),
		)
	})
}

// PythonString matches a Python string (double or single-quoted).
func (b *Builder) PythonString() ParserID {
	return b.LazyRule("python-string", func() ParserID {
		return b.Choice(b.DoubleQuotedString(), b.SingleQuotedString())
	})
}

// PythonBool matches True or False.
func (b *Builder) PythonBool() ParserID {
	return b.LazyRule("python-bool", func() ParserID {
		return b.Seq(b.Choice(b.Literal("True"), b.Literal("False")), b.Space())
	})
}

// PythonNull matches None.
func (b *Builder) PythonNull() ParserID {
	return b.LazyRule("python-none", func() ParserID {
		return b.Seq(b.Literal("None"), b.Space())
	})
}

// PythonDict matches a Python dictionary {key: value, ...}.
func (b *Builder) PythonDict() ParserID {
	return b.LazyRule("python-dict", func() ParserID {
		member := b.Seq(b.PythonString(), b.Space(), b.Literal(":"), b.Space(), b.PythonValue())
		return b.Seq(
			b.Literal("{"), b.Space(),
			b.Optional(b.Seq(member, b.ZeroOrMore(b.Seq(b.Space(), b.Literal(","), b.Space(), member)))),
			b.Space(), b.Literal("}"), b.Space(),
		)
	})
}

// PythonArray matches a Python array [value, ...].
func (b *Builder) PythonArray() ParserID {
	return b.LazyRule("python-array", func() ParserID {
		return b.Seq(
			b.Literal("["), b.Space(),
			b.Optional(b.Seq(b.PythonValue(), b.ZeroOrMore(b.Seq(b.Space(), b.Literal(","), b.Space(), b.PythonValue())))),
			b.Space(), b.Literal("]"), b.Space(),
		)
	})
}

// LazyRule creates a named rule with deferred construction to support recursion.
// If the rule already exists, returns a ref to it. Otherwise, creates a placeholder,
// builds the child, and replaces the placeholder.
func (b *Builder) LazyRule(name string, builderFn func() ParserID) ParserID {
	cleanName := invalidRuleCharsRe.ReplaceAllString(name, "-")
	if _, exists := b.arena.rules[cleanName]; exists {
		return b.add(&RefParser{Name: cleanName})
	}

	// Create placeholder rule to allow recursive references
	placeholderChild := b.add(&AnyParser{})
	ruleID := b.add(&RuleParser{Name: cleanName, Child: placeholderChild})
	b.arena.rules[cleanName] = ruleID

	// Build the actual parser
	child := builderFn()

	// Update the rule with the real child
	b.arena.parsers[ruleID] = &RuleParser{Name: cleanName, Child: child}

	return b.add(&RefParser{Name: cleanName})
}

// Rule creates a named rule and returns a ref to it.
func (b *Builder) Rule(name string, child ParserID) ParserID {
	cleanName := invalidRuleCharsRe.ReplaceAllString(name, "-")
	ruleID := b.add(&RuleParser{Name: cleanName, Child: child})
	b.arena.rules[cleanName] = ruleID
	return b.add(&RefParser{Name: cleanName})
}

// TriggerRule creates a named rule marked as a trigger (for lazy grammar generation).
func (b *Builder) TriggerRule(name string, child ParserID) ParserID {
	cleanName := invalidRuleCharsRe.ReplaceAllString(name, "-")
	ruleID := b.add(&RuleParser{Name: cleanName, Child: child, Trigger: true})
	b.arena.rules[cleanName] = ruleID
	return b.add(&RefParser{Name: cleanName})
}

// Ref creates a forward reference to a named rule.
func (b *Builder) Ref(name string) ParserID {
	return b.add(&RefParser{Name: name})
}

// Atomic creates a parser that suppresses partial AST nodes.
func (b *Builder) Atomic(child ParserID) ParserID {
	return b.add(&AtomicParser{Child: child})
}

// Tag creates a semantic tag in the AST.
func (b *Builder) Tag(tag string, child ParserID) ParserID {
	return b.add(&TagParser{Child: child, Tag: tag})
}

// Schema wraps a parser with schema metadata (pass-through at parse time).
func (b *Builder) Schema(child ParserID, name string) ParserID {
	return b.add(&SchemaParser{Child: child, Name: name})
}

// SetRoot sets the root parser.
func (b *Builder) SetRoot(id ParserID) {
	b.arena.root = id
}

// Build resolves references and returns the arena.
func (b *Builder) Build() *Arena {
	b.arena.resolveRefs()
	arena := b.arena
	// Reset builder
	b.arena = Arena{
		rules: make(map[string]ParserID),
		root:  InvalidParserID,
	}
	return &arena
}

// parseCharClasses parses a character class expression and returns ranges and negation.
func parseCharClasses(classes string) ([]CharRange, bool) {
	content := classes
	negated := false

	if len(content) > 0 && content[0] == '[' {
		content = content[1:]
	}
	if len(content) > 0 && content[len(content)-1] == ']' {
		content = content[:len(content)-1]
	}
	if len(content) > 0 && content[0] == '^' {
		negated = true
		content = content[1:]
	}

	var ranges []CharRange
	i := 0
	for i < len(content) {
		startChar, startLen := ParseCharClassChar(content, i)
		i += startLen

		if i+1 < len(content) && content[i] == '-' {
			endChar, endLen := ParseCharClassChar(content, i+1)
			ranges = append(ranges, CharRange{Start: startChar, End: endChar})
			i += 1 + endLen
		} else {
			ranges = append(ranges, CharRange{Start: startChar, End: startChar})
		}
	}

	return ranges, negated
}

func ParseCharClassChar(content string, pos int) (rune, int) {
	if content[pos] == '\\' && pos+1 < len(content) {
		switch content[pos+1] {
		case 'n':
			return '\n', 2
		case 't':
			return '\t', 2
		case 'r':
			return '\r', 2
		case '\\':
			return '\\', 2
		case ']':
			return ']', 2
		case '[':
			return '[', 2
		case 'x':
			if r, n := parseHexEscape(content, pos+2, 2); n > 0 {
				return r, 2 + n
			}
			return 'x', 2
		case 'u':
			if r, n := parseHexEscape(content, pos+2, 4); n > 0 {
				return r, 2 + n
			}
			return 'u', 2
		case 'U':
			if r, n := parseHexEscape(content, pos+2, 8); n > 0 {
				return r, 2 + n
			}
			return 'U', 2
		default:
			return rune(content[pos+1]), 2
		}
	}
	return rune(content[pos]), 1
}

func parseHexEscape(s string, pos, count int) (rune, int) {
	if pos+count > len(s) {
		return 0, 0
	}
	var value rune
	for i := 0; i < count; i++ {
		c := s[pos+i]
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value += rune(c - '0')
		case c >= 'a' && c <= 'f':
			value += rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			value += rune(c-'A') + 10
		default:
			return 0, 0
		}
	}
	return value, count
}

// jsonNumberParser is a dedicated parser for JSON numbers used by JSONNumber().
type jsonNumberParser struct{}

func (p *jsonNumberParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	if start >= len(ctx.Input) {
		if ctx.IsPartial {
			return NewParseResultRange(NeedMoreInput, start, start)
		}
		return NewParseResult(Fail, start)
	}
	if ctx.Input[start] == '-' || (ctx.Input[start] >= '0' && ctx.Input[start] <= '9') {
		return parseJSONNumber(ctx, start, start)
	}
	return NewParseResult(Fail, start)
}

// BuildPegParser is a helper that creates a parser using a builder function.
func BuildPegParser(fn func(b *Builder) ParserID) *Arena {
	b := NewBuilder()
	root := fn(b)
	b.SetRoot(root)
	return b.Build()
}
