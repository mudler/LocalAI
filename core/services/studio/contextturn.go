package studio

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// homeReader reads the files of a user's home as the user, outside the fence
// of any chat. The loader names the path, never the model, so a scoped chat
// reads its own notes without gaining any access to the files.
type homeReader struct{ f *files.Service }

func (h homeReader) ReadFile(ctx context.Context, principal, rel string, limit int64) ([]byte, error) {
	if !h.f.Available() {
		return nil, contextfiles.ErrNotExist
	}
	abs, err := h.f.Abs(principal, rel)
	if err != nil {
		return nil, err
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
// record: the model names a topic, never an agent or a channel.
func contextSubject(rec session.ChatRecord) contextfiles.Subject {
	sub := contextfiles.Subject{Principal: rec.Principal, AgentID: rec.AgentID}
	if rec.Origin == session.OriginConnector {
		sub.Channel = rec.OriginID
	}
	return sub
}

// contextTurn returns the agent profile and the context indexes for the prompt
// of a turn, and a key that changes when either does. Both texts are the same
// at each turn until a file changes, so the transcript does not grow and the
// harness is restarted only for a real change.
func (s *Service) contextTurn(ctx context.Context, rec session.ChatRecord) (text, key string) {
	if !s.d.Files.Available() {
		return "", ""
	}
	sub := contextSubject(rec)
	profile, index := s.ctxfiles.Profile(ctx, sub), s.ctxfiles.Block(ctx, sub)
	return joinBlocks(profile.Text, index.Text), profile.Key + index.Key
}

// contextTools is memory_read, while a source is registered.
func (s *Service) contextTools(rec session.ChatRecord) []session.Tool {
	if !s.d.Files.Available() || len(s.ctxfiles.Registry().Sources()) == 0 {
		return nil
	}
	sub := contextSubject(rec)
	return []session.Tool{{
		Name:        contextfiles.ToolName,
		Description: contextfiles.ToolDescription,
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"topic":{"type":"string","description":"The topic name or file name from a line of the index."},` +
			`"scope":{"type":"string","enum":["user","agent","channel"],"description":"Where to look. Omit to search the channel, then the agent, then the user."}},` +
			`"required":["topic"]}`),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Topic string `json:"topic"`
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			return s.ctxfiles.Detail(ctx, sub, contextfiles.Scope(args.Scope), args.Topic)
		},
	}}
}
