package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"sigs.k8s.io/yaml"
)

var galleryIndexPath = os.Getenv("GALLERY_INDEX_PATH")

// getGalleryIndexPath returns the gallery index file path, with a default fallback
func getGalleryIndexPath() string {
	if galleryIndexPath != "" {
		return galleryIndexPath
	}
	return "gallery/index.yaml"
}

type galleryModel struct {
	Name string   `yaml:"name"`
	Urls []string `yaml:"urls"`
}

// loadGalleryURLSet parses gallery/index.yaml once and returns the set of
// HuggingFace model URLs already present in the gallery.
func loadGalleryURLSet() (map[string]struct{}, error) {
	indexPath := getGalleryIndexPath()
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", indexPath, err)
	}

	var galleryModels []galleryModel
	if err := yaml.Unmarshal(content, &galleryModels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", indexPath, err)
	}

	set := make(map[string]struct{}, len(galleryModels))
	for _, gm := range galleryModels {
		for _, u := range gm.Urls {
			set[u] = struct{}{}
		}
	}

	// Also skip URLs already proposed in open (unmerged) gallery-agent PRs.
	// The workflow injects these via EXTRA_SKIP_URLS so we don't keep
	// re-proposing the same model every run while a PR is waiting to merge.
	for _, line := range strings.FieldsFunc(os.Getenv("EXTRA_SKIP_URLS"), func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		u := strings.TrimSpace(line)
		if u != "" {
			set[u] = struct{}{}
		}
	}

	return set, nil
}

// modelAlreadyInGallery checks whether a HuggingFace model repo is already
// referenced in the gallery URL set.
func modelAlreadyInGallery(set map[string]struct{}, modelID string) bool {
	_, ok := set["https://huggingface.co/"+modelID]
	return ok
}

// baseModelFromTags returns the first `base_model:<repo>` value found in the
// tag list, or "" if none is present. HuggingFace surfaces the base model
// declared in the model card's YAML frontmatter as such a tag.
func baseModelFromTags(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "base_model:") {
			return strings.TrimPrefix(t, "base_model:")
		}
	}
	return ""
}

// licenseFromTags returns the `license:<id>` value from the tag list, or "".
func licenseFromTags(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "license:") {
			return strings.TrimPrefix(t, "license:")
		}
	}
	return ""
}

// curatedTags produces the gallery tag list from HuggingFace's raw tag set.
// Always includes llm + gguf, then adds whitelisted family / capability
// markers when they appear in the HF tag list.
func curatedTags(hfTags []string) []string {
	whitelist := []string{
		"gpu", "cpu",
		"llama", "mistral", "mixtral", "qwen", "qwen2", "qwen3",
		"gemma", "gemma2", "gemma3", "phi", "phi3", "phi4",
		"deepseek", "yi", "falcon", "command-r",
		"vision", "multimodal", "code", "chat",
		"instruction-tuned", "reasoning", "thinking",
	}
	seen := map[string]struct{}{}
	out := []string{"llm", "gguf"}
	seen["llm"] = struct{}{}
	seen["gguf"] = struct{}{}

	hfSet := map[string]struct{}{}
	for _, t := range hfTags {
		hfSet[strings.ToLower(t)] = struct{}{}
	}
	for _, w := range whitelist {
		if _, ok := hfSet[w]; ok {
			if _, dup := seen[w]; !dup {
				out = append(out, w)
				seen[w] = struct{}{}
			}
		}
	}
	return out
}

// resolveReadme fetches a description-quality README for a (possibly
// quantized) repo: if a `base_model:` tag is present, fetch the base repo's
// README; otherwise fall back to the repo's own README.
func resolveReadme(client *hfapi.Client, modelID string, hfTags []string) (string, error) {
	if base := baseModelFromTags(hfTags); base != "" && base != modelID {
		if content, err := client.GetReadmeContent(base, "README.md"); err == nil && strings.TrimSpace(content) != "" {
			return cleanTextContent(content), nil
		}
	}
	content, err := client.GetReadmeContent(modelID, "README.md")
	if err != nil {
		return "", err
	}
	return cleanTextContent(content), nil
}

// extractDescription turns a raw HuggingFace README into a concise plain-text
// description suitable for embedding in gallery/index.yaml: strips YAML
// frontmatter, HTML tags/comments, markdown images, link URLs (keeping the
// link text), markdown tables, and then truncates at a paragraph boundary
// around ~1200 characters. Raw README should still be used for icon
// extraction — call this only for the `description:` field.
func extractDescription(readme string) string {
	s := readme

	// Strip leading YAML frontmatter: `---\n...\n---\n` at start of file.
	if strings.HasPrefix(strings.TrimLeft(s, " \t\n"), "---") {
		trimmed := strings.TrimLeft(s, " \t\n")
		rest := strings.TrimPrefix(trimmed, "---")
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			after := rest[idx+len("\n---"):]
			after = strings.TrimPrefix(after, "\n")
			s = after
		}
	}

	// Strip HTML comments and tags.
	s = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(s, "")

	// Strip markdown images entirely.
	s = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`).ReplaceAllString(s, "")
	// Replace markdown links `[text](url)` with just `text`.
	s = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`).ReplaceAllString(s, "$1")

	// Drop table lines and horizontal rules, and flatten all leading
	// whitespace: generateYAMLEntry embeds this under a `description: |`
	// literal block whose indentation is set by the first non-empty line.
	// If any line has extra leading whitespace (e.g. from an indented
	// `<p align="center">` block in the original README), YAML will pick
	// that up as the block's indent and every later line at a smaller
	// indent blows the block scalar. Stripping leading whitespace here
	// guarantees uniform 4-space indentation after formatTextContent runs.
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimLeft(line, " \t")
		ts := strings.TrimSpace(t)
		if strings.HasPrefix(ts, "|") {
			continue
		}
		if strings.HasPrefix(ts, ":--") || strings.HasPrefix(ts, "---") || strings.HasPrefix(ts, "===") {
			continue
		}
		kept = append(kept, t)
	}
	s = strings.Join(kept, "\n")

	// Normalise whitespace and drop any leading blank lines so the literal
	// block in YAML doesn't start with a blank first line (which would
	// break the indentation detector the same way).
	s = cleanTextContent(s)
	s = strings.TrimLeft(s, " \t\n")

	// Truncate at a paragraph boundary around maxLen chars.
	const maxLen = 1200
	if len(s) > maxLen {
		cut := strings.LastIndex(s[:maxLen], "\n\n")
		if cut < maxLen/3 {
			cut = maxLen
		}
		s = strings.TrimRight(s[:cut], " \t\n") + "\n\n..."
	}

	return s
}

// cleanTextContent removes trailing spaces/tabs and collapses multiple empty
// lines so README content embeds cleanly into YAML without lint noise.
func cleanTextContent(text string) string {
	lines := strings.Split(text, "\n")
	var cleaned []string
	var prevEmpty bool
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" {
			if !prevEmpty {
				cleaned = append(cleaned, "")
			}
			prevEmpty = true
		} else {
			cleaned = append(cleaned, trimmed)
			prevEmpty = false
		}
	}
	return strings.TrimRight(strings.Join(cleaned, "\n"), "\n")
}

// extractIconFromReadme scans README content for an image URL usable as a
// gallery entry icon.
func extractIconFromReadme(readmeContent string) string {
	if readmeContent == "" {
		return ""
	}

	markdownImageRegex := regexp.MustCompile(`(?i)!\[[^\]]*\]\(([^)]+\.(png|jpg|jpeg|svg|webp|gif))\)`)
	htmlImageRegex := regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+\.(png|jpg|jpeg|svg|webp|gif))["']`)
	plainImageRegex := regexp.MustCompile(`(?i)https?://[^\s<>"']+\.(png|jpg|jpeg|svg|webp|gif)`)

	if m := markdownImageRegex.FindStringSubmatch(readmeContent); len(m) > 1 && strings.HasPrefix(strings.ToLower(m[1]), "http") {
		return strings.TrimSpace(m[1])
	}
	if m := htmlImageRegex.FindStringSubmatch(readmeContent); len(m) > 1 && strings.HasPrefix(strings.ToLower(m[1]), "http") {
		return strings.TrimSpace(m[1])
	}
	if m := plainImageRegex.FindStringSubmatch(readmeContent); len(m) > 0 && strings.HasPrefix(strings.ToLower(m[0]), "http") {
		return strings.TrimSpace(m[0])
	}
	return ""
}

// getHuggingFaceAvatarURL returns the HF avatar URL for a user, or "".
func getHuggingFaceAvatarURL(author string) string {
	if author == "" {
		return ""
	}
	userURL := fmt.Sprintf("https://huggingface.co/api/users/%s/overview", author)
	resp, err := http.Get(userURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var info map[string]any
	if err := json.Unmarshal(body, &info); err != nil {
		return ""
	}
	if v, ok := info["avatarUrl"].(string); ok && v != "" {
		return v
	}
	if v, ok := info["avatar"].(string); ok && v != "" {
		return v
	}
	return ""
}

// extractModelIcon extracts an icon URL from the README, falling back to the
// HuggingFace user avatar.
func extractModelIcon(model ProcessedModel) string {
	if icon := extractIconFromReadme(model.ReadmeContent); icon != "" {
		return icon
	}
	if model.Author != "" {
		if avatar := getHuggingFaceAvatarURL(model.Author); avatar != "" {
			return avatar
		}
	}
	return ""
}
