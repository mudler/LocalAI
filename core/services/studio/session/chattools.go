package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// TurnTools are the host-side tools a product adds to a chat for one turn.
type TurnTools struct {
	// Tools run on the host, not in the sandbox. They are added to the six
	// sandbox tools.
	Tools []Tool
	// Note is one line for the model at the start of the turn, for example
	// "unavailable this turn: github (cannot connect)". It is not stored as a
	// user message.
	Note string
	// Prompt is a block added to the system prompt for this turn, for example
	// an index of skills. It goes after the product's SystemPrompt. A changed
	// Prompt restarts the harness from its saved state, as a changed set of
	// tools does; an unchanged one does not. It is cut at 32 KiB on a line
	// boundary. When ChatTools fails, the turn has no Prompt.
	Prompt string
	// RefreshKey is opaque. The sandbox of a chat is refreshed before a turn
	// when the key differs from the key of the turn before and both are not
	// empty: the guest then sees the files that changed in the workspace, and
	// its processes end. Set it to a value that changes only when the files
	// that Prompt names changed. A turn whose hook failed, was stopped, or
	// returned an empty key keeps the previous key. The first turn of a chat
	// that was opened again never refreshes.
	RefreshKey string
	// WorkFolder asks the chat to name its work folder in the prompt
	// (workfiles.Prompt): the chat takes the name of its chat folder for it
	// when it has none yet. The folders are made when something is saved
	// there, so a chat that saves nothing leaves no folder behind.
	WorkFolder bool
}

const (
	// maxNoteLen is how much of a Note the model gets.
	maxNoteLen = 400
	// maxPromptLen is how much of a Prompt the model gets.
	maxPromptLen = 32 << 10
	// noteChatToolsFailed is the note when the product's hook failed.
	noteChatToolsFailed = "connector tools are unavailable this turn."
)

var hostToolNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// sandboxToolNames are the names the six sandbox tools use.
var sandboxToolNames = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range NewTools(nil) {
		m[t.Name] = true
	}
	return m
}()

// hostTools is the set of host-side tools of a chat. The harness is started
// with stable wrappers that look the current closure up by name, so that a
// new closure for an unchanged tool needs no harness restart.
type hostTools struct {
	defs   []Tool          // what the running harness was started with (Run is a wrapper)
	cur    map[string]Tool // the closures of the current turn
	prompt string          // the TurnTools.Prompt that the running harness was started with
	fp     string          // fingerprint of defs and prompt
	key    string          // the last non-empty TurnTools.RefreshKey
}

// capPrompt cuts a prompt block to maxPromptLen bytes on a line boundary. A
// block whose first line is longer than the limit is cut on a rune boundary.
func capPrompt(s string, log *slog.Logger, chat string) string {
	return capPromptAt(s, maxPromptLen, log, chat)
}

// turnPrompt is the prompt block of tt, capped, with the block that names the
// work folder last when tt asks for it. reserve takes a folder name for a
// chat that has none; without it such a chat gets no block (opening a chat
// must not name its folder: only a turn does).
func (c *Chat) turnPrompt(ctx context.Context, tt TurnTools, reserve bool) string {
	if !tt.WorkFolder {
		return capPrompt(tt.Prompt, c.m.log, c.id)
	}
	block := c.workFolderBlock(ctx, reserve)
	if block == "" {
		return capPrompt(tt.Prompt, c.m.log, c.id)
	}
	// The block goes last and is never the part that is cut.
	return strings.TrimSpace(capPromptAt(tt.Prompt, maxPromptLen-len(block)-2, c.m.log, c.id) + "\n\n" + block)
}

// capPromptAt is capPrompt with a limit of n bytes.
func capPromptAt(s string, n int, log *slog.Logger, chat string) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	cut = strings.ToValidUTF8(cut, "")
	log.Warn("session: the turn prompt is over the limit and was cut", "chat", chat, "bytes", len(s), "kept", len(cut))
	return cut
}

// fingerprint hashes the sorted names, descriptions and schemas of the tools
// and the prompt block.
func fingerprint(tools []Tool, prompt string) string {
	sorted := append([]Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	h := sha256.New()
	for _, t := range sorted {
		_, _ = fmt.Fprintf(h, "%d:%s\n%d:%s\n%d:%s\n", len(t.Name), t.Name, len(t.Description), t.Description, len(t.Schema), t.Schema)
	}
	_, _ = fmt.Fprintf(h, "prompt\n%d:%s\n", len(prompt), prompt)
	return hex.EncodeToString(h.Sum(nil))
}

// acceptHostTools drops what the harness could not take: a name that collides
// with a tool of the session's own sandbox (only when the session has one) or
// with another host tool, a name the tool server refuses, a missing Run, a
// schema that is not a JSON object. Without a session sandbox the names are
// free: the product's own bash takes "bash".
func acceptHostTools(tools []Tool, log *slog.Logger, chat string, sandboxed bool) []Tool {
	var out []Tool
	seen := map[string]bool{}
	for _, tool := range tools {
		var schema map[string]any
		switch {
		case sandboxed && sandboxToolNames[tool.Name]:
			log.Warn("session: a host tool has the name of a sandbox tool and was dropped", "chat", chat, "tool", tool.Name)
		case !hostToolNameRE.MatchString(tool.Name) || tool.Run == nil:
			log.Warn("session: a host tool has no usable name or no Run and was dropped", "chat", chat)
		case seen[tool.Name]:
			log.Warn("session: two host tools have one name; the second was dropped", "chat", chat, "tool", tool.Name)
		case json.Unmarshal(tool.Schema, &schema) != nil || schema == nil:
			log.Warn("session: a host tool has a schema that is not an object and was dropped", "chat", chat, "tool", tool.Name)
		default:
			seen[tool.Name] = true
			out = append(out, tool)
		}
	}
	return out
}

// oneLine makes a note a single line of bounded length.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxNoteLen {
		s = strings.ToValidUTF8(s[:maxNoteLen], "")
	}
	return s
}

// callChatTools runs the product's hook. A panic is an error.
func callChatTools(ctx context.Context, f func(context.Context, workspace.Actor) (TurnTools, error), a workspace.Actor, log *slog.Logger) (tt TurnTools, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("session: ChatTools panicked", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			tt, err = TurnTools{}, errors.New("ChatTools panicked")
		}
	}()
	return f(ctx, a)
}

// hostWrappers returns the tools the harness is started with for the current
// host tool set. Each looks the closure of the current turn up by name.
func (c *Chat) hostWrappers() []Tool {
	c.hostMu.Lock()
	defs := append([]Tool(nil), c.host.defs...)
	c.hostMu.Unlock()
	out := make([]Tool, len(defs))
	for i, d := range defs {
		name := d.Name
		d.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			c.hostMu.Lock()
			cur, ok := c.host.cur[name]
			c.hostMu.Unlock()
			if !ok {
				return "", errors.New("not granted")
			}
			slot := &cardSlot{}
			res, err := cur.Run(context.WithValue(ctx, cardSlotKey{}, slot), args)
			if err == nil {
				c.addPendingCard(name, res, slot.get())
			}
			return res, err
		}
		out[i] = d
	}
	return out
}

// applyChatTools runs at the start of a turn. It asks the product for the host
// tools and the prompt block, remembers the closures for this turn, and
// restarts the harness from its saved state when the set of tools (names,
// descriptions, schemas) or the prompt block differs from the one the harness
// runs with. It returns the note for the model, and whether the turn must
// refresh the sandbox: the RefreshKey changed from a non-empty earlier key.
// A failed or stopped hook changes nothing and never asks for a refresh.
//
// The turn is already reserved (the chat is running), so nothing else changes
// the harness. A change that arrives while a turn runs is seen at the next turn.
func (c *Chat) applyChatTools(ctx context.Context) (note string, refresh bool) {
	f := c.m.o.ChatTools
	if f == nil {
		return "", false
	}
	actor, _, _ := c.access()
	tt, err := callChatTools(ctx, f, actor, c.m.log)
	if ctx.Err() != nil {
		// The turn was stopped while the hook ran. Its result is partial: keep
		// the state of the last turn, so that the next turn does not see a change.
		return "", false
	}
	note = oneLine(tt.Note)
	var tools []Tool
	prompt := ""
	if err != nil {
		c.m.log.Warn("session: ChatTools failed; the turn runs with the sandbox tools", "chat", c.id, "error", err)
		note = noteChatToolsFailed
	} else {
		tools = acceptHostTools(tt.Tools, c.m.log, c.id, c.m.o.Sandbox != nil)
		prompt = c.turnPrompt(ctx, tt, true)
	}
	fp := fingerprint(tools, prompt)
	cur := make(map[string]Tool, len(tools))
	defs := make([]Tool, len(tools))
	for i, t := range tools {
		cur[t.Name] = t
		defs[i] = Tool{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	c.hostMu.Lock()
	prev := c.host.fp
	if prev == "" { // no host tool has been set yet
		prev = fingerprint(nil, "")
	}
	changed := fp != prev
	if err == nil && tt.RefreshKey != "" {
		refresh = c.host.key != "" && tt.RefreshKey != c.host.key
		c.host.key = tt.RefreshKey
	}
	c.host.cur = cur
	if changed {
		c.host.defs, c.host.prompt, c.host.fp = defs, prompt, fp
	}
	c.hostMu.Unlock()
	if !changed {
		return note, refresh
	}
	// Run the swap and the fallback to the end even when the turn is stopped:
	// stopping must not leave the chat without a harness.
	ctx = context.WithoutCancel(ctx)
	// A message that arrives during the swap is queued, not injected.
	c.mu.Lock()
	c.swapping = true
	c.mu.Unlock()
	c.injectMu.Lock()
	c.injectMu.Unlock() //nolint:staticcheck // barrier: waits for the injects in flight
	defer func() {
		c.mu.Lock()
		c.swapping = false
		c.mu.Unlock()
	}()
	if err := c.swapHarness(ctx); err != nil {
		c.m.log.Error("session: the harness did not restart with the new host tools", "chat", c.id, "error", err)
		// Go on without them; this ends the chat only if the plain restart fails too.
		c.hostMu.Lock()
		c.host.defs, c.host.cur, c.host.prompt, c.host.fp = nil, nil, "", fingerprint(nil, "")
		c.hostMu.Unlock()
		if err := c.restartHarness(ctx); err != nil {
			return note, false
		}
		return noteChatToolsFailed, false
	}
	return note, refresh
}

// cardSlotKey is the context key of the slot that SetToolCard fills.
type cardSlotKey struct{}

// cardSlot holds the card that one host tool call set.
type cardSlot struct {
	mu    sync.Mutex
	card  *ToolCard
	files []ToolFile
	more  int
}

func (s *cardSlot) set(c ToolCard) { s.mu.Lock(); s.card = &c; s.mu.Unlock() }

func (s *cardSlot) setFiles(f []ToolFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files, s.more = append([]ToolFile(nil), f...), 0
	if len(s.files) > MaxToolFiles {
		s.files, s.more = s.files[:MaxToolFiles], len(s.files)-MaxToolFiles
	}
}

// get returns the card of the call: the one the tool set, with the files on
// it. Files and no card make a FilesCardKind card.
func (s *cardSlot) get() *ToolCard {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.files) == 0 {
		return s.card
	}
	var c ToolCard
	if s.card != nil {
		c = *s.card
	} else {
		var newest int64
		for _, f := range s.files {
			newest = max(newest, f.Rev)
		}
		c = ToolCard{Kind: FilesCardKind, ID: strconv.FormatInt(newest, 10)}
	}
	c.Files, c.FilesMore = s.files, s.more
	return &c
}

// SetToolCard is called by a host tool inside Run. It records the card for the
// call in progress: the chat puts it on the tool_end event and on the tool
// entry of that call. It does nothing outside a host tool's Run, and the card
// is used only when Run returns no error. A card whose Kind is not 1 to 32
// characters of [a-z0-9_], or whose ID is not 1 to 128 printable ASCII
// characters, is dropped and logged. The card is for the product's interface:
// the model never sees it and the tool text does not change.
func SetToolCard(ctx context.Context, c ToolCard) {
	if s, ok := ctx.Value(cardSlotKey{}).(*cardSlot); ok {
		s.set(c)
	}
}

// SetToolFiles is called by a host tool inside Run, like SetToolCard, to put
// the user's files that the call changed on its card. It keeps the card the
// tool sets with SetToolCard (before or after), so a call can say both who
// served it and what it changed. A later call replaces the files; the first
// MaxToolFiles are kept and the rest counted. Files with a bad path or op are
// dropped when the card is checked.
func SetToolFiles(ctx context.Context, files []ToolFile) {
	if s, ok := ctx.Value(cardSlotKey{}).(*cardSlot); ok {
		s.setFiles(files)
	}
}

// maxPendingCards bounds the cards that wait for their tool_end event.
const maxPendingCards = 64

// pendingCard is a successful host call whose tool_end event has not arrived
// yet. card is nil when the call set none (or a bad one): the slot still
// exists, so that the end of such a call cannot take the card of another call.
type pendingCard struct {
	name, output string
	card         *ToolCard
}

// validCard tells whether c is within the documented limits.
func validCard(c ToolCard) bool {
	if n := len(c.Kind); n < 1 || n > 32 {
		return false
	}
	for i := 0; i < len(c.Kind); i++ {
		if b := c.Kind[i]; !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_') {
			return false
		}
	}
	if n := len(c.ID); n < 1 || n > 128 {
		return false
	}
	for i := 0; i < len(c.ID); i++ {
		if b := c.ID[i]; b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}

// Limits of the app of a card.
const (
	maxAppServer = 200
	maxAppURI    = 2048
	maxAppInput  = 16 << 10
	maxAppResult = 256 << 10
)

// cleanCard returns a copy of c that is within the documented limits, and
// whether the card is valid at all. A card with an app whose reference is bad is
// invalid. An input or a result that is too long, or not JSON, is dropped and
// the app is marked cut: the reference is still of use.
func cleanCard(c ToolCard) (ToolCard, bool) {
	if !validCard(c) {
		return ToolCard{}, false
	}
	c.Files, c.FilesMore = cleanFiles(c.Files, c.FilesMore)
	classifyCard(&c)
	if c.App == nil {
		return c, true
	}
	a := *c.App
	if a.Server == "" || len(a.Server) > maxAppServer || len(a.Tool) > maxAppServer ||
		!strings.HasPrefix(a.ResourceURI, "ui://") || len(a.ResourceURI) > maxAppURI {
		return ToolCard{}, false
	}
	for _, r := range a.Server + a.Tool + a.ResourceURI {
		if r < 0x21 && r != ' ' || r == 0x7f {
			return ToolCard{}, false
		}
	}
	a.Visibility = append([]string(nil), a.Visibility...)
	if a.Input = bounded(a.Input, maxAppInput); a.Input == nil && c.App.Input != nil {
		a.Cut = true
	}
	if a.Result = bounded(a.Result, maxAppResult); a.Result == nil && c.App.Result != nil {
		a.Cut = true
	}
	c.App = &a
	return c, true
}

// classifyCard puts the kind of each file on a card: the interface shows a
// working file apart from what the user asked for, and reads the kind from
// the card instead of guessing it from the path.
func classifyCard(c *ToolCard) {
	for i := range c.Files {
		c.Files[i].Kind = workfiles.Kind(c.Files[i].Path)
	}
	c.FileKind = ""
	if c.Kind == ChangeCardKind {
		if _, p, ok := strings.Cut(c.ID, ":"); ok && p != "" {
			c.FileKind = workfiles.Kind(p)
		}
	}
}

// cleanFiles keeps the files of a card that have a usable path and op, at most
// MaxToolFiles. A dropped file is counted in more: the card still says that
// the call changed it.
func cleanFiles(in []ToolFile, more int) ([]ToolFile, int) {
	if len(in) == 0 {
		return nil, max(more, 0)
	}
	out := make([]ToolFile, 0, min(len(in), MaxToolFiles))
	for _, f := range in {
		ok := len(out) < MaxToolFiles && f.Path != "" && f.Path[0] == '/' && len(f.Path) <= maxToolFilePath &&
			utf8.ValidString(f.Path) && f.Rev > 0 &&
			(f.Op == ToolFileCreated || f.Op == ToolFileChanged || f.Op == ToolFileDeleted)
		for _, r := range f.Path {
			if r < 0x20 || r == 0x7f {
				ok = false
				break
			}
		}
		if !ok {
			more++
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		out = nil
	}
	return out, max(more, 0)
}

// bounded returns raw when it is valid JSON of at most max bytes, else nil.
func bounded(raw json.RawMessage, max int) json.RawMessage {
	if len(raw) == 0 || len(raw) > max || !json.Valid(raw) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// addPendingCard records a successful host tool call until the harness reports
// its end, with the card it set (nil when none). It logs a bad card without its
// contents and keeps the call with no card.
func (c *Chat) addPendingCard(name, output string, card *ToolCard) {
	var keep *ToolCard
	if card != nil {
		if cp, ok := cleanCard(*card); ok {
			keep = &cp
		} else {
			c.m.log.Warn("session: a host tool set an invalid card and it was dropped", "chat", c.id, "tool", name)
		}
	}
	c.cardMu.Lock()
	defer c.cardMu.Unlock()
	if len(c.cards) >= maxPendingCards {
		c.cards = append(c.cards[:0], c.cards[1:]...)
	}
	c.cards = append(c.cards, pendingCard{name: name, output: CapText(output, maxOutput), card: keep})
}

// takeCard removes the pending call of a tool_end and returns its card (nil
// when the call set none): the first call whose name and capped output match,
// else the oldest call of that name.
func (c *Chat) takeCard(name, output string) *ToolCard {
	c.cardMu.Lock()
	defer c.cardMu.Unlock()
	pick := -1
	for i, p := range c.cards {
		if p.name != name {
			continue
		}
		if p.output == output {
			pick = i
			break
		}
		if pick < 0 {
			pick = i
		}
	}
	if pick < 0 {
		return nil
	}
	card := c.cards[pick].card
	c.cards = append(c.cards[:pick], c.cards[pick+1:]...)
	return card
}

// clearCards forgets the pending cards, at the start and the end of a turn.
func (c *Chat) clearCards() {
	c.cardMu.Lock()
	c.cards = nil
	c.cardMu.Unlock()
}

// primeChatTools asks the ChatTools hook once before the harness starts, so a
// chat opens with its host tools and its first turn needs no restart. A failed
// hook changes nothing: the turn hook asks again.
func (c *Chat) primeChatTools(ctx context.Context) {
	f := c.m.o.ChatTools
	if f == nil {
		return
	}
	actor, _, _ := c.access()
	tt, err := callChatTools(ctx, f, actor, c.m.log)
	if err != nil || ctx.Err() != nil {
		return
	}
	tools := acceptHostTools(tt.Tools, c.m.log, c.id, c.m.o.Sandbox != nil)
	prompt := c.turnPrompt(ctx, tt, false)
	cur := make(map[string]Tool, len(tools))
	defs := make([]Tool, len(tools))
	for i, t := range tools {
		cur[t.Name] = t
		defs[i] = Tool{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	c.hostMu.Lock()
	c.host.cur, c.host.defs, c.host.prompt, c.host.fp = cur, defs, prompt, fingerprint(tools, prompt)
	if tt.RefreshKey != "" {
		c.host.key = tt.RefreshKey
	}
	c.hostMu.Unlock()
}
