// Package builtinskills holds writing-skills, localai-workspace and localai-apps.
// The tree is embedded in the binary and synchronized into the built-in layer
// when the server starts. Document authoring procedures belong to add-ons.
package builtinskills

import (
	"embed"
	"io/fs"
)

//go:embed all:tree
var embedded embed.FS

// FS returns the tree, rooted at the folder that holds one folder per skill.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "tree")
	if err != nil {
		panic(err) // the folder is embedded at build time
	}
	return sub
}
