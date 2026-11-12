package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/config/harnessctx"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Sources of the window of a chat in Harness.Source.
const (
	// HarnessSourceModel means the window of the model is the one that is
	// too small. Only an administrator can change it.
	HarnessSourceModel = "model"
	// HarnessSourceChat means the model is large enough but the chat's own
	// context size setting is below the minimum. The owner of the chat can
	// change that.
	HarnessSourceChat = "chat"
)

// Harness says whether a chat has the context window the chat agent needs.
// It is what the chat page uses to show a banner before the first turn fails.
type Harness struct {
	// OK is false when the effective window is below Minimum. A window that
	// is not known (a remote model) is OK: nothing can be said about it.
	OK bool
	// Window is the window the chat runs with, in tokens. Zero when unknown.
	Window int
	// Minimum and Recommended are the windows for the role of the user.
	Minimum     int
	Recommended int
	// CanFix says whether this user may change what makes the window too
	// small: an administrator for the model, anyone for their own chat
	// setting.
	CanFix bool
	// Admin says whether the minimum is the administrator's.
	Admin bool
	// Source names what limits the window when OK is false (HarnessSource*).
	Source string
}

// Harness computes the window check of a chat for a user. The role comes from
// Deps.IsAdmin; without it authentication is off and everyone is the
// administrator. The window of the
// model is the one its backend loads. The chat's own context size changes
// only what the chat loop plans with, so it can lower the effective window
// but never raise the real one.
func (s *Service) Harness(ctx context.Context, user string, rec session.ChatRecord) Harness {
	admin := s.isAdmin(ctx, user)
	minimum, recommended := harnessctx.For(admin)
	h := Harness{OK: true, Minimum: minimum, Recommended: recommended, Admin: admin}

	model := 0
	if s.d.ContextTokens != nil {
		model = s.d.ContextTokens(rec.Model)
	}
	chat := rec.Settings.ContextSize
	h.Window = model
	if h.Window == 0 {
		h.Window = chat
	}
	switch {
	case model > 0 && model < minimum:
		h.OK, h.Source, h.CanFix = false, HarnessSourceModel, admin
		h.Window = model
	case chat > 0 && chat < minimum:
		h.OK, h.Source, h.CanFix = false, HarnessSourceChat, true
		h.Window = chat
	}
	return h
}
