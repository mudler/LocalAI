// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/xlog"
)

// sweepPackages removes the published package folders that nothing can use
// again. A failed upgrade publishes its candidate before it tries to start it,
// and the journal records the generation only when it starts, so every
// failed attempt leaves a whole package behind.
//
// A package is kept when the owner journal names it in any generation of any
// add-on (installed or not, so the previous generation stays for a
// rollback), in the next state of any operation, when a canceled upgrade
// holds it, or when an unfinished data recovery upgrade names its digest.
// When one of those sources cannot be read, nothing is removed.
//
// The caller holds opMu and mu, so no operation publishes a package meanwhile.
func (m *Manager) sweepPackages() {
	if m.installer == nil {
		return
	}
	keep, pending, reason := m.packageReferences()
	if reason != "" {
		xlog.Warn("Skipped the sweep of unreferenced add-on packages", "reason", reason)
		return
	}
	root, e := openPackageRoot(m.installer.Root)
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		xlog.Warn("Skipped the sweep of unreferenced add-on packages", "reason", "cannot open the package folder", "error", e)
		return
	}
	defer func() { _ = root.Close() }()
	entries, e := fs.ReadDir(root.FS(), ".")
	if e != nil {
		xlog.Warn("Skipped the sweep of unreferenced add-on packages", "reason", "cannot list the package folder", "error", e)
		return
	}
	var removed int
	var freed int64
	for _, entry := range entries {
		name := entry.Name()
		// The entry type comes from the folder listing, not from a stat
		// through a link: a link named like a package is never a package.
		if !strings.HasPrefix(name, "package-") || !entry.IsDir() || keep[name] {
			continue
		}
		var receipt packages.PackageReceipt
		b, e := fs.ReadFile(root.FS(), name+"/receipt.json")
		if e == nil {
			e = json.Unmarshal(b, &receipt)
		}
		if e != nil {
			// Commit writes the receipt before the folder gets its name, so
			// this is not a package this host published. Leave it to a person.
			xlog.Warn("Kept an add-on package folder whose receipt cannot be read", "directory", name, "error", e)
			continue
		}
		if slices.Contains(pending[receipt.Manifest.ID], receipt.Digest) {
			continue
		}
		size := treeSize(root, name)
		if e := removeReadOnlyTree(root, name); e != nil {
			xlog.Warn("Could not remove an unreferenced add-on package", "addonId", receipt.Manifest.ID, "directory", name, "error", e)
			continue
		}
		removed++
		freed += size
		xlog.Info("Removed an unreferenced add-on package", "addonId", receipt.Manifest.ID, "directory", name, "bytes", size)
	}
	if removed > 0 {
		xlog.Info("Removed the unreferenced add-on packages", "count", removed, "freedBytes", freed)
	}
}

// packageReferences returns the base names of the package folders the owner
// journal and the in-memory upgrades name, and the candidate digests of the
// unfinished recovery upgrades by add-on. A non-empty reason means the
// references are not known for sure.
func (m *Manager) packageReferences() (map[string]bool, map[string][]string, string) {
	root := filepath.Clean(m.installer.Root)
	keep := map[string]bool{}
	reason := ""
	add := func(r packages.PackageReceipt) {
		if r.Directory == "" || reason != "" {
			return
		}
		dir := filepath.Clean(r.Directory)
		if filepath.Dir(dir) != root {
			// A moved owner root, or a receipt this code did not write: the
			// names in the folder cannot be matched with the journal.
			reason = "the journal names a package outside the package folder: " + dir
			return
		}
		keep[filepath.Base(dir)] = true
	}
	for _, st := range m.journal.Addons {
		for _, g := range st.Generations {
			add(g.Receipt)
		}
	}
	for _, op := range m.journal.Operations {
		if op.Next != nil {
			for _, g := range op.Next.Generations {
				add(g.Receipt)
			}
		}
	}
	for _, r := range m.canceled {
		add(r)
	}
	if reason != "" {
		return nil, nil, reason
	}
	pending, e := recovery.PendingUpgrades(m.config.Root)
	if e != nil {
		return nil, nil, "a data recovery file cannot be read: " + e.Error()
	}
	return keep, pending, ""
}

// treeSize is the size of the regular files of one tree, for the log. Links
// are not followed. An error only makes the number smaller.
func treeSize(root *os.Root, name string) int64 {
	var size int64
	_ = fs.WalkDir(root.FS(), name, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			size += info.Size()
		}
		return nil
	})
	return size
}
