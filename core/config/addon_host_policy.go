// SPDX-License-Identifier: MIT
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxAddonHostPolicyBytes = 64 * 1024

// AddonHostOpsResolution is a startup snapshot. Consumers must not mutate
// Grants or Deny. Source and GrantsIgnored are bounded diagnostics, never
// policy file contents.
type AddonHostOpsResolution struct {
	Grants map[string][]string
	// Deny is the union of the deny rules of the selected policy file, the
	// --addon-host-deny flag and its environment variable. Keys are add-on
	// IDs or "*". Values are prefixes, exact operation names or the single
	// token "*".
	Deny   map[string][]string
	Source string
	// GrantsIgnored is true when a selected policy file had its grants
	// replaced by a flat --addon-host-ops list. Its deny rules still apply.
	GrantsIgnored bool
}

// ResolveAddonHostOps applies presence-based precedence to grants. Nil means
// absent; a pointer to an empty string is an explicit decision to grant
// nothing. policyPath is already selected by the caller (flag, including
// empty, over env).
//
// A selected policy file is always opened, because its deny rules must
// survive a flat grants list: a deny that disappears when someone adds a flag
// is a security failure. Deny rules from the file, denyFlag and denyEnv are
// unioned.
func ResolveAddonHostOps(flag, environment, policyPath, denyFlag, denyEnv *string) (AddonHostOpsResolution, error) {
	out := AddonHostOpsResolution{Grants: map[string][]string{}, Deny: map[string][]string{}, Source: "empty"}
	selected := flag
	if selected != nil {
		out.Source = "flag"
	} else if environment != nil {
		selected = environment
		out.Source = "environment"
	}
	var err error
	var file AddonHostPolicy
	filePresent := policyPath != nil && *policyPath != ""
	if filePresent {
		file, err = LoadAddonHostPolicyFile(*policyPath)
		if err != nil {
			if out.Source == "empty" {
				out.Source = "policy"
			}
			return out, fmt.Errorf("addon host operations (%s): %w", out.Source, err)
		}
	}
	if selected != nil {
		out.GrantsIgnored = filePresent
		out.Grants, err = ParseAddonHostOps(*selected)
	} else if filePresent {
		out.Source = "policy"
		out.Grants = file.Grants
	}
	if err != nil {
		return out, fmt.Errorf("addon host operations (%s): %w", out.Source, err)
	}
	out.Deny = MergeAddonHostDeny(file.Deny)
	for _, raw := range []*string{denyFlag, denyEnv} {
		if raw == nil {
			continue
		}
		parsed, perr := ParseAddonHostDeny(*raw)
		if perr != nil {
			return out, fmt.Errorf("addon host deny: %w", perr)
		}
		out.Deny = MergeAddonHostDeny(out.Deny, parsed)
	}
	return out, nil
}

// AddonHostPolicy is the decoded content of a policy file.
type AddonHostPolicy struct {
	SchemaVersion int
	Grants        map[string][]string
	Deny          map[string][]string
}

// LoadAddonHostPolicy reads a policy file and returns only its grants. It
// remains for callers that predate the deny list.
func LoadAddonHostPolicy(path string) (map[string][]string, error) {
	p, err := LoadAddonHostPolicyFile(path)
	return p.Grants, err
}

// LoadAddonHostPolicyFile reads only an explicitly selected administrator file.
// Ownership and mount permissions are the deployment administrator's responsibility.
// Symlinks to regular files are permitted; this is not a filesystem sandbox.
func LoadAddonHostPolicyFile(path string) (AddonHostPolicy, error) {
	if !filepath.IsAbs(path) {
		return AddonHostPolicy{}, fmt.Errorf("policy path must be absolute")
	}
	// Stat before opening rejects directories and special files (including FIFOs)
	// without a potentially blocking read. Recheck the opened descriptor as well.
	info, err := os.Stat(path)
	if err != nil {
		return AddonHostPolicy{}, fmt.Errorf("stat policy: %w", err)
	}
	if !info.Mode().IsRegular() {
		return AddonHostPolicy{}, fmt.Errorf("policy must be a regular file")
	}
	// #nosec G304 -- Only the administrator selects this startup policy path via CLI/config; regular-file and 64 KiB checks bound its read.
	f, err := os.Open(path)
	if err != nil {
		return AddonHostPolicy{}, fmt.Errorf("open policy: %w", err)
	}
	// Read-only cleanup: close errors cannot change the decoded result.
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return AddonHostPolicy{}, fmt.Errorf("stat opened policy: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxAddonHostPolicyBytes {
		return AddonHostPolicy{}, fmt.Errorf("policy must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAddonHostPolicyBytes+1))
	if err != nil {
		return AddonHostPolicy{}, fmt.Errorf("read policy: %w", err)
	}
	if len(data) > maxAddonHostPolicyBytes {
		return AddonHostPolicy{}, fmt.Errorf("policy exceeds 64 KiB")
	}
	return parseAddonHostPolicy(data)
}

// Policy file schema versions this binary reads.
const (
	addonHostPolicyV1 = 1
	addonHostPolicyV2 = 2
)

// Token decoding deliberately avoids encoding/json struct-field case folding
// and map decoding's last-key-wins behavior.
func parseAddonHostPolicy(data []byte) (AddonHostPolicy, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	expect := func(want json.Delim) error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if t != want {
			return fmt.Errorf("expected %q", want)
		}
		return nil
	}
	fail := func(err error) (AddonHostPolicy, error) { return AddonHostPolicy{}, err }
	if err := expect('{'); err != nil {
		return fail(err)
	}
	seen := map[string]bool{}
	out := AddonHostPolicy{Grants: map[string][]string{}, Deny: map[string][]string{}}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return fail(err)
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return fail(fmt.Errorf("duplicate or invalid policy key"))
		}
		seen[name] = true
		switch name {
		case "schemaVersion":
			version, err := d.Token()
			if err != nil {
				return fail(err)
			}
			n, isNumber := version.(json.Number)
			v, convErr := n.Int64()
			switch {
			case !isNumber || convErr != nil || v < addonHostPolicyV1:
				return fail(fmt.Errorf("schemaVersion must be an integer (supported versions: 1, 2)"))
			case v > addonHostPolicyV2:
				return fail(fmt.Errorf("policy requires a newer LocalAI (supported versions: 1, 2)"))
			}
			out.SchemaVersion = int(v)
		case "grants":
			m, err := readHostOpMap(d, expect, hostOpPrefix.MatchString, false)
			if err != nil {
				return fail(err)
			}
			out.Grants = m
		case "deny":
			m, err := readHostOpMap(d, expect, validDenyEntry, true)
			if err != nil {
				return fail(err)
			}
			out.Deny = m
		default:
			return fail(fmt.Errorf("unknown policy key"))
		}
	}
	if err := expect('}'); err != nil {
		return fail(err)
	}
	if !seen["schemaVersion"] {
		return fail(fmt.Errorf("policy requires schemaVersion"))
	}
	if out.SchemaVersion == addonHostPolicyV1 {
		if seen["deny"] {
			return fail(fmt.Errorf("deny requires schemaVersion 2"))
		}
		if !seen["grants"] {
			return fail(fmt.Errorf("policy requires schemaVersion and grants"))
		}
	}
	if _, err := d.Token(); err != io.EOF {
		return fail(fmt.Errorf("unexpected trailing policy data"))
	}
	return out, nil
}

// readHostOpMap reads {"id": ["entry", ...]}. For a deny map the key "*" is
// valid and the entry "*" must stand alone.
func readHostOpMap(d *json.Decoder, expect func(json.Delim) error, validEntry func(string) bool, deny bool) (map[string][]string, error) {
	if err := expect('{'); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		id, ok := token.(string)
		if !ok || !(addonIDForHostOps.MatchString(id) || (deny && id == "*")) {
			return nil, fmt.Errorf("invalid policy addon ID")
		}
		if _, exists := out[id]; exists {
			return nil, fmt.Errorf("duplicate policy addon ID")
		}
		if len(out) >= 128 {
			return nil, fmt.Errorf("policy exceeds 128 addons")
		}
		if err := expect('['); err != nil {
			return nil, err
		}
		entries := []string{}
		seenEntries := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return nil, err
			}
			entry, ok := token.(string)
			if !ok || !validEntry(entry) || seenEntries[entry] {
				return nil, fmt.Errorf("invalid or duplicate policy prefix")
			}
			if len(entries) >= 32 {
				return nil, fmt.Errorf("policy exceeds 32 prefixes per addon")
			}
			entries = append(entries, entry)
			seenEntries[entry] = true
		}
		if err := expect(']'); err != nil {
			return nil, err
		}
		if seenEntries["*"] && len(entries) != 1 {
			return nil, fmt.Errorf(`policy deny "*" must be the only entry of its list`)
		}
		out[id] = entries
	}
	if err := expect('}'); err != nil {
		return nil, err
	}
	return out, nil
}

// CloneAddonHostOps isolates startup grants from caller-owned maps and slices.
func CloneAddonHostOps(ops map[string][]string) map[string][]string {
	if ops == nil {
		return nil
	}
	out := make(map[string][]string, len(ops))
	for id, prefixes := range ops {
		out[id] = append([]string(nil), prefixes...)
	}
	return out
}
