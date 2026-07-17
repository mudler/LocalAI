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
	HuggingFaceTokenEnv   = "HF_TOKEN"
)

var (
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	cacheKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
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
}

func (s Spec) Normalize() (Spec, error) {
	if strings.TrimSpace(s.Name) == "" {
		s.Name = TargetModel
	}
	if strings.TrimSpace(s.Target) == "" {
		s.Target = TargetModel
	}
	s.Name = strings.TrimSpace(s.Name)
	s.Target = strings.TrimSpace(s.Target)
	s.Source.Type = strings.TrimSpace(s.Source.Type)
	s.Source.TokenEnv = strings.TrimSpace(s.Source.TokenEnv)

	if s.Name != TargetModel || s.Target != TargetModel {
		return Spec{}, fmt.Errorf("only artifact name/target %q is supported", TargetModel)
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
