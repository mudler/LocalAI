// SPDX-License-Identifier: MIT
package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/go-containerregistry/pkg/name"
)

// Bounds on the backend fields. A library is read from a remote file, so each
// list and text is limited before the host keeps it.
const (
	maxBackendMirrors = 8
	maxBackendURLs    = 32
	maxBackendTags    = 64
	maxBackendCaps    = 32
	maxTextBytes      = 2048
	maxLicenseBytes   = 128
	maxTagBytes       = 64
)

// capabilityPattern is the grammar of a capability name, as the host reports
// it (default, nvidia, nvidia-l4t-cuda-13, metal-darwin-arm64, ...).
var capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// hasBackendOnlyFields reports whether an entry sets a field that only a
// backend may set. An add-on entry that does is refused.
func (r Release) hasBackendOnlyFields() bool {
	return r.License != "" || len(r.URLs) > 0 || len(r.Tags) > 0 || r.Icon != "" ||
		len(r.Mirrors) > 0 || len(r.Capabilities) > 0 || r.SchemaVersion != 0
}

// validFetchedRelease is the check a library applies to every entry it keeps.
// A meta backend (capabilities and no artifact) has no image, no digest and no
// platform; every other entry has an artifact on the host's platform, and a
// digest unless it is mutable.
func validFetchedRelease(r Release, allowLoopback bool) bool {
	if !validEntryID(r) || !versionPattern.MatchString(r.Version) || r.Name == "" {
		return false
	}
	if r.IsMeta() {
		if r.Digest != "" || r.OS != "" || r.Arch != "" || len(r.Mirrors) > 0 || r.Mutable {
			return false
		}
	} else {
		u, err := url.Parse(r.Artifact)
		if err != nil || !validArtifactScheme(u, allowLoopback) || u.Host == "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		if !idPattern.MatchString(r.OS) || !idPattern.MatchString(r.Arch) {
			return false
		}
		// A mutable entry names a moving tag and has no digest; any other entry
		// is pinned by one.
		if r.Mutable != (r.Digest == "") || (r.Digest != "" && !digestPattern.MatchString(r.Digest)) {
			return false
		}
		// A mutable entry is an OCI tag: an HTTP archive has nothing to resolve.
		if r.Mutable && r.EntryKind() != KindBackend && r.EntryKind() != KindAddon {
			return false
		}
		if r.Mutable && !strings.HasPrefix(r.Artifact, "oci://") {
			return false
		}
	}
	if r.Channel != "" && (!channelPattern.MatchString(r.Channel) || versionPattern.MatchString(r.Channel)) {
		return false
	}
	return true
}

// validBackendFields checks the backend-only fields of a backend entry.
func validBackendFields(r Release) bool {
	if r.SchemaVersion < 0 || r.SchemaVersion > SchemaVersion {
		return false
	}
	if len(r.License) > maxLicenseBytes || !plainText(r.License) {
		return false
	}
	if len(r.URLs) > maxBackendURLs || len(r.Tags) > maxBackendTags {
		return false
	}
	for _, u := range r.URLs {
		if !validWebURL(u) {
			return false
		}
	}
	for _, t := range r.Tags {
		if t == "" || len(t) > maxTagBytes || !plainText(t) {
			return false
		}
	}
	// The icon is a URL the browser loads. The host never fetches it, so only
	// its form is checked: https, a host, no credentials.
	if r.Icon != "" {
		if u, err := url.Parse(r.Icon); !validWebURL(r.Icon) || err != nil || u.Scheme != "https" {
			return false
		}
	}
	if len(r.Mirrors) > maxBackendMirrors {
		return false
	}
	seen := map[string]bool{r.Artifact: true}
	for _, m := range r.Mirrors {
		u, err := url.Parse(m)
		if err != nil || u.Scheme != "oci" || u.Host == "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || seen[m] {
			return false
		}
		// The install parses the mirror as an image reference, so a mirror
		// that does not parse is refused here, not at install.
		if _, err := name.ParseReference(strings.TrimPrefix(m, "oci://")); err != nil {
			return false
		}
		seen[m] = true
	}
	if len(r.Capabilities) > maxBackendCaps {
		return false
	}
	for k, v := range r.Capabilities {
		if !capabilityPattern.MatchString(k) || !backendNamePattern.MatchString(v) || strings.Contains(v, "..") {
			return false
		}
	}
	// An entry without an artifact is a meta backend and needs capabilities;
	// without an artifact and without capabilities it names nothing.
	if r.Artifact == "" && len(r.Capabilities) == 0 {
		return false
	}
	return true
}

func validWebURL(s string) bool {
	if s == "" || len(s) > maxTextBytes {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && plainText(s)
}

// plainText refuses control characters, so a value cannot carry a line break
// into a log line or a UI string.
func plainText(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ErrInvalidEntry is returned for an entry the host would refuse to keep.
var ErrInvalidEntry = errors.New("library entry is not valid for this host")

// ValidateEntry applies to one entry the checks a host applies to every entry
// of a library it fetches, so a tool that writes a library can know the host
// will keep what it writes. The loopback allowance is a per-source setting and
// is off here.
func ValidateEntry(r Release) error {
	if !validFetchedRelease(r, false) || !validKindFields(r) || !validEntryUI(r.UI) || !validRequirements(r.Requires) || !validEntryTools(r) {
		return fmt.Errorf("%w: %s %q", ErrInvalidEntry, r.EntryKind(), r.ID)
	}
	return nil
}
