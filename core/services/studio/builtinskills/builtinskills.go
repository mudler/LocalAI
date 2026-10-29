// Package builtinskills holds the skills that ship with the server: the office
// skills docx, xlsx, pptx and pdf, writing-skills and localai-workspace. The
// tree is embedded in the binary and written into the built-in layer when the
// server starts. The
// office skills are adapted from the Feather project (MIT); the NOTICE file in
// the tree keeps its source and licence, and must stay with them.
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
