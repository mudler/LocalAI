package peg

// trie is used for multi-delimiter matching in UntilParser.
type trie struct {
	nodes []trieNode
}

type trieNode struct {
	children map[rune]int
	isWord   bool
}

type trieMatch int

const (
	trieNoMatch       trieMatch = 0
	triePartialMatch  trieMatch = 1
	trieCompleteMatch trieMatch = 2
)

func newTrie(words []string) *trie {
	t := &trie{}
	t.createNode() // root
	for _, w := range words {
		t.insert(w)
	}
	return t
}

func (t *trie) createNode() int {
	idx := len(t.nodes)
	t.nodes = append(t.nodes, trieNode{children: make(map[rune]int)})
	return idx
}

func (t *trie) insert(word string) {
	current := 0
	for _, ch := range word {
		if next, ok := t.nodes[current].children[ch]; ok {
			current = next
		} else {
			child := t.createNode()
			t.nodes[current].children[ch] = child
			current = child
		}
	}
	t.nodes[current].isWord = true
}

// checkAt checks if any delimiter starts at position pos in the input.
func (t *trie) checkAt(input string, pos int) trieMatch {
	current := 0
	p := pos

	for p < len(input) {
		r, size, status := parseUTF8Codepoint(input, p)
		if status != utf8Success {
			break
		}

		next, ok := t.nodes[current].children[r]
		if !ok {
			return trieNoMatch
		}

		current = next
		p += size

		if t.nodes[current].isWord {
			return trieCompleteMatch
		}
	}

	// Reached end of input while still in the trie
	if current != 0 {
		return triePartialMatch
	}

	return trieNoMatch
}
