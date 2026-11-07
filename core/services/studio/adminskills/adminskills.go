// Package adminskills turns the admin recipes of localaitools into built-in
// skills, one localai-admin-<recipe> skill each.
//
// The recipe text lives once, in the embedded prompts of localaitools, where
// the standalone MCP server sends it inline. The chats load a recipe by name
// instead, so the built-in layer needs a SKILL.md for each. This package
// builds them from the same files; there is no second copy in the skill tree.
package adminskills

import (
	"io/fs"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/localaitools"
)

// With returns base with a folder <name>/SKILL.md added for each admin recipe.
// A file of base at the same path is replaced.
func With(base fs.FS) fs.FS {
	out := fstest.MapFS{}
	err := fs.WalkDir(base, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(base, p)
		if err != nil {
			return err
		}
		out[p] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		panic(err) // base is an embedded tree
	}
	for _, r := range localaitools.Recipes() {
		out[r.Name+"/SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: " + r.Name + "\ndescription: " + r.Description + "\n---\n\n" + r.Body)}
	}
	return out
}
