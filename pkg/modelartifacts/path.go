package modelartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type Layout struct {
	Root     string
	CacheKey string
	Lock     string
	// PartialRoot holds every in-flight materialization for every artifact.
	// Individual writers never share a subtree of it.
	PartialRoot string
	// Partial is this writer's private staging tree. It is empty until
	// WithWriter stamps a writer identity onto the layout, because a partial
	// tree belongs to one process run and the layout alone cannot name it.
	Partial  string
	Final    string
	Snapshot string
	Manifest string
}

// WithWriter returns a copy of the layout whose partial tree belongs
// exclusively to writerID.
//
// Every writer used to stage into the same `.partial/<cacheKey>`, which made
// the artifact lock a correctness dependency: two writers that both believed
// they held it opened the same blob with O_APPEND, interleaved their bytes into
// one file, and read each other's in-flight size when probing for a resume
// point (#10981 put a real cluster in exactly that state, because flock(2) on
// CIFS reports contention as EACCES). Suffixing the writer identity makes that
// impossible by construction, so a lock failure now costs a duplicated download
// rather than a corrupted one.
func (l Layout) WithWriter(writerID string) (Layout, error) {
	if !writerIDPattern.MatchString(writerID) {
		return Layout{}, fmt.Errorf("invalid artifact writer id")
	}
	if !cacheKeyPattern.MatchString(l.CacheKey) || l.PartialRoot == "" {
		return Layout{}, fmt.Errorf("writer layout requires a resolved cache key")
	}
	l.Partial = filepath.Join(l.PartialRoot, l.CacheKey+"."+writerID)
	return l, nil
}

// splitPartialDirName recovers the (cacheKey, writerID) pair a partial tree
// name encodes, reporting false for anything this package did not create. The
// sweep and the adoption scan both refuse to touch a name they cannot parse, so
// an unrelated directory that happens to sit under `.partial` is never removed.
func splitPartialDirName(name string) (cacheKey string, writerID string, ok bool) {
	key, writer, found := strings.Cut(name, ".")
	if !found || !cacheKeyPattern.MatchString(key) || !writerIDPattern.MatchString(writer) {
		return "", "", false
	}
	return key, writer, true
}

func CacheKey(spec Spec) (string, error) {
	normalized, err := spec.Normalize()
	if err != nil {
		return "", err
	}
	if normalized.Resolved == nil {
		return "", fmt.Errorf("cache key requires resolved artifact state")
	}
	identity := struct {
		Type           string   `json:"type"`
		Endpoint       string   `json:"endpoint"`
		Repo           string   `json:"repo"`
		Revision       string   `json:"revision"`
		AllowPatterns  []string `json:"allow_patterns,omitempty"`
		IgnorePatterns []string `json:"ignore_patterns,omitempty"`
	}{
		Type: normalized.Source.Type, Endpoint: normalized.Resolved.Endpoint,
		Repo: normalized.Source.Repo, Revision: normalized.Resolved.Revision,
		AllowPatterns: normalized.Source.AllowPatterns, IgnorePatterns: normalized.Source.IgnorePatterns,
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func RelativeSnapshotPath(cacheKey string) (string, error) {
	if !cacheKeyPattern.MatchString(cacheKey) {
		return "", fmt.Errorf("invalid artifact cache key")
	}
	return filepath.Join(".artifacts", "huggingface", cacheKey, "snapshot"), nil
}

func LayoutFor(modelsPath string, spec Spec) (Layout, error) {
	if spec.Resolved == nil || !cacheKeyPattern.MatchString(spec.Resolved.CacheKey) {
		return Layout{}, fmt.Errorf("layout requires a resolved cache key")
	}
	root := filepath.Join(modelsPath, ".artifacts")
	final := filepath.Join(root, "huggingface", spec.Resolved.CacheKey)
	return Layout{
		Root:        root,
		CacheKey:    spec.Resolved.CacheKey,
		Lock:        filepath.Join(root, ".locks", spec.Resolved.CacheKey+".lock"),
		PartialRoot: filepath.Join(root, ".partial"),
		Final:       final,
		Snapshot:    filepath.Join(final, "snapshot"),
		Manifest:    filepath.Join(final, "manifest.json"),
	}, nil
}

func ValidateRelativeHubPath(candidate string) error {
	if candidate == "" || filepath.IsAbs(candidate) || strings.ContainsAny(candidate, "\\\x00") {
		return fmt.Errorf("unsafe Hub path %q", candidate)
	}
	parts := strings.SplitSeq(candidate, "/")
	for part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe Hub path %q", candidate)
		}
	}
	return nil
}
