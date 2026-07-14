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
	Root            string
	Lock            string
	Partial         string
	PartialSnapshot string
	Final           string
	Snapshot        string
	Manifest        string
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
	partial := filepath.Join(root, ".partial", spec.Resolved.CacheKey)
	final := filepath.Join(root, "huggingface", spec.Resolved.CacheKey)
	return Layout{
		Root:            root,
		Lock:            filepath.Join(root, ".locks", spec.Resolved.CacheKey+".lock"),
		Partial:         partial,
		PartialSnapshot: filepath.Join(partial, "snapshot"),
		Final:           final,
		Snapshot:        filepath.Join(final, "snapshot"),
		Manifest:        filepath.Join(final, "manifest.json"),
	}, nil
}

func ValidateRelativeHubPath(candidate string) error {
	if candidate == "" || filepath.IsAbs(candidate) || strings.ContainsAny(candidate, "\\\x00") {
		return fmt.Errorf("unsafe Hub path %q", candidate)
	}
	parts := strings.Split(candidate, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe Hub path %q", candidate)
		}
	}
	return nil
}
