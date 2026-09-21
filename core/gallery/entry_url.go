package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/oci"
)

// isRelativeEntryURL reports whether an entry url names a document inside the
// gallery rather than a source of its own.
//
// The test is "no scheme at all" rather than a list of shapes to accept: every
// transport the downloader knows announces itself with a prefix, and the bare
// registry hosts it also accepts (quay.io, ghcr.io, docker.io) are covered by
// LooksLikeOCI. Anything left is a path, and a path can only mean a path in
// the gallery it was published in.
func isRelativeEntryURL(entryURL string) bool {
	if entryURL == "" {
		return false
	}
	if strings.Contains(entryURL, "://") {
		return false
	}
	uri := downloader.URI(entryURL)
	return !uri.LooksLikeURL() && !uri.LooksLikeOCI()
}

// ociGalleryRoot returns the unpacked artifact directory a gallery's entries
// resolve against, or "" when the gallery is not an OCI one or nothing was
// unpacked for it.
//
// Mirrors are considered, and in the same order the fetch tries them: an OCI
// gallery whose primary was unreachable is served by a mirror, and the entries
// of the copy that actually landed on disk are the ones that can be installed.
func ociGalleryRoot(g config.Gallery, basePath string) string {
	for _, candidate := range galleryCandidates(g) {
		if !looksLikeOCIGallery(candidate) {
			continue
		}
		dir := ociGalleryCacheDir(basePath, candidate)
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// resolveGalleryEntryURL turns an entry url that is relative to the gallery
// root into one the downloader can fetch, and leaves every other url alone.
//
// A gallery published as a self-contained tree, which is what an OCI gallery
// is, names its base configs by their place in that tree: url: base/virtual.yaml
// means the file next to the index, not a host somewhere. Without this the
// string reaches the HTTP client verbatim and the entry cannot be installed at
// all.
//
// The relative path may not climb out of the gallery root. It is published
// data, so an entry that resolved above the root would let a gallery read
// whatever it pointed at on the machine listing it.
func resolveGalleryEntryURL(entryURL string, g config.Gallery, basePath string) (string, error) {
	if !isRelativeEntryURL(entryURL) {
		return entryURL, nil
	}

	if root := ociGalleryRoot(g, basePath); root != "" {
		target, err := oci.ResolveInRoot(root, entryURL)
		if err != nil {
			return "", fmt.Errorf("entry url %q is not a path inside gallery %q: %w", entryURL, g.Name, err)
		}
		return downloader.LocalPrefix + target, nil
	}

	if looksLikeOCIGallery(g.URL) {
		// The index came from the artifact, so the artifact is on disk. If it
		// is not, the entry cannot be read from anywhere and saying so beats
		// resolving it against a directory that is not the gallery.
		return "", fmt.Errorf("entry url %q needs the unpacked artifact of gallery %q, which is not in the cache", entryURL, g.Name)
	}

	return resolveAgainstIndexURL(g.URL, entryURL)
}

// galleryConfigReadRoot returns the directory a gallery config read is
// confined to.
//
// The downloader keeps a file:// read inside the base path it is given, and
// that is normally the models directory. An entry of an OCI gallery lives in
// the unpacked artifact instead, which is deliberately a sibling of the models
// directory so unpacked YAML is never mistaken for an installed model config.
// Reading it therefore needs the cache root as its own trusted root; the
// downloader still resolves symlinks against it, so the confinement the models
// directory gave is not lost, only moved to the directory the file is actually
// in.
func galleryConfigReadRoot(url, basePath string) string {
	root := ociGalleryCacheRoot(basePath)
	if root == "" || !strings.HasPrefix(url, downloader.LocalPrefix) {
		return basePath
	}
	target := filepath.Clean(strings.TrimPrefix(url, downloader.LocalPrefix))
	if target == root || strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return root
	}
	return basePath
}

// resolveAgainstIndexURL resolves a relative entry url against the directory
// of the index URL.
//
// The URL is handled as text, the way findGalleryURLFromReferenceURL already
// handles a .ref file, because these are not all RFC 3986 URLs: github: and
// hf:// carry their own syntax. The one piece of that syntax that must survive
// is the @branch a github: URL ends with, which belongs to the whole reference
// and not to the file name.
func resolveAgainstIndexURL(indexURL, relative string) (string, error) {
	clean, err := oci.SafeRelativePath(relative)
	if err != nil {
		return "", fmt.Errorf("entry url %q is not a path inside the gallery: %w", relative, err)
	}

	cut := strings.LastIndex(indexURL, "/")
	if cut < 0 {
		return "", fmt.Errorf("gallery url %q names no directory to resolve %q against", indexURL, relative)
	}

	base, suffix := indexURL, ""
	if at := strings.Index(indexURL[cut:], "@"); at >= 0 {
		suffix = indexURL[cut+at:]
		base = indexURL[:cut+at]
	}
	return base[:cut+1] + clean + suffix, nil
}
