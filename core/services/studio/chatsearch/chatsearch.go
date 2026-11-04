// Package chatsearch finds text in chat transcripts. A store keeps an index
// (or scans) and returns candidate messages; Build then checks them, ranks
// them and cuts the snippets, so every store gives the same answers.
//
// Matching is substring matching on text folded to lower case rune by rune
// (Fold), so the index, the query and the snippet agree on every script. A
// query is split in words and a message matches when it holds every word; a
// phrase in double quotes is one word.
package chatsearch

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Limits of a search.
const (
	// MinTermRunes is the length under which the trigram index finds nothing;
	// a shorter word is checked on the rows the longer words select.
	MinTermRunes = 3
	// MaxTerms is the most words a query may have; the rest is ignored.
	MaxTerms = 6
	// MaxTermRunes is the longest word; a longer one is cut.
	MaxTermRunes = 64
	// PoolSize is the most candidate messages a store returns, newest first.
	// Ranking works on this pool.
	PoolSize = 600
	// DefaultSnippetRunes is the width of a snippet unless the query says.
	DefaultSnippetRunes = 160
	// ScanBudget is the time a store without an index spends on one search.
	ScanBudget = 2 * time.Second
)

// Origin filters for Query.Origin besides a kind of origin (trigger,
// connector, job, page).
const (
	OriginPerson    = "person"    // chats a person started
	OriginAutomated = "automated" // every other chat
)

// Query is a search.
type Query struct {
	// Principal owns the chats. A store applies it in the query itself.
	Principal string
	// Text is what to find.
	Text string

	// ChatID limits the search to one chat.
	ChatID string
	// AgentID limits it to the chats of one saved agent.
	AgentID string
	// Origin limits it to chats of one origin: OriginPerson, OriginAutomated
	// or a kind of origin.
	Origin string
	// Role limits it to messages of one role (user or assistant).
	Role string
	// Since and Until bound the time of the message; the zero time is no bound.
	Since, Until time.Time
	// Exclude drops a chat after it was loaded (a private chat, a chat the
	// caller may not read). It may be nil.
	Exclude func(session.ChatRecord) bool

	// Limit is the most hits to return; 0 is 50.
	Limit int
	// PerChat keeps the best message of each chat.
	PerChat bool
	// Titles also matches the title of the chats.
	Titles bool
	// SnippetRunes is the width of a snippet; 0 is DefaultSnippetRunes.
	SnippetRunes int
	// Now is the time recency is measured from; zero is the clock.
	Now time.Time
}

// Terms returns the words of the query, folded.
func (q Query) Terms() []string { return Terms(q.Text) }

// Range marks a part of a snippet. Start and End are offsets in UTF-16 code
// units, the way a JavaScript string counts, End exclusive.
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Hit is one matching message, or a chat whose title matches (Seq 0).
type Hit struct {
	Chat    session.ChatRecord
	InTitle bool
	// Seq, Role and At say which message matched; Seq is 0 for a title hit.
	Seq  int64
	Role string
	At   time.Time
	// Snippet is the text around the match, with the words marked by
	// Highlights.
	Snippet    string
	Highlights []Range
	// Matches is how many messages of the chat matched, among the candidates.
	Matches int
	Score   float64
}

// Progress says how complete the index is.
type Progress struct {
	// Indexing is true while the messages written before the index existed
	// are still being added.
	Indexing bool
	// Done and Total count chats; Total is 0 when unknown.
	Done, Total int
}

// Result is the answer of a search.
type Result struct {
	Hits []Hit
	// Engine is how the store searched: fts5, pg_trgm or scan.
	Engine string
	// Partial is true when a time budget ended the search before every chat
	// was read (a store without an index).
	Partial bool
	// Searched and Total count the chats a store without an index read, and
	// the chats it could have read; both 0 for an indexed store.
	Searched, Total int
	Progress        Progress
}

// Index is what a store implements to be searched.
type Index interface {
	// SearchText finds the messages of q.Principal's chats that match q. A
	// chat of another principal, a sub-agent chat and a deleted chat never
	// appear.
	SearchText(ctx context.Context, q Query) (Result, error)
}

// Candidate is a message a store found: it may match.
type Candidate struct {
	Chat session.ChatRecord
	Seq  int64
	Role string
	// Text is what the person reads (see IndexText).
	Text string
	At   time.Time
}

// Fold lowers s rune by rune, so a folded string has as many runes as s.
func Fold(s string) string { return strings.Map(unicode.ToLower, s) }

// Terms splits text into folded words. A phrase in double quotes is one word.
func Terms(text string) []string {
	var out []string
	add := func(w string) {
		w = strings.TrimSpace(Fold(w))
		if r := []rune(w); len(r) > MaxTermRunes {
			w = string(r[:MaxTermRunes])
		}
		if w != "" && !slices.Contains(out, w) && len(out) < MaxTerms {
			out = append(out, w)
		}
	}
	for len(text) > 0 {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		if strings.HasPrefix(text, `"`) {
			if end := strings.Index(text[1:], `"`); end >= 0 {
				add(text[1 : 1+end])
				text = text[end+2:]
				continue
			}
			text = text[1:]
			continue
		}
		end := strings.IndexFunc(text, unicode.IsSpace)
		if end < 0 {
			end = len(text)
		}
		add(text[:end])
		text = text[end:]
	}
	return out
}

// IndexText is the text of a message that search covers, folded for the
// index: the words the person reads, not the list of files the model gets. It
// returns false for a line that is not a user or assistant message or has no
// text. hasAttachments says the entry keeps its files apart already, so its
// text has no list to remove.
func IndexText(role, text string, hasAttachments bool) (display string, ok bool) {
	if role != session.RoleUser && role != session.RoleAssistant {
		return "", false
	}
	e := session.Entry{Role: role, Text: text}
	if hasAttachments {
		e.Attachments = []session.Attachment{{}}
	}
	display = session.DisplayEntry(e).Text
	return display, strings.TrimSpace(display) != ""
}

// Matches reports whether folded holds every term.
func Matches(folded string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, t := range terms {
		if !strings.Contains(folded, t) {
			return false
		}
	}
	return true
}

// ChatMatches reports whether the chat passes the filters of q that do not
// depend on the message: the kind, the agent, the origin and the chat.
func (q Query) ChatMatches(rec session.ChatRecord) bool {
	if rec.Principal != q.Principal || rec.Kind == session.KindSubAgent {
		return false
	}
	if q.ChatID != "" && rec.ID != q.ChatID {
		return false
	}
	if q.AgentID != "" && rec.AgentID != q.AgentID {
		return false
	}
	switch q.Origin {
	case "":
	case OriginPerson:
		if rec.Origin != "" {
			return false
		}
	case OriginAutomated:
		if rec.Origin == "" {
			return false
		}
	default:
		if rec.Origin != q.Origin {
			return false
		}
	}
	return q.Exclude == nil || !q.Exclude(rec)
}

// TimeMatches reports whether t is inside Since and Until.
func (q Query) TimeMatches(t time.Time) bool {
	if !q.Since.IsZero() && t.Before(q.Since) {
		return false
	}
	return q.Until.IsZero() || t.Before(q.Until)
}

// Build checks the candidates against q, ranks them and returns up to
// q.Limit hits. titles are chats whose title may match; a chat whose title
// matches but that has no matching message comes back as a title hit.
func Build(q Query, cands []Candidate, titles []session.ChatRecord) []Hit {
	terms := q.Terms()
	if len(terms) == 0 {
		return nil
	}
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	width := q.SnippetRunes
	if width <= 0 {
		width = DefaultSnippetRunes
	}
	phrase := Fold(strings.Join(strings.Fields(q.Text), " "))
	titleHit := map[string]bool{}
	if q.Titles {
		for _, rec := range titles {
			if q.ChatMatches(rec) && Matches(Fold(rec.Title), terms) {
				titleHit[rec.ID] = true
			}
		}
	}

	byChat := map[string][]Hit{}
	var order []string
	for _, c := range cands {
		if !q.ChatMatches(c.Chat) || !q.TimeMatches(c.At) || (q.Role != "" && c.Role != q.Role) {
			continue
		}
		folded := Fold(c.Text)
		if !Matches(folded, terms) {
			continue
		}
		snip, marks := cut(c.Text, folded, terms, width)
		h := Hit{Chat: c.Chat, Seq: c.Seq, Role: c.Role, At: c.At, Snippet: snip, Highlights: marks,
			InTitle: titleHit[c.Chat.ID]}
		h.Score = quality(folded, terms, phrase) + recency(now, c.At)
		if h.InTitle {
			h.Score += 1.5
		}
		if _, seen := byChat[c.Chat.ID]; !seen {
			order = append(order, c.Chat.ID)
		}
		byChat[c.Chat.ID] = append(byChat[c.Chat.ID], h)
	}

	var hits []Hit
	for _, id := range order {
		group := byChat[id]
		sort.SliceStable(group, func(i, j int) bool { return better(group[i], group[j]) })
		for i := range group {
			group[i].Matches = len(group)
		}
		if q.PerChat {
			group = group[:1]
		}
		hits = append(hits, group...)
	}
	// A chat whose title matches and that has no matching message.
	if q.Titles {
		for _, rec := range titles {
			if _, has := byChat[rec.ID]; has || !titleHit[rec.ID] || !q.TimeMatches(rec.Updated) {
				continue
			}
			hits = append(hits, Hit{Chat: rec, InTitle: true, Score: 1.5 + 2 + recency(now, rec.Updated)})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return better(hits[i], hits[j]) })
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// better orders hits: score, then the newest, then the chat and line.
func better(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	ta, tb := a.At, b.At
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	if a.Chat.ID != b.Chat.ID {
		return a.Chat.ID > b.Chat.ID
	}
	return a.Seq < b.Seq
}

// quality scores how well folded matches: the whole query as one phrase, each
// word as a whole word, and repeats.
func quality(folded string, terms []string, phrase string) float64 {
	var s float64
	if len(terms) > 1 && strings.Contains(folded, phrase) {
		s += 2
	}
	repeats := 0
	for _, t := range terms {
		if wholeWord(folded, t) {
			s += 1
		}
		repeats += min(strings.Count(folded, t), 4) - 1
	}
	return s + 0.1*float64(repeats)
}

// wholeWord reports whether t occurs in s between non-letters.
func wholeWord(s, t string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], t)
		if i < 0 {
			return false
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(s[:i])
		after, _ := utf8.DecodeRuneInString(s[i+len(t):])
		if (i == 0 || !isWord(before)) && (i+len(t) == len(s) || !isWord(after)) {
			return true
		}
		from = i + len(t)
	}
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// recency is 1 for now and falls to 0.5 after a month, 0.25 after two.
func recency(now, at time.Time) float64 {
	if at.IsZero() {
		return 0
	}
	days := now.Sub(at).Hours() / 24
	if days < 0 {
		days = 0
	}
	return math.Exp2(-days / 30)
}

// cut returns the text around the first match, about width runes, with white
// space folded to single spaces, and the ranges of the words in it.
func cut(text, folded string, terms []string, width int) (string, []Range) {
	tr, fr := []rune(text), []rune(folded)
	if len(tr) != len(fr) { // Fold keeps the rune count; stay safe anyway
		tr = fr
	}
	first := -1
	for _, t := range terms {
		if i := indexRunes(fr, []rune(t), 0); i >= 0 && (first < 0 || i < first) {
			first = i
		}
	}
	if first < 0 {
		first = 0
	}
	from := max(0, first-width/3)
	to := min(len(tr), from+width)
	from = max(0, min(from, to-width))
	window, wf := tr[from:to], fr[from:to]

	// Collapse white space, keeping the folded copy in step.
	var out, outF []rune
	space := true
	for i, r := range window {
		if unicode.IsSpace(r) {
			if !space {
				out, outF = append(out, ' '), append(outF, ' ')
			}
			space = true
			continue
		}
		space = false
		out, outF = append(out, r), append(outF, wf[i])
	}
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out, outF = out[:n-1], outF[:n-1]
	}
	pre := 0
	if from > 0 {
		out, outF = append([]rune("…"), out...), append([]rune("…"), outF...)
		pre = 1
	}
	if to < len(tr) {
		out, outF = append(out, '…'), append(outF, '…')
	}

	var marks []Range
	for _, t := range terms {
		tt := []rune(t)
		for at := indexRunes(outF, tt, pre); at >= 0; at = indexRunes(outF, tt, at+len(tt)) {
			marks = append(marks, Range{Start: at, End: at + len(tt)})
		}
	}
	marks = mergeRanges(marks)
	for i, m := range marks {
		marks[i] = Range{Start: utf16Len(out[:m.Start]), End: utf16Len(out[:m.End])}
	}
	return string(out), marks
}

func utf16Len(r []rune) int { return len(utf16.Encode(r)) }

// indexRunes finds sub in s at or after from, or -1.
func indexRunes(s, sub []rune, from int) int {
	if len(sub) == 0 {
		return -1
	}
	for i := max(from, 0); i+len(sub) <= len(s); i++ {
		if s[i] == sub[0] && slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// mergeRanges sorts the ranges and joins the ones that touch or overlap.
func mergeRanges(r []Range) []Range {
	if len(r) == 0 {
		return nil
	}
	sort.Slice(r, func(i, j int) bool { return r[i].Start < r[j].Start })
	out := []Range{r[0]}
	for _, x := range r[1:] {
		last := &out[len(out)-1]
		if x.Start <= last.End {
			last.End = max(last.End, x.End)
			continue
		}
		out = append(out, x)
	}
	return out
}
