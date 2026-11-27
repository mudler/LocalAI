package skills

import (
	"path"
	"slices"
	"strings"
)

// noDescription stands in for the description of a plugin skill that has
// none. The plugin format does not require one, and a skill with an empty
// line in the index is still worth offering.
const noDescription = "(no description)"

// ValidNameIn reports whether s can be the name of a skill of the layer l.
// The plugin layer takes only a qualified name, "<plugin>:<skill>", with two
// valid names; every other layer takes a plain name.
func ValidNameIn(l Layer, s string) bool {
	if l != LayerPlugin {
		return ValidName(s)
	}
	plugin, name, ok := strings.Cut(s, ":")
	return ok && ValidName(plugin) && ValidName(name)
}

// pluginSkills returns the skills of a plugin layer root, from the list the
// root carries. It reads no file: the package never changes, and the
// Plugins service checked it at install.
func pluginSkills(r Root) []Skill {
	base := cleanPath(r.Path)
	guest := strings.TrimSuffix(r.Guest, "/")
	out := make([]Skill, 0, len(r.PluginSkills))
	for _, ps := range r.PluginSkills {
		sk := Skill{
			Name: r.PluginTitle + ":" + ps.Name, Layer: LayerPlugin,
			Plugin: r.Plugin, PluginTitle: r.PluginTitle, PluginInstall: r.PluginInstall, PluginRoot: guest,
			NoModel: ps.NoModel, NoMenu: ps.NoMenu, ArgumentHint: Clean(ps.ArgumentHint),
			Requires: slices.Clone(ps.Requires),
		}
		// "." is a plugin whose root folder is its one skill.
		dir := path.Clean(ps.Dir)
		if dir == ".." || strings.HasPrefix(dir, "../") || path.IsAbs(dir) {
			sk.Invalid = ErrBadName.Error()
			out = append(out, sk)
			continue
		}
		sk.Path, sk.GuestPath = base, guest+"/"+SkillFile
		if dir != "." {
			sk.Path, sk.GuestPath = base+"/"+dir, guest+"/"+dir+"/"+SkillFile
		}
		if !ValidNameIn(LayerPlugin, sk.Name) {
			sk.Invalid = ErrBadName.Error()
			out = append(out, sk)
			continue
		}
		sk.Description = Clean(ps.Description)
		if sk.Description == "" {
			sk.Description = noDescription
		}
		if len(sk.Description) > MaxDescription {
			sk.Description = strings.ToValidUTF8(sk.Description[:MaxDescription], "")
		}
		out = append(out, sk)
	}
	return out
}
