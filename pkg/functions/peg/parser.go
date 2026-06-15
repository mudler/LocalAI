package peg

// Parser is the interface all parser types implement.
type Parser interface {
	parse(arena *Arena, ctx *ParseContext, start int) ParseResult
}

// EpsilonParser always succeeds, consumes nothing.
type EpsilonParser struct{}

func (p *EpsilonParser) parse(_ *Arena, _ *ParseContext, start int) ParseResult {
	return NewParseResult(Success, start)
}

// StartParser matches start of input.
type StartParser struct{}

func (p *StartParser) parse(_ *Arena, _ *ParseContext, start int) ParseResult {
	if start == 0 {
		return NewParseResult(Success, start)
	}
	return NewParseResult(Fail, start)
}

// EndParser matches end of input.
type EndParser struct{}

func (p *EndParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	if start >= len(ctx.Input) {
		return NewParseResult(Success, start)
	}
	return NewParseResult(Fail, start)
}

// LiteralParser matches an exact string.
type LiteralParser struct {
	Literal string
}

func (p *LiteralParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start
	for i := range len(p.Literal) {
		if pos >= len(ctx.Input) {
			if !ctx.IsPartial {
				return NewParseResult(Fail, start)
			}
			return NewParseResultRange(NeedMoreInput, start, pos)
		}
		if ctx.Input[pos] != p.Literal[i] {
			return NewParseResult(Fail, start)
		}
		pos++
	}
	return NewParseResultRange(Success, start, pos)
}

// SequenceParser matches children in order.
type SequenceParser struct {
	Children []ParserID
}

func (p *SequenceParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start
	var nodes []AstID

	for _, childID := range p.Children {
		result := arena.ParseAt(childID, ctx, pos)

		if result.Type == Fail {
			if ctx.IsPartial && result.End >= len(ctx.Input) {
				return NewParseResultNodes(NeedMoreInput, start, result.End, nodes)
			}
			return NewParseResultRange(Fail, start, result.End)
		}

		nodes = append(nodes, result.Nodes...)

		if result.Type == NeedMoreInput {
			return NewParseResultNodes(NeedMoreInput, start, result.End, nodes)
		}

		pos = result.End
	}

	return NewParseResultNodes(Success, start, pos, nodes)
}

// ChoiceParser tries each alternative until one succeeds.
type ChoiceParser struct {
	Children []ParserID
}

func (p *ChoiceParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	for _, childID := range p.Children {
		result := arena.ParseAt(childID, ctx, start)
		if result.Type != Fail {
			return result
		}
	}
	return NewParseResult(Fail, start)
}

// RepetitionParser matches min to max repetitions.
type RepetitionParser struct {
	Child    ParserID
	MinCount int
	MaxCount int // -1 for unbounded
}

func (p *RepetitionParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start
	matchCount := 0
	var nodes []AstID

	for p.MaxCount == -1 || matchCount < p.MaxCount {
		if pos >= len(ctx.Input) {
			break
		}

		result := arena.ParseAt(p.Child, ctx, pos)

		if result.Type == Success {
			// Prevent infinite loop on empty matches
			if result.End == pos {
				break
			}
			nodes = append(nodes, result.Nodes...)
			pos = result.End
			matchCount++
			continue
		}

		if result.Type == NeedMoreInput {
			nodes = append(nodes, result.Nodes...)
			return NewParseResultNodes(NeedMoreInput, start, result.End, nodes)
		}

		// Child failed
		break
	}

	if p.MinCount > 0 && matchCount < p.MinCount {
		if pos >= len(ctx.Input) && ctx.IsPartial {
			return NewParseResultNodes(NeedMoreInput, start, pos, nodes)
		}
		return NewParseResultRange(Fail, start, pos)
	}

	return NewParseResultNodes(Success, start, pos, nodes)
}

// AndParser is a positive lookahead — succeeds if child succeeds, consumes nothing.
type AndParser struct {
	Child ParserID
}

func (p *AndParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	result := arena.ParseAt(p.Child, ctx, start)
	return NewParseResult(result.Type, start)
}

// NotParser is a negative lookahead — succeeds if child fails, consumes nothing.
type NotParser struct {
	Child ParserID
}

func (p *NotParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	result := arena.ParseAt(p.Child, ctx, start)
	if result.Type == Success {
		return NewParseResult(Fail, start)
	}
	if result.Type == NeedMoreInput {
		return NewParseResult(NeedMoreInput, start)
	}
	return NewParseResult(Success, start)
}

// AnyParser matches any single UTF-8 codepoint.
type AnyParser struct{}

func (p *AnyParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	_, size, status := parseUTF8Codepoint(ctx.Input, start)
	if status == utf8Incomplete {
		if !ctx.IsPartial {
			return NewParseResult(Fail, start)
		}
		return NewParseResult(NeedMoreInput, start)
	}
	if status == utf8Invalid {
		return NewParseResult(Fail, start)
	}
	return NewParseResultRange(Success, start, start+size)
}

// SpaceParser matches zero or more whitespace characters.
type SpaceParser struct{}

func (p *SpaceParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start
	for pos < len(ctx.Input) {
		c := ctx.Input[pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			pos++
		} else {
			break
		}
	}
	return NewParseResultRange(Success, start, pos)
}

// CharRange represents a range of Unicode codepoints.
type CharRange struct {
	Start rune
	End   rune
}

func (r CharRange) Contains(cp rune) bool {
	return cp >= r.Start && cp <= r.End
}

// CharsParser matches characters from a character class.
type CharsParser struct {
	Pattern  string
	Ranges   []CharRange
	Negated  bool
	MinCount int
	MaxCount int // -1 for unbounded
}

func (p *CharsParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start
	matchCount := 0

	for p.MaxCount == -1 || matchCount < p.MaxCount {
		r, size, status := parseUTF8Codepoint(ctx.Input, pos)

		if status == utf8Incomplete {
			if matchCount >= p.MinCount {
				return NewParseResultRange(Success, start, pos)
			}
			if !ctx.IsPartial {
				return NewParseResult(Fail, start)
			}
			return NewParseResultRange(NeedMoreInput, start, pos)
		}

		if status == utf8Invalid {
			if matchCount >= p.MinCount {
				return NewParseResultRange(Success, start, pos)
			}
			return NewParseResult(Fail, start)
		}

		matches := false
		for _, cr := range p.Ranges {
			if cr.Contains(r) {
				matches = true
				break
			}
		}

		if p.Negated {
			matches = !matches
		}

		if matches {
			pos += size
			matchCount++
		} else {
			break
		}
	}

	if matchCount < p.MinCount {
		if pos >= len(ctx.Input) && ctx.IsPartial {
			return NewParseResultRange(NeedMoreInput, start, pos)
		}
		return NewParseResultRange(Fail, start, pos)
	}

	return NewParseResultRange(Success, start, pos)
}

// JSONStringParser matches JSON string content (without quotes).
type JSONStringParser struct{}

func (p *JSONStringParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start

	for pos < len(ctx.Input) {
		c := ctx.Input[pos]

		if c == '"' {
			return NewParseResultRange(Success, start, pos)
		}

		if c == '\\' {
			result := handleEscapeSequence(ctx, start, pos)
			if result.Type != Success {
				return result
			}
			pos = result.End
		} else {
			_, size, status := parseUTF8Codepoint(ctx.Input, pos)
			if status == utf8Incomplete {
				if !ctx.IsPartial {
					return NewParseResult(Fail, start)
				}
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			if status == utf8Invalid {
				return NewParseResult(Fail, start)
			}
			pos += size
		}
	}

	if !ctx.IsPartial {
		return NewParseResultRange(Fail, start, pos)
	}
	return NewParseResultRange(NeedMoreInput, start, pos)
}

// PythonDictStringParser matches single-quoted string content (without quotes).
// Like JSONStringParser but terminates on single quote instead of double quote.
type PythonDictStringParser struct{}

func (p *PythonDictStringParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	pos := start

	for pos < len(ctx.Input) {
		c := ctx.Input[pos]

		if c == '\'' {
			return NewParseResultRange(Success, start, pos)
		}

		if c == '\\' {
			result := handleEscapeSequence(ctx, start, pos)
			if result.Type != Success {
				return result
			}
			pos = result.End
		} else {
			_, size, status := parseUTF8Codepoint(ctx.Input, pos)
			if status == utf8Incomplete {
				if !ctx.IsPartial {
					return NewParseResult(Fail, start)
				}
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			if status == utf8Invalid {
				return NewParseResult(Fail, start)
			}
			pos += size
		}
	}

	if !ctx.IsPartial {
		return NewParseResultRange(Fail, start, pos)
	}
	return NewParseResultRange(NeedMoreInput, start, pos)
}

func handleEscapeSequence(ctx *ParseContext, start int, pos int) ParseResult {
	pos++ // consume '\'
	if pos >= len(ctx.Input) {
		if !ctx.IsPartial {
			return NewParseResult(Fail, start)
		}
		return NewParseResultRange(NeedMoreInput, start, pos)
	}

	switch ctx.Input[pos] {
	case '"', '\'', '\\', '/', 'b', 'f', 'n', 'r', 't':
		pos++
		return NewParseResultRange(Success, start, pos)
	case 'u':
		return handleUnicodeEscape(ctx, start, pos)
	default:
		return NewParseResult(Fail, start)
	}
}

func handleUnicodeEscape(ctx *ParseContext, start int, pos int) ParseResult {
	pos++ // consume 'u'
	for range 4 {
		if pos >= len(ctx.Input) {
			if !ctx.IsPartial {
				return NewParseResult(Fail, start)
			}
			return NewParseResultRange(NeedMoreInput, start, pos)
		}
		if !isHexDigit(ctx.Input[pos]) {
			return NewParseResult(Fail, start)
		}
		pos++
	}
	return NewParseResultRange(Success, start, pos)
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// UntilParser matches everything until one of the delimiters is found.
type UntilParser struct {
	Delimiters []string
}

func (p *UntilParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	matcher := newTrie(p.Delimiters)

	pos := start
	lastValidPos := start

	for pos < len(ctx.Input) {
		_, size, status := parseUTF8Codepoint(ctx.Input, pos)

		if status == utf8Incomplete {
			if !ctx.IsPartial {
				return NewParseResult(Fail, start)
			}
			return NewParseResultRange(NeedMoreInput, start, lastValidPos)
		}

		if status == utf8Invalid {
			return NewParseResult(Fail, start)
		}

		match := matcher.checkAt(ctx.Input, pos)

		if match == trieCompleteMatch {
			return NewParseResultRange(Success, start, pos)
		}

		if match == triePartialMatch {
			return NewParseResultRange(Success, start, pos)
		}

		pos += size
		lastValidPos = pos
	}

	if lastValidPos == len(ctx.Input) && ctx.IsPartial {
		return NewParseResultRange(NeedMoreInput, start, lastValidPos)
	}
	return NewParseResultRange(Success, start, lastValidPos)
}

// RuleParser creates an AST node with a rule name.
type RuleParser struct {
	Name    string
	Child   ParserID
	Trigger bool
}

func (p *RuleParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	result := arena.ParseAt(p.Child, ctx, start)

	if result.Type != Fail {
		text := ""
		if result.Start < len(ctx.Input) {
			end := result.End
			if end > len(ctx.Input) {
				end = len(ctx.Input)
			}
			text = ctx.Input[result.Start:end]
		}

		nodeID := ctx.Ast.AddNode(
			p.Name, "", result.Start, result.End, text,
			result.Nodes, result.Type == NeedMoreInput,
		)

		return NewParseResultNodes(result.Type, result.Start, result.End, []AstID{nodeID})
	}

	return result
}

// RefParser references a named rule (resolved during Build).
type RefParser struct {
	Name string
}

func (p *RefParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	ruleID := arena.GetRule(p.Name)
	return arena.ParseAt(ruleID, ctx, start)
}

// AtomicParser suppresses partial AST nodes.
type AtomicParser struct {
	Child ParserID
}

func (p *AtomicParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	result := arena.ParseAt(p.Child, ctx, start)
	if result.Type == NeedMoreInput {
		result.Nodes = nil
	}
	return result
}

// TagParser creates an AST node with a semantic tag.
type TagParser struct {
	Child ParserID
	Tag   string
}

func (p *TagParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	result := arena.ParseAt(p.Child, ctx, start)

	if result.Type != Fail {
		text := ""
		if result.Start < len(ctx.Input) {
			end := result.End
			if end > len(ctx.Input) {
				end = len(ctx.Input)
			}
			text = ctx.Input[result.Start:end]
		}

		nodeID := ctx.Ast.AddNode(
			"", p.Tag, result.Start, result.End, text,
			result.Nodes, result.Type == NeedMoreInput,
		)

		return NewParseResultNodes(result.Type, result.Start, result.End, []AstID{nodeID})
	}

	return result
}

// SchemaParser wraps a parser with schema metadata (pass-through at parse time).
type SchemaParser struct {
	Child ParserID
	Name  string
}

func (p *SchemaParser) parse(arena *Arena, ctx *ParseContext, start int) ParseResult {
	return arena.ParseAt(p.Child, ctx, start)
}

// JSONParser matches a complete JSON value (object, array, string, number, bool, null).
type JSONParser struct {
	arena *Arena
}

func (p *JSONParser) parse(_ *Arena, ctx *ParseContext, start int) ParseResult {
	return parseJSONValue(ctx, start, start)
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func parseLiteralAt(ctx *ParseContext, start, pos int, lit string) ParseResult {
	for i := range len(lit) {
		if pos+i >= len(ctx.Input) {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, pos+i)
			}
			return NewParseResult(Fail, start)
		}
		if ctx.Input[pos+i] != lit[i] {
			return NewParseResult(Fail, start)
		}
	}
	return NewParseResultRange(Success, start, pos+len(lit))
}

func parseJSONString(ctx *ParseContext, start, pos int) ParseResult {
	pos++ // skip opening "
	for pos < len(ctx.Input) {
		c := ctx.Input[pos]
		if c == '"' {
			return NewParseResultRange(Success, start, pos+1)
		}
		if c == '\\' {
			pos++
			if pos >= len(ctx.Input) {
				if ctx.IsPartial {
					return NewParseResultRange(NeedMoreInput, start, pos)
				}
				return NewParseResult(Fail, start)
			}
			switch ctx.Input[pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				pos++
			case 'u':
				pos++
				for range 4 {
					if pos >= len(ctx.Input) {
						if ctx.IsPartial {
							return NewParseResultRange(NeedMoreInput, start, pos)
						}
						return NewParseResult(Fail, start)
					}
					if !isHexDigit(ctx.Input[pos]) {
						return NewParseResult(Fail, start)
					}
					pos++
				}
			default:
				return NewParseResult(Fail, start)
			}
		} else {
			_, size, status := parseUTF8Codepoint(ctx.Input, pos)
			if status == utf8Incomplete {
				if !ctx.IsPartial {
					return NewParseResult(Fail, start)
				}
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			if status == utf8Invalid {
				return NewParseResult(Fail, start)
			}
			pos += size
		}
	}
	if ctx.IsPartial {
		return NewParseResultRange(NeedMoreInput, start, pos)
	}
	return NewParseResult(Fail, start)
}

func parseJSONNumber(ctx *ParseContext, start, pos int) ParseResult {
	p := pos
	if p < len(ctx.Input) && ctx.Input[p] == '-' {
		p++
	}
	if p >= len(ctx.Input) {
		if ctx.IsPartial {
			return NewParseResultRange(NeedMoreInput, start, p)
		}
		return NewParseResult(Fail, start)
	}
	if ctx.Input[p] == '0' {
		p++
	} else if ctx.Input[p] >= '1' && ctx.Input[p] <= '9' {
		p++
		for p < len(ctx.Input) && ctx.Input[p] >= '0' && ctx.Input[p] <= '9' {
			p++
		}
	} else {
		return NewParseResult(Fail, start)
	}
	if p < len(ctx.Input) && ctx.Input[p] == '.' {
		p++
		digitStart := p
		for p < len(ctx.Input) && ctx.Input[p] >= '0' && ctx.Input[p] <= '9' {
			p++
		}
		if p == digitStart {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, p)
			}
			return NewParseResult(Fail, start)
		}
	}
	if p < len(ctx.Input) && (ctx.Input[p] == 'e' || ctx.Input[p] == 'E') {
		p++
		if p < len(ctx.Input) && (ctx.Input[p] == '+' || ctx.Input[p] == '-') {
			p++
		}
		digitStart := p
		for p < len(ctx.Input) && ctx.Input[p] >= '0' && ctx.Input[p] <= '9' {
			p++
		}
		if p == digitStart {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, p)
			}
			return NewParseResult(Fail, start)
		}
	}

	// In partial mode, check if the next character could continue the number.
	// This prevents premature commits (e.g. returning "3" when "3.14" is incoming).
	if ctx.IsPartial && p >= len(ctx.Input) {
		return NewParseResultRange(NeedMoreInput, start, p)
	}
	if ctx.IsPartial && p < len(ctx.Input) && isNumberContinuation(ctx.Input[p]) {
		return NewParseResultRange(NeedMoreInput, start, p)
	}

	return NewParseResultRange(Success, start, p)
}

func isNumberContinuation(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

func parseJSONObject(ctx *ParseContext, start, pos int) ParseResult {
	pos++ // skip {
	pos = skipWS(ctx.Input, pos)
	if pos >= len(ctx.Input) {
		if ctx.IsPartial {
			return NewParseResultRange(NeedMoreInput, start, pos)
		}
		return NewParseResult(Fail, start)
	}
	if ctx.Input[pos] == '}' {
		return NewParseResultRange(Success, start, pos+1)
	}
	for {
		pos = skipWS(ctx.Input, pos)
		// key
		if pos >= len(ctx.Input) {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			return NewParseResult(Fail, start)
		}
		if ctx.Input[pos] != '"' {
			return NewParseResult(Fail, start)
		}
		r := parseJSONString(ctx, start, pos)
		if r.Type != Success {
			return r
		}
		pos = r.End
		pos = skipWS(ctx.Input, pos)
		// colon
		if pos >= len(ctx.Input) {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			return NewParseResult(Fail, start)
		}
		if ctx.Input[pos] != ':' {
			return NewParseResult(Fail, start)
		}
		pos++
		pos = skipWS(ctx.Input, pos)
		// value
		vr := parseJSONValue(ctx, start, pos)
		if vr.Type != Success {
			return vr
		}
		pos = vr.End
		pos = skipWS(ctx.Input, pos)
		if pos >= len(ctx.Input) {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			return NewParseResult(Fail, start)
		}
		if ctx.Input[pos] == '}' {
			return NewParseResultRange(Success, start, pos+1)
		}
		if ctx.Input[pos] != ',' {
			return NewParseResult(Fail, start)
		}
		pos++
	}
}

func parseJSONArray(ctx *ParseContext, start, pos int) ParseResult {
	pos++ // skip [
	pos = skipWS(ctx.Input, pos)
	if pos >= len(ctx.Input) {
		if ctx.IsPartial {
			return NewParseResultRange(NeedMoreInput, start, pos)
		}
		return NewParseResult(Fail, start)
	}
	if ctx.Input[pos] == ']' {
		return NewParseResultRange(Success, start, pos+1)
	}
	for {
		pos = skipWS(ctx.Input, pos)
		vr := parseJSONValue(ctx, start, pos)
		if vr.Type != Success {
			return vr
		}
		pos = vr.End
		pos = skipWS(ctx.Input, pos)
		if pos >= len(ctx.Input) {
			if ctx.IsPartial {
				return NewParseResultRange(NeedMoreInput, start, pos)
			}
			return NewParseResult(Fail, start)
		}
		if ctx.Input[pos] == ']' {
			return NewParseResultRange(Success, start, pos+1)
		}
		if ctx.Input[pos] != ',' {
			return NewParseResult(Fail, start)
		}
		pos++
	}
}

func parseJSONValue(ctx *ParseContext, start, pos int) ParseResult {
	pos = skipWS(ctx.Input, pos)
	if pos >= len(ctx.Input) {
		if ctx.IsPartial {
			return NewParseResultRange(NeedMoreInput, start, pos)
		}
		return NewParseResult(Fail, start)
	}
	switch ctx.Input[pos] {
	case '{':
		return parseJSONObject(ctx, start, pos)
	case '[':
		return parseJSONArray(ctx, start, pos)
	case '"':
		return parseJSONString(ctx, start, pos)
	case 't':
		return parseLiteralAt(ctx, start, pos, "true")
	case 'f':
		return parseLiteralAt(ctx, start, pos, "false")
	case 'n':
		return parseLiteralAt(ctx, start, pos, "null")
	default:
		if ctx.Input[pos] == '-' || (ctx.Input[pos] >= '0' && ctx.Input[pos] <= '9') {
			return parseJSONNumber(ctx, start, pos)
		}
		return NewParseResult(Fail, start)
	}
}

func skipWS(input string, pos int) int {
	for pos < len(input) && isWhitespace(input[pos]) {
		pos++
	}
	return pos
}
