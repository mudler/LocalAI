package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// ErrChatBusy means a change needs the chat to be idle and a turn is running.
var ErrChatBusy = errors.New("studio: a turn is running; stop it first")

// ErrInvalidSettings means the settings of a chat are out of range.
var ErrInvalidSettings = errors.New("studio: invalid chat settings")

// Limits of the per-chat settings.
const (
	maxSystemPromptBytes = 32 << 10
	maxTitleRunes        = 200
	minContextSize       = 512
	maxContextSize       = 4 << 20
)

var reasoningEfforts = map[string]bool{"": true, "none": true, "minimal": true, "low": true, "medium": true, "high": true}

// ValidateSettings checks the settings of a chat.
func ValidateSettings(set session.ChatSettings) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSettings, fmt.Sprintf(format, a...))
	}
	switch {
	case len(set.SystemPrompt) > maxSystemPromptBytes:
		return bad("system_prompt is longer than %d bytes", maxSystemPromptBytes)
	case set.Temperature != nil && (*set.Temperature < 0 || *set.Temperature > 2):
		return bad("temperature must be between 0 and 2")
	case set.TopP != nil && (*set.TopP <= 0 || *set.TopP > 1):
		return bad("top_p must be above 0 and at most 1")
	case set.TopK != nil && *set.TopK < 0:
		return bad("top_k must not be negative")
	case !reasoningEfforts[set.ReasoningEffort]:
		return bad("reasoning_effort must be none, minimal, low, medium or high")
	case set.ContextSize != 0 && (set.ContextSize < minContextSize || set.ContextSize > maxContextSize):
		return bad("context_size must be 0 or between %d and %d", minContextSize, maxContextSize)
	}
	return nil
}

// UpdateInput is a change to a chat. A nil field is left alone.
type UpdateInput struct {
	Title *string
	// Model moves the chat to another model. The caller checks that the user
	// may use it.
	Model *string
	// Settings replaces the chat's settings as a whole.
	Settings *session.ChatSettings
}

// Update changes the title, the model or the settings of a chat. The settings
// apply from the next turn. A new title or model needs an idle chat, because
// the title names the chat's folder and a running turn saves its own copy of
// the record: with a turn running it is ErrChatBusy.
func (s *Service) Update(ctx context.Context, user, conv string, in UpdateInput) (session.ChatRecord, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return session.ChatRecord{}, err
	}
	if rec.Kind == session.KindSubAgent {
		return session.ChatRecord{}, ErrSubAgentReadOnly
	}
	if in.Settings != nil {
		if err := ValidateSettings(*in.Settings); err != nil {
			return session.ChatRecord{}, err
		}
	}
	var model *string
	if in.Model != nil {
		m := strings.TrimSpace(*in.Model)
		if m == "" {
			return session.ChatRecord{}, ErrNoModel
		}
		model = &m
	}
	var title string
	if in.Title != nil {
		if title = session.CleanTitle(*in.Title); title == "" || utf8.RuneCountInString(title) > maxTitleRunes {
			return session.ChatRecord{}, fmt.Errorf("%w: the title must have 1 to %d characters", ErrInvalidSettings, maxTitleRunes)
		}
	}
	if in.Settings != nil {
		if err := s.d.Store.SetChatMeta(ctx, p, conv, session.ChatMeta{Settings: in.Settings}); err != nil {
			return session.ChatRecord{}, err
		}
	}
	if in.Title != nil || model != nil {
		err := s.withClaim(ctx, p, conv, func(cur session.ChatRecord) error {
			if model != nil {
				if err := s.d.Store.SetChatMeta(ctx, p, conv, session.ChatMeta{Model: model}); err != nil {
					return err
				}
			}
			if in.Title == nil {
				return nil
			}
			return s.rename(ctx, p, conv, title)
		})
		if err != nil {
			return session.ChatRecord{}, err
		}
	}
	return s.d.Store.GetChat(ctx, p, conv)
}

// rename gives the chat a new title. With a workspace the chat's folder is
// renamed with it, which the loop does.
func (s *Service) rename(ctx context.Context, p, conv, title string) error {
	if !s.d.Files.Available() {
		return s.d.Store.SetChatMeta(ctx, p, conv, session.ChatMeta{Title: &title})
	}
	chat, err := s.chat(ctx, p, conv)
	if err != nil {
		return err
	}
	serr := chat.SetTitle(ctx, title)
	if cerr := chat.Close(); cerr != nil {
		s.log.Warn("studio: could not close the chat after a rename", "chat", conv, "error", cerr)
	}
	return serr
}

// withClaim runs f while this process holds the turn claim of an idle chat, so
// that no turn starts on any frontend meanwhile. The last outcome of the chat
// is kept. A running turn is ErrChatBusy. Messages that arrived during f are
// started afterwards, as after a normal turn.
func (s *Service) withClaim(ctx context.Context, p, conv string, f func(session.ChatRecord) error) error {
	unlock := s.lock(key(p, conv))
	defer unlock()
	prev, err := s.d.Store.Turn(ctx, p, conv)
	if err != nil {
		return err
	}
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return ErrChatBusy
		}
		return err
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err == nil {
		err = f(rec)
	}
	if rerr := s.releaseTurn(ctx, p, conv, runID, prev.Last); rerr != nil {
		s.log.Warn("studio: could not release the claim after a change", "chat", conv, "error", rerr)
	}
	if prev.Last == store.TurnDone || prev.Last == "" {
		if _, serr := s.startFromPending(ctx, p, conv); serr != nil {
			s.log.Warn("studio: could not start the messages that waited during a change", "chat", conv, "error", serr)
		}
	}
	return err
}
