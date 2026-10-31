package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// Errors of the transcript edits.
var (
	// ErrNothingToRegenerate means the chat has no user message to answer again.
	ErrNothingToRegenerate = errors.New("studio: the chat has no message to answer again")
	// ErrNotUserMessage means the entry named for an edit is not a user message.
	ErrNotUserMessage = errors.New("studio: only a message you sent can be edited")
)

// rebuildState makes the resume state of a chat whose transcript was cut or
// copied. A test harness has none: the next turn then replays the end of the
// transcript through the system prompt, as for any chat without a state.
func (s *Service) rebuildState(entries []session.Entry) []byte {
	if s.d.Harness != nil {
		return nil
	}
	state, err := nibharness.TranscriptState(entries)
	if err != nil {
		s.log.Warn("studio: could not rebuild the model state of a chat", "error", err)
		return nil
	}
	return state
}

// Regenerate answers the last message of the chat again. The last turn's user
// messages stay; everything the model said after them is removed, and a new
// turn starts. Files that the removed turn changed stay changed.
func (s *Service) Regenerate(ctx context.Context, user, conv string) (SendResult, error) {
	p := principal(user)
	return s.rerun(ctx, p, conv, func(entries []session.Entry) (int64, session.Message, error) {
		last := -1
		for i, entrie := range slices.Backward(entries) {
			if entrie.Role == session.RoleUser {
				last = i
				break
			}
		}
		if last < 0 {
			return 0, session.Message{}, ErrNothingToRegenerate
		}
		// A turn can hold several user messages: the first, and those sent
		// while it ran. They are answered together again, in order.
		first := last
		if turn := entries[last].Turn; turn > 0 {
			for i := last - 1; i >= 0 && entries[i].Turn == turn; i-- {
				if entries[i].Role == session.RoleUser {
					first = i
				}
			}
		}
		// The words, the files and the text read from them go again.
		var (
			texts []string
			msg   session.Message
		)
		for _, e := range entries[first:] {
			if e.Role != session.RoleUser {
				continue
			}
			d := session.DisplayEntry(e)
			if d.Text != "" {
				texts = append(texts, d.Text)
			}
			msg.Note += d.Note
			msg.Attachments = append(msg.Attachments, d.Attachments...)
			if msg.Invoked == nil {
				msg.Invoked = d.Invoked
			}
		}
		msg.Text = strings.Join(texts, "\n\n")
		return entries[first].Seq - 1, msg, nil
	})
}

// EditAndRerun replaces the user message with sequence number seq by text,
// removes everything after it and starts a new turn.
func (s *Service) EditAndRerun(ctx context.Context, user, conv string, seq int64, text string) (SendResult, error) {
	if strings.TrimSpace(text) == "" {
		return SendResult{}, fmt.Errorf("%w: empty message", session.ErrInvalid)
	}
	return s.rerun(ctx, principal(user), conv, func(entries []session.Entry) (int64, session.Message, error) {
		for _, e := range entries {
			if e.Seq != seq {
				continue
			}
			if e.Role != session.RoleUser {
				return 0, session.Message{}, ErrNotUserMessage
			}
			// The edit replaces the words; the files of the message stay.
			d := session.DisplayEntry(e)
			return seq - 1, session.Message{Text: text, Note: d.Note, Attachments: d.Attachments, Invoked: d.Invoked}, nil
		}
		return 0, session.Message{}, session.ErrNotFound
	})
}

// rerun cuts the transcript where pick says and runs the message as a new
// turn, all under one turn claim so that nothing starts between the cut and
// the turn.
func (s *Service) rerun(ctx context.Context, p, conv string, pick func([]session.Entry) (keep int64, msg session.Message, err error)) (SendResult, error) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return SendResult{}, err
	}
	if rec.Kind == session.KindSubAgent {
		return SendResult{}, ErrSubAgentReadOnly
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	prev, err := s.d.Store.Turn(ctx, p, conv)
	if err != nil {
		return SendResult{}, err
	}
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return SendResult{}, ErrChatBusy
		}
		return SendResult{}, err
	}
	msg, err := s.cut(ctx, p, conv, pick)
	if err != nil {
		if rerr := s.releaseTurn(ctx, p, conv, runID, prev.Last); rerr != nil {
			s.log.Warn("studio: could not release the claim after a failed cut", "chat", conv, "error", rerr)
		}
		return SendResult{}, err
	}
	s.recommand(ctx, p, rec, &msg)
	if err := s.startTurn(ctx, p, conv, runID, msg); err != nil {
		return SendResult{}, err
	}
	return SendResult{Started: true}, nil
}

// recommand gives a message that runs again the instructions of the command it
// was sent with. A skill or an agent that is gone leaves the message plain; the
// chip stays, as it was what the person sent.
func (s *Service) recommand(ctx context.Context, p string, rec session.ChatRecord, msg *session.Message) {
	if msg.Invoked == nil {
		return
	}
	cmd := Command{Skill: msg.Invoked.Name}
	if msg.Invoked.Kind == session.InvokeAgent {
		cmd = Command{Agent: msg.Invoked.ID}
	}
	directive, _, err := s.commandMessage(ctx, p, rec, msg.Text, cmd)
	if err != nil {
		s.log.Info("studio: the command of a message that runs again is gone", "chat", rec.ID, "error", err)
		return
	}
	msg.Directive = directive
}

// cut removes the entries pick names and rebuilds the model state from those
// that stay. It returns the message to run.
func (s *Service) cut(ctx context.Context, p, conv string, pick func([]session.Entry) (int64, session.Message, error)) (session.Message, error) {
	entries, err := s.d.Store.Entries(ctx, conv, 0, 0)
	if err != nil {
		return session.Message{}, err
	}
	keep, msg, err := pick(entries)
	if err != nil {
		return session.Message{}, err
	}
	if err := s.d.Store.TruncateEntries(ctx, p, conv, keep); err != nil {
		return session.Message{}, err
	}
	var kept []session.Entry
	for _, e := range entries {
		if e.Seq <= keep {
			kept = append(kept, e)
		}
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return session.Message{}, err
	}
	rec.HarnessState, rec.Updated = s.rebuildState(kept), time.Now().UTC()
	if err := s.d.Store.UpdateChat(ctx, rec); err != nil {
		return session.Message{}, err
	}
	return msg, nil
}

// Fork copies the chat up to and including the entry with sequence number
// upTo into a new chat, which has the same model, settings and switches. With
// upTo 0 the whole transcript is copied. The new chat has no folder and no
// pending messages; the next message starts it.
func (s *Service) Fork(ctx context.Context, user, conv string, upTo int64, title string) (session.ChatRecord, error) {
	p := principal(user)
	src, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return session.ChatRecord{}, err
	}
	entries, err := s.d.Store.Entries(ctx, conv, 0, 0)
	if err != nil {
		return session.ChatRecord{}, err
	}
	var kept []session.Entry
	for _, e := range entries {
		if (upTo == 0 || e.Seq <= upTo) && e.Role != session.RoleCheckpoint && e.Role != session.RoleGoal {
			kept = append(kept, e)
		}
	}
	if upTo > 0 && len(kept) == 0 {
		return session.ChatRecord{}, session.ErrNotFound
	}
	if title = session.CleanTitle(title); title == "" {
		title = session.CleanTitle(src.Title + " (fork)")
	}
	now := time.Now().UTC()
	rec := session.ChatRecord{
		ID: newID(), Principal: p, Title: title, Created: now, Updated: now, Kind: session.KindChat,
		Model: src.Model, AgentID: src.AgentID, Assistant: src.Assistant, Settings: src.Settings,
		Egress: forkEgress(src.Egress),
	}
	if err := s.d.Store.CreateChat(ctx, rec); err != nil {
		return session.ChatRecord{}, err
	}
	fail := func(err error) (session.ChatRecord, error) {
		_ = s.d.Store.DeleteChat(ctx, p, rec.ID) // no half-copied chat is left behind
		return session.ChatRecord{}, err
	}
	for _, e := range kept {
		if rec.Turns < e.Turn {
			rec.Turns = e.Turn
		}
		if _, err := s.d.Store.AppendEntry(ctx, rec.ID, e); err != nil {
			return fail(err)
		}
	}
	for _, set := range []struct {
		off []string
		fn  func(context.Context, string, string, []string) error
	}{
		{src.SkillsOff, s.d.Store.SetSkillsOff}, {src.AddonsOff, s.d.Store.SetAddonsOff}, {src.McpOff, s.d.Store.SetMcpOff},
		{src.BuiltinsOff, s.d.Store.SetBuiltinsOff}, {src.BuiltinsOn, s.d.Store.SetBuiltinsOn},
	} {
		if len(set.off) == 0 {
			continue
		}
		if err := set.fn(ctx, p, rec.ID, set.off); err != nil {
			return fail(err)
		}
	}
	rec.HarnessState = s.rebuildState(kept)
	if err := s.d.Store.UpdateChat(ctx, rec); err != nil {
		return fail(err)
	}
	return s.d.Store.GetChat(ctx, p, rec.ID)
}
