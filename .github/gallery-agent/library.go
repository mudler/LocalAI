package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"gopkg.in/yaml.v3"
)

// appendLibraryEntries converts gallery-shaped entry texts to model library
// entries and appends them to the library file at path. It returns the ids it
// added and the ids it skipped because the library already has them.
//
// The entries are appended as text, after the existing content, so the diff of
// the library is only the new entries. The result is decoded again with the
// reader the host uses before it is written, so a malformed entry fails the
// run instead of reaching a pull request.
func appendLibraryEntries(path string, galleryEntries []string) (added, skipped []string, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	current, err := gallery.DecodeModelIndex(content)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode %s as a model library: %w", path, err)
	}
	ids := make(map[string]struct{}, len(current.Entries))
	for _, e := range current.Entries {
		ids[e.ID] = struct{}{}
	}

	var entries []gallery.ModelLibraryEntry
	for _, text := range galleryEntries {
		var models []gallery.GalleryModel
		if err := yaml.Unmarshal([]byte(text), &models); err != nil {
			return nil, nil, fmt.Errorf("generated entry does not decode: %w", err)
		}
		for _, m := range models {
			entry, err := gallery.ModelLibraryEntryFromGallery(m)
			if err != nil {
				return nil, nil, fmt.Errorf("converting %q to a library entry: %w", m.Name, err)
			}
			if _, dup := ids[entry.ID]; dup {
				skipped = append(skipped, entry.ID)
				continue
			}
			ids[entry.ID] = struct{}{}
			entries = append(entries, entry)
			added = append(added, entry.ID)
		}
	}
	if len(entries) == 0 {
		return nil, skipped, nil
	}

	encoded, err := gallery.EncodeModelIndex(entries)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding library entries: %w", err)
	}

	out := string(content)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += string(encoded)
	if _, err := gallery.DecodeModelIndex([]byte(out)); err != nil {
		return nil, nil, fmt.Errorf("the library with the new entries does not decode: %w", err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return nil, nil, fmt.Errorf("failed to write %s: %w", path, err)
	}
	return added, skipped, nil
}
