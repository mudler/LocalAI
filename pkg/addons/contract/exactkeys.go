// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// exactObjectFields checks keys before encoding/json can match struct fields
// case-insensitively. strictJSON retains duplicate, trailing and size/depth bounds.
// Only the immediate object is checked: localized maps keep their locale keys.
func exactObjectFields(b []byte, max int64, keys ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := strictJSON(b, &fields, max); err != nil {
		return nil, err
	}
	for key := range fields {
		if !slices.Contains(keys, key) {
			return nil, fmt.Errorf("noncanonical or unknown JSON key %q", key)
		}
	}
	return fields, nil
}

// rejectExtensionAliases narrows exact spelling to the new declaration entry
// keys, without changing historical spelling behavior for unrelated fields.
func rejectExtensionAliases(b []byte, keys ...string) error {
	var fields map[string]json.RawMessage
	if err := strictJSON(b, &fields, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	for key := range fields {
		for _, canonical := range keys {
			if key != canonical && strings.EqualFold(key, canonical) {
				return fmt.Errorf("noncanonical extension key %q", key)
			}
		}
	}
	return nil
}

func (c *Capabilities) UnmarshalJSON(b []byte) error {
	if err := rejectExtensionAliases(b, "executionProvider", "hostOperations", "network", "contextFiles"); err != nil {
		return err
	}
	type wire Capabilities
	var v wire
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	*c = Capabilities(v)
	return nil
}

func (u *UI) UnmarshalJSON(b []byte) error {
	if err := rejectExtensionAliases(b, "homeCommands", "modelActions"); err != nil {
		return err
	}
	type wire UI
	var v wire
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	*u = UI(v)
	return nil
}

func (p *ExecutionProvider) UnmarshalJSON(b []byte) error {
	if _, err := exactObjectFields(b, DefaultLimits().ManifestBytes, "tools", "statusTool", "jobs"); err != nil {
		return err
	}
	type wire ExecutionProvider
	var v wire
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	*p = ExecutionProvider(v)
	return nil
}

func (j *ExecutionJobs) UnmarshalJSON(b []byte) error {
	if _, err := exactObjectFields(b, DefaultLimits().ManifestBytes, "tool"); err != nil {
		return err
	}
	type wire ExecutionJobs
	var v wire
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	*j = ExecutionJobs(v)
	return nil
}

func (o *HostOperations) UnmarshalJSON(b []byte) error {
	if _, err := exactObjectFields(b, DefaultLimits().ManifestBytes, "required", "optional"); err != nil {
		return err
	}
	type wire HostOperations
	var v wire
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	*o = HostOperations(v)
	return nil
}
