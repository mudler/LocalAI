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

// AddonHostOpsResolution is a startup snapshot. Consumers must not mutate Grants.
// Source and PolicyIgnored are bounded diagnostics, never policy file contents.
type AddonHostOpsResolution struct {
	Grants        map[string][]string
	Source        string
	PolicyIgnored bool
}

// ResolveAddonHostOps applies presence-based precedence. Nil means absent;
// a pointer to an empty string is an explicit decision to grant nothing.
// policyPath is already selected by the caller (flag, including empty, over env).
func ResolveAddonHostOps(flag, environment, policyPath *string) (AddonHostOpsResolution, error) {
	out := AddonHostOpsResolution{Grants: map[string][]string{}, Source: "empty"}
	selected := flag
	if selected != nil {
		out.Source = "flag"
	} else if environment != nil {
		selected = environment
		out.Source = "environment"
	}
	var err error
	if selected != nil {
		out.PolicyIgnored = policyPath != nil && *policyPath != ""
		out.Grants, err = ParseAddonHostOps(*selected)
	} else if policyPath != nil && *policyPath != "" {
		out.Source = "policy"
		out.Grants, err = LoadAddonHostPolicy(*policyPath)
	}
	if err != nil {
		return out, fmt.Errorf("addon host operations (%s): %w", out.Source, err)
	}
	return out, nil
}

// LoadAddonHostPolicy reads only an explicitly selected administrator file.
// Ownership and mount permissions are the deployment administrator's responsibility.
// Symlinks to regular files are permitted; this is not a filesystem sandbox.
func LoadAddonHostPolicy(path string) (map[string][]string, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("policy path must be absolute")
	}
	// Stat before opening rejects directories and special files (including FIFOs)
	// without a potentially blocking read. Recheck the opened descriptor as well.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat policy: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("policy must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open policy: %w", err)
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened policy: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxAddonHostPolicyBytes {
		return nil, fmt.Errorf("policy must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAddonHostPolicyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}
	if len(data) > maxAddonHostPolicyBytes {
		return nil, fmt.Errorf("policy exceeds 64 KiB")
	}
	return parseAddonHostPolicy(data)
}

// Token decoding deliberately avoids encoding/json struct-field case folding
// and map decoding's last-key-wins behavior.
func parseAddonHostPolicy(data []byte) (map[string][]string, error) {
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
	if err := expect('{'); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	grants := map[string][]string{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return nil, fmt.Errorf("duplicate or invalid policy key")
		}
		seen[name] = true
		switch name {
		case "schemaVersion":
			version, err := d.Token()
			if err != nil {
				return nil, err
			}
			if version != json.Number("1") {
				return nil, fmt.Errorf("schemaVersion must be integer 1")
			}
		case "grants":
			if err := expect('{'); err != nil {
				return nil, err
			}
			for d.More() {
				token, err := d.Token()
				if err != nil {
					return nil, err
				}
				id, ok := token.(string)
				if !ok || !addonIDForHostOps.MatchString(id) {
					return nil, fmt.Errorf("invalid policy addon ID")
				}
				if _, exists := grants[id]; exists {
					return nil, fmt.Errorf("duplicate policy addon ID")
				}
				if len(grants) >= 128 {
					return nil, fmt.Errorf("policy exceeds 128 addons")
				}
				if err := expect('['); err != nil {
					return nil, err
				}
				prefixes := []string{}
				seenPrefixes := map[string]bool{}
				for d.More() {
					token, err := d.Token()
					if err != nil {
						return nil, err
					}
					prefix, ok := token.(string)
					if !ok || !hostOpPrefix.MatchString(prefix) || seenPrefixes[prefix] {
						return nil, fmt.Errorf("invalid or duplicate policy prefix")
					}
					if len(prefixes) >= 32 {
						return nil, fmt.Errorf("policy exceeds 32 prefixes per addon")
					}
					prefixes = append(prefixes, prefix)
					seenPrefixes[prefix] = true
				}
				if err := expect(']'); err != nil {
					return nil, err
				}
				grants[id] = prefixes
			}
			if err := expect('}'); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown policy key")
		}
	}
	if err := expect('}'); err != nil {
		return nil, err
	}
	if !seen["schemaVersion"] || !seen["grants"] {
		return nil, fmt.Errorf("policy requires schemaVersion and grants")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing policy data")
	}
	return grants, nil
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
