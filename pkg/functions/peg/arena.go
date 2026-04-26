package peg

import "fmt"

// Arena stores parser instances and provides the Parse entry point.
type Arena struct {
	parsers []Parser
	rules   map[string]ParserID
	root    ParserID
}

func NewArena() *Arena {
	return &Arena{
		rules: make(map[string]ParserID),
		root:  InvalidParserID,
	}
}

func (a *Arena) addParser(p Parser) ParserID {
	id := ParserID(len(a.parsers))
	a.parsers = append(a.parsers, p)
	return id
}

func (a *Arena) Get(id ParserID) Parser {
	return a.parsers[id]
}

func (a *Arena) Root() ParserID {
	return a.root
}

func (a *Arena) SetRoot(id ParserID) {
	a.root = id
}

func (a *Arena) GetRule(name string) ParserID {
	id, ok := a.rules[name]
	if !ok {
		panic(fmt.Sprintf("Rule not found: %s", name))
	}
	return id
}

func (a *Arena) HasRule(name string) bool {
	_, ok := a.rules[name]
	return ok
}

// Parse parses from the root parser.
func (a *Arena) Parse(ctx *ParseContext) ParseResult {
	if a.root == InvalidParserID {
		panic("No root parser set")
	}
	return a.ParseAt(a.root, ctx, 0)
}

// ParseFrom parses from the root parser starting at position start.
func (a *Arena) ParseFrom(ctx *ParseContext, start int) ParseResult {
	if a.root == InvalidParserID {
		panic("No root parser set")
	}
	return a.ParseAt(a.root, ctx, start)
}

// ParseAt parses using a specific parser at a given position.
func (a *Arena) ParseAt(id ParserID, ctx *ParseContext, start int) ParseResult {
	parser := a.parsers[id]
	return parser.parse(a, ctx, start)
}

// ParseAnywhere tries parsing from every position in the input until it succeeds.
func (a *Arena) ParseAnywhere(ctx *ParseContext) ParseResult {
	if a.root == InvalidParserID {
		panic("No root parser set")
	}
	if len(ctx.Input) == 0 {
		return a.ParseAt(a.root, ctx, 0)
	}
	for i := range len(ctx.Input) {
		result := a.ParseAt(a.root, ctx, i)
		if result.Type == Success || i == len(ctx.Input)-1 {
			return result
		}
	}
	return NewParseResult(Fail, 0)
}

// resolveRefs walks all parsers and replaces refs with resolved rule IDs.
func (a *Arena) resolveRefs() {
	for i, p := range a.parsers {
		switch pt := p.(type) {
		case *SequenceParser:
			for j, child := range pt.Children {
				pt.Children[j] = a.resolveRef(child)
			}
		case *ChoiceParser:
			for j, child := range pt.Children {
				pt.Children[j] = a.resolveRef(child)
			}
		case *RepetitionParser:
			pt.Child = a.resolveRef(pt.Child)
		case *AndParser:
			pt.Child = a.resolveRef(pt.Child)
		case *NotParser:
			pt.Child = a.resolveRef(pt.Child)
		case *RuleParser:
			pt.Child = a.resolveRef(pt.Child)
		case *TagParser:
			pt.Child = a.resolveRef(pt.Child)
		case *AtomicParser:
			pt.Child = a.resolveRef(pt.Child)
		case *SchemaParser:
			pt.Child = a.resolveRef(pt.Child)
		// Leaf parsers — no children to resolve
		case *EpsilonParser, *StartParser, *EndParser, *LiteralParser,
			*AnyParser, *SpaceParser, *CharsParser, *JSONStringParser,
			*PythonDictStringParser, *UntilParser, *RefParser, *JSONParser,
			*jsonNumberParser:
			// nothing to do
		default:
			_ = i // satisfy compiler
		}
	}

	if a.root != InvalidParserID {
		a.root = a.resolveRef(a.root)
	}
}

func (a *Arena) resolveRef(id ParserID) ParserID {
	if ref, ok := a.parsers[id].(*RefParser); ok {
		return a.GetRule(ref.Name)
	}
	return id
}
