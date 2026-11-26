package content

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidDigest reports whether d is a content digest: sha256:<64 hex>.
func ValidDigest(d string) bool { return digestRE.MatchString(d) }

// DigestHex returns the hex part of a digest, the name of its folder.
func DigestHex(d string) string { return strings.TrimPrefix(d, "sha256:") }

// Digest returns the content digest of a tree: SHA-256 over the sorted lines
// "<path>\0<mode>\0<sha256 of the content>\n". Two commits with the same
// plugin files give one digest.
func Digest(t Tree) string {
	files := make([]File, len(t.Files))
	copy(files, t.Files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write([]byte(f.Mode))
		h.Write([]byte{0})
		h.Write([]byte(f.SHA256))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
