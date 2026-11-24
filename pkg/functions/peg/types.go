package peg

import "unicode/utf8"

// ParserID is a unique identifier for a parser in the arena.
type ParserID int

const InvalidParserID ParserID = -1

// AstID is a unique identifier for an AST node.
type AstID int

const InvalidAstID AstID = -1

// ParseResultType indicates the outcome of a parse attempt.
type ParseResultType int

const (
	Fail          ParseResultType = 0
	Success       ParseResultType = 1
	NeedMoreInput ParseResultType = 2
)

func (t ParseResultType) String() string {
	switch t {
	case Fail:
		return "fail"
	case Success:
		return "success"
	case NeedMoreInput:
		return "need_more_input"
	default:
		return "unknown"
	}
}

// ParseResult holds the result of a parse operation.
type ParseResult struct {
	Type  ParseResultType
	Start int
	End   int
	Nodes []AstID
}

func NewParseResult(typ ParseResultType, start int) ParseResult {
	return ParseResult{Type: typ, Start: start, End: start}
}

func NewParseResultRange(typ ParseResultType, start, end int) ParseResult {
	return ParseResult{Type: typ, Start: start, End: end}
}

func NewParseResultNodes(typ ParseResultType, start, end int, nodes []AstID) ParseResult {
	return ParseResult{Type: typ, Start: start, End: end, Nodes: nodes}
}

// AstNode is a node in the parse AST.
type AstNode struct {
	ID        AstID
	Rule      string
	Tag       string
	Start     int
	End       int
	Text      string
	Children  []AstID
	IsPartial bool
}

// AstArena stores AST nodes.
type AstArena struct {
	nodes []AstNode
}

func (a *AstArena) AddNode(rule, tag string, start, end int, text string, children []AstID, isPartial bool) AstID {
	id := AstID(len(a.nodes))
	a.nodes = append(a.nodes, AstNode{
		ID:        id,
		Rule:      rule,
		Tag:       tag,
		Start:     start,
		End:       end,
		Text:      text,
		Children:  children,
		IsPartial: isPartial,
	})
	return id
}

func (a *AstArena) Get(id AstID) *AstNode {
	return &a.nodes[id]
}

func (a *AstArena) Size() int {
	return len(a.nodes)
}

func (a *AstArena) Clear() {
	a.nodes = a.nodes[:0]
}

// Visit traverses the AST tree rooted at the given node, calling fn for each node.
func (a *AstArena) Visit(id AstID, fn func(*AstNode)) {
	if id == InvalidAstID {
		return
	}
	node := a.Get(id)
	fn(node)
	for _, child := range node.Children {
		a.Visit(child, fn)
	}
}

// VisitResult traverses all top-level nodes in a parse result.
func (a *AstArena) VisitResult(result *ParseResult, fn func(*AstNode)) {
	for _, id := range result.Nodes {
		a.Visit(id, fn)
	}
}

// ParseContext holds the state for a parse operation.
type ParseContext struct {
	Input     string
	IsPartial bool
	Debug     bool
	Ast       AstArena
}

func NewParseContext(input string, isPartial bool) *ParseContext {
	return &ParseContext{
		Input:     input,
		IsPartial: isPartial,
	}
}

// parseUTF8Codepoint parses a single UTF-8 codepoint at position pos.
// Returns the codepoint, bytes consumed, and status.
type utf8Status int

const (
	utf8Success    utf8Status = 0
	utf8Incomplete utf8Status = 1
	utf8Invalid    utf8Status = 2
)

func parseUTF8Codepoint(input string, pos int) (rune, int, utf8Status) {
	if pos >= len(input) {
		return 0, 0, utf8Incomplete
	}
	r, size := utf8.DecodeRuneInString(input[pos:])
	if r == utf8.RuneError {
		if size == 0 {
			return 0, 0, utf8Incomplete
		}
		// Could be incomplete multi-byte sequence
		b := input[pos]
		var expectedLen int
		switch {
		case b&0x80 == 0:
			expectedLen = 1
		case b&0xE0 == 0xC0:
			expectedLen = 2
		case b&0xF0 == 0xE0:
			expectedLen = 3
		case b&0xF8 == 0xF0:
			expectedLen = 4
		default:
			return 0, 0, utf8Invalid
		}
		if pos+expectedLen > len(input) {
			return 0, 0, utf8Incomplete
		}
		return 0, 0, utf8Invalid
	}
	return r, size, utf8Success
}
