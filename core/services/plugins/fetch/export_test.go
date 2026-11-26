package fetch

import "github.com/mudler/nib/plugin/claudeplugin"

// SetWrapGit lets a test watch the git commands of a fetcher.
func (f *Fetcher) SetWrapGit(w func(claudeplugin.GitFunc) claudeplugin.GitFunc) { f.wrapGit = w }
