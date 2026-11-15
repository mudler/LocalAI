package projects

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	maxNameRunes   = 120
	maxListEntries = 64
	maxSlugLen     = 48
	fallbackSlug   = "project"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// knownKeys are the top-level keys of Manifest, lower case. encoding/json
// matches a key to a field without regard to case, so a differently cased copy
// of one of these is the field, not an unknown key.
var knownKeys = map[string]bool{"version": true, "id": true, "name": true, "description": true, "defaults": true, "sharedwith": true, "archived": true}

// ParseManifest reads a manifest and checks the rules of version 1. Unknown
// top-level fields do not fail the parse so that older builds can read newer
// manifests that only add fields; they are kept in Extra and written back by
// Marshal. A newer version is refused.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for k, v := range all {
		if knownKeys[strings.ToLower(k)] {
			continue
		}
		if m.Extra == nil {
			m.Extra = map[string]json.RawMessage{}
		}
		m.Extra[k] = append(json.RawMessage(nil), v...)
	}
	if m.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf("%w: unsupported manifest version %d", ErrInvalid, m.Version)
	}
	if strings.TrimSpace(m.Name) == "" {
		return Manifest{}, fmt.Errorf("%w: name is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(m.Name) > maxNameRunes {
		return Manifest{}, fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, maxNameRunes)
	}
	if m.ID != "" && !IDSafe(m.ID) {
		return Manifest{}, fmt.Errorf("%w: id is not a safe identifier", ErrInvalid)
	}
	// sharedWith is reserved. Sharing is a workspace grant, not a manifest
	// field, so a list is read and dropped: it grants nothing and a rewrite
	// never writes it back.
	m.SharedWith = nil
	lists := []struct {
		field   string
		entries []string
	}{
		{"skills", m.Defaults.Skills},
		{"addons", m.Defaults.Addons},
		{"connectors", m.Defaults.Connectors},
		{"mcpServers", m.Defaults.MCPServers},
		{"collections", m.Defaults.Collections},
	}
	for _, l := range lists {
		if len(l.entries) > maxListEntries {
			return Manifest{}, fmt.Errorf("%w: defaults.%s has more than %d entries", ErrInvalid, l.field, maxListEntries)
		}
	}
	return m, nil
}

// Marshal returns the manifest as indented JSON with a trailing newline. The
// keys in Extra follow the known keys, sorted, each value exactly as it was
// read. Encoding a Manifest cannot fail, so there is no error to return.
func (m Manifest) Marshal() []byte {
	b, _ := json.MarshalIndent(m, "", "  ")
	keys := make([]string, 0, len(m.Extra))
	for k := range m.Extra {
		if !knownKeys[strings.ToLower(k)] {
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		sort.Strings(keys)
		// The known keys always include version, so the object is not empty
		// and each extra key follows a comma.
		b = bytes.TrimSuffix(b, []byte("\n}"))
		for _, k := range keys {
			kb, _ := json.Marshal(k)
			b = append(b, ",\n  "...)
			b = append(b, kb...)
			b = append(b, ": "...)
			b = append(b, m.Extra[k]...)
		}
		b = append(b, "\n}"...)
	}
	return append(b, '\n')
}

// NewID returns a random project ID that satisfies IDSafe.
func NewID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand does not fail on supported platforms; a failure here
		// would leave the process unable to create secure values at all.
		panic(err)
	}
	return hex.EncodeToString(buf[:])
}

// IDSafe reports whether s can be used as a project ID in a path or a URL.
func IDSafe(s string) bool {
	return idPattern.MatchString(s)
}

// Slugify turns a project name into a folder name: lowercase kebab-case of at
// most 48 characters. Latin accents are folded ("Zürich" is "zurich") and
// letters with no decomposition are mapped by hand ("Straße" is "strasse"). A
// name with no Latin letter or digit left gets "project-" and a short stable
// id. If taken reports a slug as used, a counter is appended (-2, -3, ...)
// until a free one is found. A nil taken treats no slug as used. Candidates
// are passed to taken lowercase, and taken must compare case-insensitively
// against stored names.
func Slugify(name string, taken func(string) bool) string {
	base := kebab(ascii(name))
	if base == "" {
		base = fallbackSlug
		if hasNonASCIIText(name) {
			base = shortSlug(name)
		}
	}
	base = truncate(base, maxSlugLen)
	if taken == nil || !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("-%d", n)
		candidate := truncate(base, maxSlugLen-len(suffix)) + suffix
		if !taken(candidate) {
			return candidate
		}
	}
}

// letterFold maps Latin letters that have no Unicode decomposition to ASCII.
var letterFold = strings.NewReplacer(
	"ø", "o", "Ø", "O", "ß", "ss", "ẞ", "SS", "đ", "d", "Đ", "D",
	"æ", "ae", "Æ", "AE", "œ", "oe", "Œ", "OE", "ł", "l", "Ł", "L",
	"þ", "th", "Þ", "TH", "ð", "d", "Ð", "D",
)

// ascii folds Latin diacritics away ("Zürich" becomes "Zurich"). Letters of
// other scripts are left as they are.
func ascii(name string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, letterFold.Replace(name))
	if err != nil {
		return name
	}
	return out
}

// hasNonASCIIText reports whether name holds a non-ASCII rune that is not
// white space or a control character.
func hasNonASCIIText(name string) bool {
	for _, r := range name {
		if r > unicode.MaxASCII && !unicode.IsSpace(r) && !unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// shortSlug returns "project-" and four hex characters of the SHA-256 of the
// NFC-normalised, lower-cased name, so the same name always gets the same
// folder name.
func shortSlug(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(norm.NFC.String(name))))
	return fallbackSlug + "-" + hex.EncodeToString(sum[:2])
}

// kebab lowercases s and joins runs of ASCII letters and digits with dashes.
func kebab(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// truncate cuts s to at most n bytes and drops a dash left at the new end.
// The slug is ASCII, so cutting by byte is safe.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimRight(s[:n], "-")
}
