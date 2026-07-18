// Command gallery_denormalize fills the read-only denormalized fields on the
// candidates of gallery model entries: backend, quantization, and
// inferred_min_vram.
//
// It never modifies an authored min_vram. Authored values are authoritative,
// because a human who measured a real load knows more than a pre-download
// estimate does.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/vram"
)

const estimateContext = 8192

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gallery_denormalize <index.yaml>")
		os.Exit(1)
	}
	path := os.Args[1]

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
		os.Exit(1)
	}

	var entries []gallery.GalleryModel
	if err := yaml.Unmarshal(data, &entries); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
		os.Exit(1)
	}

	byName := map[string]gallery.GalleryModel{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	failures := 0
	for i := range entries {
		if !entries[i].HasCandidates() {
			continue
		}
		for j := range entries[i].Candidates {
			c := &entries[i].Candidates[j]

			target, ok := byName[c.Model]
			if !ok {
				fmt.Fprintf(os.Stderr, "%s: candidate %q not found\n", entries[i].Name, c.Model)
				failures++
				continue
			}

			// The concrete entry's payload usually lives behind its url
			// rather than inline, so fetch it. Metadata.Backend is populated
			// at load time by the running server, not by parsing the index,
			// so it is always empty here and cannot be copied.
			resolved, err := gallery.GetGalleryConfigFromURLWithContext[gallery.ModelConfig](ctx, target.URL, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: cannot fetch config for %q: %v\n", entries[i].Name, c.Model, err)
				failures++
				continue
			}

			c.Backend = backendOf(target, resolved)
			c.Quantization = quantizationOf(target)

			// Authored values win, and the final unconstrained candidate is
			// deliberately floorless, so neither gets an inferred value.
			// Clearing first matters: a candidate that gained an authored
			// min_vram, or that became the last resort after a reorder, would
			// otherwise keep a stale inferred floor that makes
			// EffectiveMinVRAM report a constraint the entry no longer has.
			if c.MinVRAM != "" || j == len(entries[i].Candidates)-1 {
				c.InferredMinVRAM = ""
				continue
			}

			// Weight files can come from either side: a concrete index entry
			// usually lists them itself (files:, i.e. AdditionalFiles) while
			// the url it points at is only a base config, but some entries
			// invert that. Install time downloads both, so estimate over both.
			files := make([]vram.FileInput, 0, len(target.AdditionalFiles)+len(resolved.Files))
			for _, f := range target.AdditionalFiles {
				files = append(files, vram.FileInput{URI: f.URI})
			}
			for _, f := range resolved.Files {
				files = append(files, vram.FileInput{URI: f.URI})
			}

			estimate, err := vram.EstimateModelMultiContext(ctx, vram.ModelEstimateInput{
				Files: files,
				Size:  target.Size,
			}, []uint32{estimateContext})
			if err != nil || estimate.VRAMForContext(estimateContext) == 0 {
				fmt.Fprintf(os.Stderr,
					"%s: could not estimate min_vram for %q (%v); author one by hand\n",
					entries[i].Name, c.Model, err)
				failures++
				continue
			}
			c.InferredMinVRAM = fmt.Sprintf("%dMiB", estimate.VRAMForContext(estimateContext)/(1024*1024))
		}
	}

	if err := writeCandidates(path, data, entries); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
		os.Exit(1)
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "%d candidate(s) need a hand-authored min_vram\n", failures)
		os.Exit(1)
	}
}

// writeCandidates writes back only the three derived keys on each candidate,
// leaving every other byte of the index alone.
//
// Round-tripping through []GalleryModel would reflow all 1200+ entries
// (quoting, key order, line wrapping), burying the handful of real changes in
// reformatting noise and making the nightly PR unreviewable. Even re-encoding
// just the candidates subtree would restyle authored fields such as min_vram,
// so the rewrite reaches down to individual mapping keys and the node tree is
// re-encoded at the index's authored indent. That also makes the job
// idempotent: a run that computes the same values leaves the file untouched
// and opens no PR.
func writeCandidates(path string, data []byte, entries []gallery.GalleryModel) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("re-parse for node rewrite: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("unexpected document shape")
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode {
		return fmt.Errorf("index root is not a sequence")
	}
	if len(root.Content) != len(entries) {
		return fmt.Errorf("entry count drift: %d nodes vs %d entries", len(root.Content), len(entries))
	}

	changed := false
	for i, entryNode := range root.Content {
		if !entries[i].HasCandidates() || entryNode.Kind != yaml.MappingNode {
			continue
		}

		candidatesNode := mappingValue(entryNode, "candidates")
		if candidatesNode == nil || candidatesNode.Kind != yaml.SequenceNode {
			return fmt.Errorf("entry %q has candidates but no candidates sequence in the document", entries[i].Name)
		}
		if len(candidatesNode.Content) != len(entries[i].Candidates) {
			return fmt.Errorf("entry %q candidate count drift: %d nodes vs %d parsed",
				entries[i].Name, len(candidatesNode.Content), len(entries[i].Candidates))
		}

		for j, candidateNode := range candidatesNode.Content {
			if candidateNode.Kind != yaml.MappingNode {
				return fmt.Errorf("entry %q candidate %d is not a mapping", entries[i].Name, j)
			}
			c := entries[i].Candidates[j]
			for _, kv := range []struct{ key, value string }{
				{"backend", c.Backend},
				{"quantization", c.Quantization},
				{"inferred_min_vram", c.InferredMinVRAM},
			} {
				if setMappingValue(candidateNode, kv.key, kv.value) {
					changed = true
				}
			}
		}
	}

	// Nothing to rewrite means nothing to encode: leave the file, and its
	// mtime, alone.
	if !changed {
		return nil
	}

	// yaml.Marshal encodes at yaml.v3's default 4-space indent, which reflows
	// every nested block in the file (~6000 lines against the real index) and
	// drowns the handful of rewritten keys. The index is authored at 2, so
	// encode at 2 and the diff stays limited to what actually changed.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("flush encoder: %w", err)
	}

	// The encoder does not emit a document start marker, so restore the one the
	// file was authored with rather than deleting it on every run.
	out := buf.Bytes()
	if header := documentHeader(data); header != nil && !bytes.HasPrefix(out, header) {
		out = append(header, out...)
	}

	// Preserve the file's existing mode instead of forcing 0644.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	return os.WriteFile(path, out, info.Mode().Perm())
}

// documentHeader returns the leading document start marker line, or nil when the
// file was authored without one.
func documentHeader(data []byte) []byte {
	for _, marker := range [][]byte{[]byte("---\n"), []byte("---\r\n")} {
		if bytes.HasPrefix(data, marker) {
			return marker
		}
	}
	return nil
}

// mappingValue returns the value node for key, or nil when absent.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMappingValue sets key to value, appending the key when absent and dropping
// it when value is empty so a field that stops being derivable does not leave a
// stale number behind. It reports whether the document actually changed.
func setMappingValue(mapping *yaml.Node, key, value string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if value == "" {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
		if mapping.Content[i+1].Value == value {
			return false
		}
		mapping.Content[i+1] = scalarNode(value)
		return true
	}
	if value == "" {
		return false
	}
	mapping.Content = append(mapping.Content, scalarNode(key), scalarNode(value))
	return true
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// backendOf mirrors the priority used by resolveBackend in
// core/gallery/backend_resolve.go: an entry's overrides win over whatever the
// referenced config file declares.
func backendOf(m gallery.GalleryModel, resolved gallery.ModelConfig) string {
	if b, ok := m.Overrides["backend"].(string); ok && b != "" {
		return b
	}
	var cfg struct {
		Backend string `yaml:"backend"`
	}
	if err := yaml.Unmarshal([]byte(resolved.ConfigFile), &cfg); err == nil {
		return cfg.Backend
	}
	return ""
}

// quantizationOf reports the quantization declared by a concrete entry's
// overrides, empty when it declares none.
func quantizationOf(m gallery.GalleryModel) string {
	if q, ok := m.Overrides["quantization"].(string); ok {
		return q
	}
	return ""
}
