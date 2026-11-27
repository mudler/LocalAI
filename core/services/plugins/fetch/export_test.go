package fetch

import (
	"context"
	"net"

	"github.com/mudler/nib/plugin/claudeplugin"
)

// SetWrapGit lets a test watch the git commands of a fetcher.
func (f *Fetcher) SetWrapGit(w func(claudeplugin.GitFunc) claudeplugin.GitFunc) { f.wrapGit = w }

// SetLookup replaces the DNS lookup of the private-address check.
func (f *Fetcher) SetLookup(l func(ctx context.Context, host string) ([]net.IPAddr, error)) {
	f.lookup = l
}
