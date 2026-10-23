// SPDX-License-Identifier: MIT
package catalog

// SchemaVersion is the version of the shared envelope that the kind-specific
// index files (models today) write. A reader refuses an entry with a higher
// version than it knows, because a newer envelope may change the meaning of a
// field it already reads.
const SchemaVersion = 1

// Envelope is the common header of a library entry. Every kind shares it and
// adds a body of its own next to it, so a tool that lists, filters or shows
// entries needs one reader for the header and no knowledge of the body.
//
// The add-on and backend index files use Release, which names the same
// identifier id (catalog.Release.ID). An index written before that still holds
// addonId; ReadIndex reads it as id for one release (LegacyIDField) and every
// writer emits id. Release has no license, urls, tags, icon or schemaVersion
// yet; moving it to this whole header is a later step.
type Envelope struct {
	Kind        Kind     `json:"kind" yaml:"kind"`
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Version     string   `json:"version" yaml:"version"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	License     string   `json:"license,omitempty" yaml:"license,omitempty"`
	URLs        []string `json:"urls,omitempty" yaml:"urls,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Icon        string   `json:"icon,omitempty" yaml:"icon,omitempty"`
	Channel     string   `json:"channel,omitempty" yaml:"channel,omitempty"`
	// Policy is the signature policy of the entry. Model files are checked by
	// the sha256 of each file, not by a signature, so a model entry leaves it
	// at its zero value.
	Policy        VerificationPolicy `json:"policy" yaml:"policy"`
	SchemaVersion int                `json:"schemaVersion" yaml:"schemaVersion"`
}

// ValidKindName reports whether s is a well-formed kind name, the grammar a
// reader needs to skip a kind it does not know.
func ValidKindName(s string) bool { return kindPattern.MatchString(s) }
