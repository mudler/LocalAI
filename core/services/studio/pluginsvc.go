package studio

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// ErrBadPluginSwitch means a plugin switch does not name an install, or there
// are too many switches.
var ErrBadPluginSwitch = errors.New("studio: a plugin switch is not valid")

// maxPluginSwitches bounds the plugin switches of one chat, as
// maxSkillSwitches does for skills.
const maxPluginSwitches = 200

// pluginInstallShape is the shape of a plugin install ID.
var pluginInstallShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SetPluginsOff sets the plugins a chat has switched off, by install ID,
// replacing the earlier list. A chat keeps what is off, so a plugin the user
// enables later is on in it. A switch never adds a plugin: for a chat with an
// agent it only narrows what the agent grants. An ID appears once. The chat of
// another user is session.ErrNotFound.
func (s *Service) SetPluginsOff(ctx context.Context, user, conv string, off []string) error {
	clean, err := cleanPluginSwitches(off)
	if err != nil {
		return err
	}
	return s.d.Store.SetPluginsOff(ctx, principal(user), conv, clean)
}

// cleanPluginSwitches checks and dedups a list of plugin switches.
func cleanPluginSwitches(off []string) ([]string, error) {
	if len(off) > maxPluginSwitches {
		return nil, fmt.Errorf("%w: at most %d switches", ErrBadPluginSwitch, maxPluginSwitches)
	}
	seen := make(map[string]bool, len(off))
	clean := make([]string, 0, len(off))
	for _, id := range off {
		if !pluginInstallShape.MatchString(id) {
			return nil, fmt.Errorf("%w: %q", ErrBadPluginSwitch, id)
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	return clean, nil
}
