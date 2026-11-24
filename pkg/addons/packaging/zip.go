// SPDX-License-Identifier: MIT
package packaging

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/addons/zipformat"
)

// cmdZip writes the zip of a verified platform layout, and its sha256 file.
// The files are those of the OCI package, so the zip is not a second build
// path: whatever the layout passed in verify is what the zip holds.
func cmdZip(args []string) error {
	fs := flag.NewFlagSet("zip", flag.ContinueOnError)
	dir := fs.String("layout", "", "platform OCI layout")
	platform := fs.String("platform", "", "expected os/arch")
	version := fs.String("version", "", "expected version")
	config := fs.String("config", "addons/tables/package/platforms.yaml", "platform matrix file")
	outDir := fs.String("out", "", "directory that receives <id>-<version>-<os>-<arch>.zip and its .sha256 file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outDir == "" || *dir == "" {
		return errors.New("--layout and --out are required")
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	v, err := verifyLeg(*dir, *platform, *version, cfg)
	if err != nil {
		return err
	}
	entries := make([]zipformat.Entry, 0, len(v.Files))
	for _, f := range v.Files {
		entries = append(entries, zipformat.Entry{Name: f.name, Data: f.data, Exec: f.exec})
	}
	var buf bytes.Buffer
	m := v.Manifest
	if err := zipformat.Write(&buf, m.ID, m.Version, m.OS, m.Arch, m.Executable, entries); err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o750); err != nil {
		return err
	}
	name := zipformat.FileName(m.ID, m.Version, m.OS, m.Arch)
	zipPath := filepath.Join(*outDir, name)
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil { // #nosec G306 -- a distributable file
		return err
	}
	sum := sha256.Sum256(buf.Bytes())
	line := fmt.Sprintf("%x  %s\n", sum, name)
	if err := os.WriteFile(zipPath+".sha256", []byte(line), 0o644); err != nil { // #nosec G306 -- a distributable file
		return err
	}
	_, _ = fmt.Fprintf(stdout, "zip=%s\nsha256=%x\nsize=%d\n", zipPath, sum, buf.Len())
	return nil
}
