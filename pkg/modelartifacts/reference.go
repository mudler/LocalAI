package modelartifacts

import (
	"fmt"
	"strings"
)

var hfReferencePrefixes = []string{
	"https://huggingface.co/",
	"huggingface://",
	"hf://",
	"hf.co/",
}

// ParsePrimaryReference converts a Hugging Face repository or file reference
// into a managed artifact spec. It accepts repo roots like "owner/repo" and
// direct file references like "huggingface://owner/repo/path/to/model.gguf".
// The boolean return is false when the reference is not Hugging Face-shaped.
func ParsePrimaryReference(raw string) (Spec, bool, error) {
	source, ok, err := ParsePrimarySource(raw)
	if err != nil || !ok {
		return Spec{}, ok, err
	}
	return Spec{
		Name:   TargetModel,
		Target: TargetModel,
		Source: source,
	}, true, nil
}

// ParsePrimarySource converts a Hugging Face repository or file reference into
// a managed source definition. Direct file references are translated into a
// repo plus a single allow pattern so the materializer downloads only the
// selected file.
func ParsePrimarySource(raw string) (Source, bool, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return Source{}, false, nil
	}

	stripped := ref
	for _, prefix := range hfReferencePrefixes {
		stripped = strings.TrimPrefix(stripped, prefix)
	}
	stripped = strings.TrimSuffix(stripped, "/")

	parts := strings.Split(stripped, "/")
	if len(parts) < 2 {
		return Source{}, false, nil
	}
	hadPrefix := ref != stripped

	repo, err := normalizeRepo(strings.Join(parts[:2], "/"))
	if err != nil {
		if ref != stripped {
			return Source{}, false, fmt.Errorf("invalid Hugging Face reference %q: %w", raw, err)
		}
		return Source{}, false, nil
	}
	if !hadPrefix && len(parts) == 2 {
		return Source{}, false, nil
	}

	source := Source{
		Type: SourceTypeHuggingFace,
		Repo: repo,
	}
	if len(parts) == 2 {
		return source, true, nil
	}

	if parts[2] == "resolve" {
		if len(parts) < 5 {
			return Source{}, false, fmt.Errorf("invalid Hugging Face file reference %q", raw)
		}
		source.AllowPatterns = []string{strings.Join(parts[4:], "/")}
		return source, true, nil
	}

	source.AllowPatterns = []string{strings.Join(parts[2:], "/")}
	return source, true, nil
}
