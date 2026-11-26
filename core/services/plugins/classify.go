package plugins

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/mudler/nib/plugin/claudeplugin"
)

// reservedNames are the names of the skill layers. A plugin with such a name
// is refused, because "<layer>:<name>" is already the switch ID form of a
// skill (spec 10.1).
var reservedNames = []string{"folder", "project", "agent", "personal", "shared", "plugin", "addon", "builtin"}

// ReservedName reports whether a plugin name is a skill layer name.
func ReservedName(name string) bool { return slices.Contains(reservedNames, name) }

// CodeReasons returns the code reasons of a plugin (spec 9.1): nib's reasons,
// shell injections in skill and command bodies included. Empty means a
// text-only plugin.
func CodeReasons(p *claudeplugin.Plugin) []string {
	r := p.CodeReasons()
	if r == nil {
		r = []string{}
	}
	return r
}

// checkPluginName refuses a plugin whose name LocalAI cannot use.
func checkPluginName(p *claudeplugin.Plugin) error {
	if err := claudeplugin.ValidateName(p.Name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if ReservedName(p.Name) {
		return fmt.Errorf("%w: the plugin name %q is reserved", ErrInvalid, p.Name)
	}
	return nil
}

// addsCodeReason reports whether next has a code reason that prev has not.
func addsCodeReason(prev, next []string) bool {
	for _, r := range next {
		if !slices.Contains(prev, r) {
			return true
		}
	}
	return false
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
