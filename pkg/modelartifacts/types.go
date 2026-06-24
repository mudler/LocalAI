package modelartifacts

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	SourceTypeHuggingFace = "huggingface"
	TargetModel           = "model"
	// TargetCompanion marks a snapshot that supports the primary model rather
	// than being it: a composed pipeline may pull its tokenizer, text encoder or
	// VAE from a separate repository. Companions are surfaced to the backend as
	// named options, never as the load target.
	TargetCompanion     = "companion"
	HuggingFaceTokenEnv = "HF_TOKEN"
)

var (
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	cacheKeyPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	writerIDPattern     = regexp.MustCompile(`^[0-9a-f]{16}$`)
	artifactNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

type Spec struct {
	Name     string    `yaml:"name" json:"name"`
	Target   string    `yaml:"target" json:"target"`
	Source   Source    `yaml:"source" json:"source"`
	Resolved *Resolved `yaml:"resolved,omitempty" json:"resolved,omitempty"`
}

type Source struct {
	Type           string   `yaml:"type" json:"type"`
	Repo           string   `yaml:"repo" json:"repo"`
	Revision       string   `yaml:"revision,omitempty" json:"revision,omitempty"`
	TokenEnv       string   `yaml:"token_env,omitempty" json:"token_env,omitempty"`
	AllowPatterns  []string `yaml:"allow_patterns,omitempty" json:"allow_patterns,omitempty"`
	IgnorePatterns []string `yaml:"ignore_patterns,omitempty" json:"ignore_patterns,omitempty"`
}

type Resolved struct {
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	Revision string `yaml:"revision" json:"revision"`
	CacheKey string `yaml:"cache_key" json:"cache_key"`
	// PrimaryFile is the slash-separated path, relative to the snapshot root, of
	// the single model file to load when the resolved snapshot contains exactly
	// one file. Single-file backends (llama.cpp, whisper, ...) must be pointed at
	// this file rather than the snapshot directory. It is empty for multi-file
	// snapshots, where the snapshot directory itself is the load target (e.g. the
	// Python/transformers backends consuming a full repo).
	PrimaryFile string `yaml:"primary_file,omitempty" json:"primary_file,omitempty"`
}

func (s Spec) Normalize() (Spec, error) {
	if strings.TrimSpace(s.Target) == "" {
		s.Target = TargetModel
	}
	s.Name = strings.TrimSpace(s.Name)
	s.Target = strings.TrimSpace(s.Target)
	// The primary is always named "model": the whole codebase addresses it
	// positionally as Artifacts[0], so a second spelling would buy nothing and
	// break the pre-companion configs that omit the field entirely.
	if s.Target == TargetModel && s.Name == "" {
		s.Name = TargetModel
	}
	s.Source.Type = strings.TrimSpace(s.Source.Type)
	s.Source.TokenEnv = strings.TrimSpace(s.Source.TokenEnv)

	switch s.Target {
	case TargetModel:
		if s.Name != TargetModel {
			return Spec{}, fmt.Errorf("the primary artifact name must be %q, got %q", TargetModel, s.Name)
		}
	case TargetCompanion:
		// A companion's name is the option key the backend receives, so it has
		// to survive that round trip unambiguously.
		if !artifactNamePattern.MatchString(s.Name) {
			return Spec{}, fmt.Errorf("companion artifact name %q must match %s", s.Name, artifactNamePattern)
		}
		if s.Name == TargetModel {
			return Spec{}, fmt.Errorf("companion artifact name %q is reserved for the primary", TargetModel)
		}
	default:
		return Spec{}, fmt.Errorf("unsupported artifact target %q", s.Target)
	}
	if s.Source.Type != SourceTypeHuggingFace {
		return Spec{}, fmt.Errorf("unsupported artifact source type %q", s.Source.Type)
	}
	if s.Source.Type != SourceTypeHuggingFace {
		return Spec{}, fmt.Errorf("unsupported artifact source type %q", s.Source.Type)
	}

	repo, err := normalizeRepo(s.Source.Repo)
	if err != nil {
		return Spec{}, err
	}
	s.Source.Repo = repo
	if strings.TrimSpace(s.Source.Revision) == "" {
		s.Source.Revision = "main"
	} else {
		s.Source.Revision = strings.TrimSpace(s.Source.Revision)
	}
	if strings.ContainsRune(s.Source.Revision, '\x00') {
		return Spec{}, fmt.Errorf("revision contains NUL")
	}
	if s.Source.TokenEnv != "" && s.Source.TokenEnv != HuggingFaceTokenEnv {
		return Spec{}, fmt.Errorf("token_env must be empty or %s", HuggingFaceTokenEnv)
	}

	for _, patterns := range [][]string{s.Source.AllowPatterns, s.Source.IgnorePatterns} {
		for _, pattern := range patterns {
			if err := validatePattern(pattern); err != nil {
				return Spec{}, err
			}
		}
	}
	s.Source.AllowPatterns = slices.Clone(s.Source.AllowPatterns)
	s.Source.IgnorePatterns = slices.Clone(s.Source.IgnorePatterns)
	slices.Sort(s.Source.AllowPatterns)
	slices.Sort(s.Source.IgnorePatterns)

	if s.Resolved != nil {
		resolved := *s.Resolved
		resolved.Endpoint, err = normalizeEndpoint(resolved.Endpoint)
		if err != nil {
			return Spec{}, err
		}
		resolved.Revision = strings.ToLower(strings.TrimSpace(resolved.Revision))
		if !commitPattern.MatchString(resolved.Revision) {
			return Spec{}, fmt.Errorf("resolved revision must be 40 lowercase hexadecimal characters")
		}
		if resolved.CacheKey != "" && !cacheKeyPattern.MatchString(resolved.CacheKey) {
			return Spec{}, fmt.Errorf("resolved cache key must be 64 lowercase hexadecimal characters")
		}
		resolved.PrimaryFile = strings.TrimSpace(resolved.PrimaryFile)
		if resolved.PrimaryFile != "" {
			// PrimaryFile exists to point a single-file backend at the weight
			// file inside a snapshot. A companion is never the load target, so
			// carrying one would encode an intent nothing acts on.
			if s.Target == TargetCompanion {
				return Spec{}, fmt.Errorf("companion artifact %q must not declare primary_file", s.Name)
			}
			if err := ValidateRelativeHubPath(resolved.PrimaryFile); err != nil {
				return Spec{}, err
			}
		}
		s.Resolved = &resolved
	}

	return s, nil
}

func (s Spec) Validate() error {
	normalized, err := s.Normalize()
	if err != nil {
		return err
	}
	if normalized.Resolved != nil && normalized.Resolved.CacheKey == "" {
		return fmt.Errorf("resolved cache key is required in installed state")
	}
	return nil
}

func normalizeRepo(raw string) (string, error) {
	repo := strings.TrimSpace(raw)
	for _, prefix := range []string{"https://huggingface.co/", "huggingface://", "hf://"} {
		repo = strings.TrimPrefix(repo, prefix)
	}
	repo = strings.TrimSuffix(repo, "/")
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, "?#\\\x00") {
		return "", fmt.Errorf("Hugging Face repo must be exactly owner/repo")
	}
	return repo, nil
}

// CanonicalRepo normalizes a Hugging Face repository reference to owner/repo.
func CanonicalRepo(raw string) (string, error) {
	return normalizeRepo(raw)
}

func validatePattern(pattern string) error {
	if pattern == "" || path.IsAbs(pattern) || strings.ContainsAny(pattern, "\\\x00") {
		return fmt.Errorf("invalid artifact pattern %q", pattern)
	}
	for part := range strings.SplitSeq(pattern, "/") {
		if part == ".." {
			return fmt.Errorf("invalid artifact pattern %q", pattern)
		}
	}
	return nil
}

func normalizeEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid resolved Hugging Face endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}
