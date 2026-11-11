package session

import (
	"errors"
	"fmt"
	"regexp"
)

// The dock of a chat is the side panel of the interface that shows pages of
// the workspace in tabs beside the conversation. It lives with the chat
// record, so the pages a chat docked come back with the chat on every
// frontend and in every browser. The store's UpdateDock changes it;
// UpdateChat leaves it alone, so a turn that saves its own copy of the record
// does not undo a page the person or the assistant docked meanwhile.

// Bounds of a dock.
const (
	// MaxDockTabs is the most pages one dock holds. Opening another one drops
	// the oldest tab.
	MaxDockTabs = 6
	// MinDockWidth and MaxDockWidth bound the stored width in CSS pixels. The
	// interface also keeps it under 60% of its window; 0 is the default width.
	MinDockWidth = 320
	MaxDockWidth = 4096
)

var dockPageName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ErrInvalidDock means a dock is out of its bounds.
var ErrInvalidDock = errors.New("studio: invalid dock")

// DockTab is one page of a dock: a page of the owner's home (Folder 0) or of
// a shared folder (its id). A shared page still needs the viewer's consent
// before it runs; the dock only names it.
type DockTab struct {
	Page   string `json:"page"`
	Folder int64  `json:"folder,omitempty"`
}

// Dock is the dock of one chat.
type Dock struct {
	// Open says whether the panel shows; a closed dock keeps its tabs.
	Open bool      `json:"open"`
	Tabs []DockTab `json:"tabs"`
	// Active is the index of the tab in front.
	Active int `json:"active"`
	// Width is the width of the panel in CSS pixels, 0 for the default.
	Width int `json:"width,omitempty"`
}

// Clone returns a copy of d that shares nothing with it.
func (d *Dock) Clone() *Dock {
	if d == nil {
		return nil
	}
	cp := *d
	cp.Tabs = append([]DockTab(nil), d.Tabs...)
	return &cp
}

// Validate checks that d is inside the bounds of a dock: valid page names, no
// page twice, at most MaxDockTabs, an active index of a tab and a width of 0
// or between the bounds.
func (d Dock) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidDock, fmt.Sprintf(format, a...))
	}
	if len(d.Tabs) > MaxDockTabs {
		return bad("a dock holds at most %d pages", MaxDockTabs)
	}
	seen := map[DockTab]bool{}
	for _, t := range d.Tabs {
		switch {
		case !dockPageName.MatchString(t.Page):
			return bad("%q is not the name of a page", t.Page)
		case t.Folder < 0:
			return bad("folder must be the id of a shared folder or 0")
		case seen[t]:
			return bad("the page %s is docked twice", t.Page)
		}
		seen[t] = true
	}
	if d.Active < 0 || (len(d.Tabs) > 0 && d.Active >= len(d.Tabs)) || (len(d.Tabs) == 0 && d.Active != 0) {
		return bad("active must be the index of a tab")
	}
	if d.Width != 0 && (d.Width < MinDockWidth || d.Width > MaxDockWidth) {
		return bad("width must be 0 or between %d and %d", MinDockWidth, MaxDockWidth)
	}
	return nil
}

// OpenTab returns d with tab in front and the panel open: a page already
// docked comes to the front, a new one is added at the end, and the oldest
// tab is dropped when the dock is full. A nil d is an empty dock.
func (d *Dock) OpenTab(tab DockTab) *Dock {
	out := d.Clone()
	if out == nil {
		out = &Dock{}
	}
	out.Open = true
	for i, t := range out.Tabs {
		if t == tab {
			out.Active = i
			return out
		}
	}
	out.Tabs = append(out.Tabs, tab)
	if len(out.Tabs) > MaxDockTabs {
		out.Tabs = out.Tabs[len(out.Tabs)-MaxDockTabs:]
	}
	out.Active = len(out.Tabs) - 1
	return out
}
