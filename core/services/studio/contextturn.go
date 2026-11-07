package studio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// homeReader reads the files of a user's home as the user, outside the fence
// of any chat. The loader names the path, never the model, so a scoped chat
// reads its own notes without gaining any access to the files. A file that
// another person may write (files.Service.OthersWrite) does not exist for it.
type homeReader struct{ f *files.Service }

func (h homeReader) ReadFile(ctx context.Context, principal, rel string, limit int64) ([]byte, error) {
	if !h.f.Available() {
		return nil, contextfiles.ErrNotExist
	}
	abs, err := h.f.Abs(principal, rel)
	if err != nil {
		return nil, err
	}
	// A note in a folder that another person may write is theirs, not the
	// user's: it is not read into the user's prompt.
	others, err := h.f.OthersWrite(ctx, principal)
	if err != nil {
		return nil, err
	}
	if others.Covers(abs) {
		return nil, contextfiles.ErrNotExist
	}
	r, node, err := h.f.Store().Open(ctx, h.f.Actor(principal), abs)
	if errors.Is(err, workspace.ErrNotFound) {
		return nil, contextfiles.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if node.Kind != workspace.KindFile {
		return nil, contextfiles.ErrNotExist
	}
	return io.ReadAll(io.LimitReader(r, limit))
}

// ContextSources returns the registry of context files. A component that wants
// the host to load a small index of the user's files into the prompt registers
// a source here; nothing else is needed, and the source applies to every chat
// whatever started it.
func (s *Service) ContextSources() *contextfiles.Registry { return s.ctxfiles.Registry() }

// SetContextWriter plugs in the path that saves notes. Without one the prompt
// does not mention saving.
func (s *Service) SetContextWriter(w contextfiles.Writer) { s.ctxfiles.SetWriter(w) }

// contextSubject says whose notes a chat reads. Everything comes from the chat
// record: the model names a topic, never an agent or a channel. The channel
// folder is the one propose_change writes for the same chat, so a confirmed
// note is read back by the conversation that proposed it. saveTool is the tool
// the chat has to save a note, or empty.
func contextSubject(rec session.ChatRecord, saveTool string) contextfiles.Subject {
	sub := contextfiles.Subject{Principal: rec.Principal, AgentID: rec.AgentID, SaveTool: saveTool}
	if rec.Origin == session.OriginConnector {
		sub.ChannelDir = proposals.ChannelDir(contract.ChangeSource{ID: rec.OriginID, Chat: rec.ID})
	}
	return sub
}

// contextTurn returns the agent profile and the context indexes for the prompt
// of a turn, and a key that changes when either does. Both texts are the same
// at each turn until a file changes, so the transcript does not grow and the
// harness is restarted only for a real change.
func (s *Service) contextTurn(ctx context.Context, rec session.ChatRecord, tools []session.Tool, extra []contextfiles.Source) (text, key string) {
	return s.contextTurnFor(ctx, rec, tools, extra, false)
}

// contextTurnFor is contextTurn; omitEmpty leaves the header out while no
// index has text.
func (s *Service) contextTurnFor(ctx context.Context, rec session.ChatRecord, tools []session.Tool, extra []contextfiles.Source, omitEmpty bool) (text, key string) {
	if !s.d.Files.Available() {
		return "", ""
	}
	sub := contextSubject(rec, saveToolOf(tools))
	sub.OmitEmpty = omitEmpty
	// The profile holds the standing rules of the agent, like its
	// instructions, so every sender gets it. Only the personal notes are held
	// back for an unpaired sender (see personalNotesOpen).
	profile := s.ctxfiles.Profile(ctx, sub)
	index := s.ctxfiles.Block(ctx, sub, extra...)
	return joinBlocks(profile.Text, index.Text), profile.Key + index.Key
}

// saveToolOf is the tool a chat has to save a note, or empty. The prompt names
// it only when the chat has it.
func saveToolOf(tools []session.Tool) string {
	if hasTool(tools, proposeChangeName) {
		return proposeChangeName
	}
	return ""
}

// contextTools is context_read, while a source applies to the chat.
func (s *Service) contextTools(rec session.ChatRecord, tools []session.Tool, extra []contextfiles.Source) []session.Tool {
	if !s.d.Files.Available() || len(s.ctxfiles.Sources(extra...)) == 0 {
		return nil
	}
	sub := contextSubject(rec, saveToolOf(tools))
	return []session.Tool{{
		Name:        contextfiles.ToolName,
		Description: contextfiles.ToolDescription,
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"topic":{"type":"string","description":"The topic name or file name from a line of the index."},` +
			`"scope":{"type":"string","enum":` + scopeEnum(extra) + `,"description":"Where to look. Omit to search the channel, then the agent, then the user."}},` +
			`"required":["topic"]}`),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Topic string `json:"topic"`
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			return s.ctxfiles.Detail(ctx, sub, contextfiles.Scope(args.Scope), args.Topic, extra...)
		},
	}}
}

// scopeEnum is the JSON list of the scopes the sources hold, widest first, so
// the model is offered only the notes the chat reads.
func scopeEnum(sources []contextfiles.Source) string {
	var out []string
	for _, sc := range []contextfiles.Scope{contextfiles.ScopeUser, contextfiles.ScopeAgent, contextfiles.ScopeChannel} {
		for _, src := range sources {
			if src.Scope == sc {
				out = append(out, `"`+string(sc)+`"`)
				break
			}
		}
	}
	return "[" + strings.Join(out, ",") + "]"
}
