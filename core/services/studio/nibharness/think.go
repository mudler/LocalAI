package nibharness

import "strings"

// thinkSplitter separates inline reasoning from an answer. A model served
// without a reasoning parser writes its thoughts in the answer between <think>
// and </think>; /v1 splits them out and the chats must too. The text arrives in
// chunks, so a tag can be cut in two: the splitter holds back a tail that may
// be the start of a tag until the next chunk or the end of the turn.
type thinkSplitter struct {
	inside bool
	held   string
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// piece is a run of text and whether it is reasoning.
type piece struct {
	text      string
	reasoning bool
}

// feed takes the next chunk and returns what can be told apart so far.
func (s *thinkSplitter) feed(chunk string) []piece {
	buf := s.held + chunk
	s.held = ""
	var out []piece
	emit := func(t string) {
		if t != "" {
			out = append(out, piece{text: t, reasoning: s.inside})
		}
	}
	for {
		tag := thinkOpen
		if s.inside {
			tag = thinkClose
		}
		if i := strings.Index(buf, tag); i >= 0 {
			emit(buf[:i])
			buf = buf[i+len(tag):]
			s.inside = !s.inside
			continue
		}
		keep := partialTagTail(buf, tag)
		emit(buf[:len(buf)-keep])
		s.held = buf[len(buf)-keep:]
		return out
	}
}

// flush returns the held tail at the end of a turn: it was not a tag after all.
func (s *thinkSplitter) flush() []piece {
	held := s.held
	s.held = ""
	if held == "" {
		return nil
	}
	return []piece{{text: held, reasoning: s.inside}}
}

// partialTagTail is the length of the longest tail of buf that is a proper
// prefix of tag.
func partialTagTail(buf, tag string) int {
	for k := min(len(tag)-1, len(buf)); k > 0; k-- {
		if strings.HasSuffix(buf, tag[:k]) {
			return k
		}
	}
	return 0
}
