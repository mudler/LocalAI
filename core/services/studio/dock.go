package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The dock of a chat: the pages the interface shows beside the conversation,
// in tabs, with the width of the panel. It is stored with the chat record
// (session.Dock), so every frontend and every browser of the user shows the
// same dock for the chat, and a change rides the chat's bus as a dock event.
// Nothing is kept in memory.
//
// The person changes the dock from the interface (SetDock replaces it). The
// assistant opens a page of the user's own home in it with the open_page tool,
// which adds a tab in one atomic step, so a page the person docked meanwhile
// is not lost.

// ErrDockNotAllowed: a sub-agent run has no interface to dock pages in.
var ErrDockNotAllowed = errors.New("a sub-agent run has no dock")

// Dock returns the dock of a chat, nil when it has none.
func (s *Service) Dock(ctx context.Context, user, conv string) (*session.Dock, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return nil, err
	}
	return rec.Dock, nil
}

// SetDock replaces the dock of a chat; a dock with no tab clears it. The
// interface sends the whole dock after each change the person makes.
func (s *Service) SetDock(ctx context.Context, user, conv string, d session.Dock) (*session.Dock, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return nil, err
	}
	if rec.Kind == session.KindSubAgent {
		return nil, ErrDockNotAllowed
	}
	var next *session.Dock
	if len(d.Tabs) > 0 {
		next = d.Clone()
	}
	if err := s.d.Store.UpdateDock(ctx, p, conv, func(*session.Dock) (*session.Dock, error) { return next, nil }); err != nil {
		return nil, err
	}
	s.publishDock(p, conv)
	return next, nil
}

// publishDock tells the watchers of the chat that its dock changed.
func (s *Service) publishDock(p, conv string) {
	s.bus.Publish(key(p, conv), session.Event{Kind: session.KindDock})
}

// openPageName is the tool that opens a page in the dock of the chat. It only
// changes what the interface shows, so it asks no confirmation, like
// page_errors: a page of the user's own home runs as the user anyway when they
// open it. A shared page needs the person's consent in the interface first, so
// the tool refuses it rather than ask for it on their behalf.
const openPageName = "open_page"

const openPageDescription = "Open a page of the user's home in the dock, the panel beside this chat, so the user sees it while you talk. " +
	"Use it after you write or change a page the user asked for, or when they ask to see one. " +
	"name is the folder name of the page under pages/. The tab comes to the front and the panel opens; the user can close it. " +
	"Only the user's own pages: a page of a shared folder is refused, the user opens it from the Pages list."

const openPageSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","description":"The folder name of the page, for example task-board."}},"required":["name"]}`

// pagesHost is the pages service for the tools of the chats.
func (s *Service) pagesHost() *pages.Service {
	return pages.New(s.d.Files)
}

func (s *Service) registerOpenPage() error {
	return s.builtins.Register(builtins.Tool{
		ID: openPageName, Label: "Open page in dock", ReadOnly: true, Unlisted: true,
		Description: "Let the model open a page of the user's home in the dock beside the chat.",
		Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
			switch {
			case !s.d.Files.Available():
				return session.Tool{}, false, "the files are off"
			case c.Origin != "":
				return session.Tool{}, false, "a run that started by itself has no one watching its dock"
			case c.SubAgent:
				return session.Tool{}, false, "a sub-agent run has no dock"
			case workspace.HasFence(c.Actor):
				return session.Tool{}, false, "this agent works in one folder and cannot read the pages of the home"
			case c.Actor.ChatID == "":
				return session.Tool{}, false, "the chat is not known"
			}
			if err := s.mayUsePages(ctx, c.Actor.Principal); err != nil {
				return session.Tool{}, false, err.Error()
			}
			return session.Tool{Description: openPageDescription, Schema: json.RawMessage(openPageSchema),
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
					return s.openPageRun(ctx, c.Actor.Principal, c.Actor.ChatID, raw)
				}}, true, ""
		},
	})
}

func (s *Service) openPageRun(ctx context.Context, p, conv string, raw json.RawMessage) (string, error) {
	var in struct {
		Name   string `json:"name"`
		Folder any    `json:"folder"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("the arguments are not valid: %w", err)
		}
	}
	if in.Folder != nil {
		return "", errors.New("open_page opens only the user's own pages; a page of a shared folder needs the user's consent, so ask them to open it from the Pages list")
	}
	in.Name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(in.Name), "/"), "pages/"))
	page, err := s.pagesHost().Get(ctx, p, in.Name)
	switch {
	case errors.Is(err, pages.ErrBadName):
		return "", errors.New("name must be the folder name of a page: lowercase letters, digits and dashes")
	case errors.Is(err, pages.ErrNotFound):
		return "", fmt.Errorf("there is no page called %s; write pages/%s/index.html first", in.Name, in.Name)
	case err != nil:
		s.log.Warn("studio: open_page could not read the page", "chat", conv, "page", in.Name, "error", err)
		return "", errors.New("the page cannot be read now")
	}
	err = s.d.Store.UpdateDock(ctx, p, conv, func(d *session.Dock) (*session.Dock, error) {
		return d.OpenTab(session.DockTab{Page: page.Name}), nil
	})
	if err != nil {
		s.log.Warn("studio: open_page could not change the dock", "chat", conv, "page", page.Name, "error", err)
		return "", errors.New("the dock of this chat cannot be changed now")
	}
	s.publishDock(p, conv)
	return fmt.Sprintf("The page %s is open in the dock beside the chat. It updates by itself when its files change.", page.Name), nil
}
