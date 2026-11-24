package nodes

import (
	"path"
	"strings"
)

// A path on a worker uses the separator of the worker's OS, which is not
// always the OS of the frontend: a Linux frontend can drive a Windows worker
// and the other way round. The path/filepath package uses the frontend's
// separator, so it must not take a worker path apart or build one. These
// helpers read the style from the worker path itself.

// isWindowsStylePath reports whether p looks like a Windows path: it starts
// with a drive letter ("C:") or a UNC prefix, or it uses backslashes and no
// forward slash.
func isWindowsStylePath(p string) bool {
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return true
	}
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return strings.Contains(p, `\`) && !strings.Contains(p, "/")
}

// remoteSeparator is the separator to use when building a path on the worker
// that produced p.
func remoteSeparator(p string) string {
	if isWindowsStylePath(p) {
		return `\`
	}
	return "/"
}

// remoteBase returns the last element of a worker path, with either
// separator.
func remoteBase(p string) string {
	trimmed := strings.TrimRight(p, `/\`)
	if trimmed == "" {
		return p
	}
	if i := strings.LastIndexAny(trimmed, `/\`); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// remoteDirOf returns all but the last element of a worker path.
func remoteDirOf(p string) string {
	trimmed := strings.TrimRight(p, `/\`)
	i := strings.LastIndexAny(trimmed, `/\`)
	if i < 0 {
		return "."
	}
	if i == 0 || (i == 2 && isWindowsStylePath(trimmed)) {
		// Keep the root: "/" or "C:\".
		return trimmed[:i+1]
	}
	return trimmed[:i]
}

// remoteJoin appends a relative path to a worker directory, with the
// separator of that worker. elem may use either separator.
func remoteJoin(dir, elem string) string {
	sep := remoteSeparator(dir)
	elem = strings.Trim(strings.ReplaceAll(elem, `\`, "/"), "/")
	if sep == "/" {
		return path.Join(dir, elem)
	}
	if elem == "" {
		return dir
	}
	return strings.TrimRight(dir, `/\`) + sep + strings.ReplaceAll(elem, "/", sep)
}

// trimRemoteSuffix removes the relative path rel, written with either
// separator, from the end of the worker path remotePath. ok is false when
// remotePath does not end with rel.
func trimRemoteSuffix(remotePath, rel string) (string, bool) {
	rel = strings.Trim(strings.ReplaceAll(rel, `\`, "/"), "/")
	if rel == "" || rel == "." {
		return remotePath, true
	}
	normalized := strings.ReplaceAll(remotePath, `\`, "/")
	if normalized != rel && !strings.HasSuffix(normalized, "/"+rel) {
		return remotePath, false
	}
	// The normalization swaps single characters, so the lengths match.
	return remotePath[:len(remotePath)-len(rel)], true
}

// cleanRemoteDir removes trailing separators from a worker directory, but
// keeps a root ("/" or "C:\").
func cleanRemoteDir(dir string) string {
	if !isWindowsStylePath(dir) {
		if dir == "" {
			return "."
		}
		return path.Clean(dir)
	}
	trimmed := strings.TrimRight(dir, `/\`)
	if len(trimmed) == 2 && trimmed[1] == ':' {
		return trimmed + `\`
	}
	return trimmed
}
