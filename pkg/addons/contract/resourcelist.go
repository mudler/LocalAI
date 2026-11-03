// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
)

// MaxListedResources bounds the entries of the listing the host builds. The
// answer says when it is cut short.
const MaxListedResources = 100

// DefaultResourceListItems is the key of the result array when the add-on
// names none.
const DefaultResourceListItems = "resources"

// ResourceList declares that the host offers a read tool listing the
// resources of the add-on the caller may use. The add-on writes no code for
// it: the host builds the tool from its own record of the grants. The model
// then finds the IDs that every tool of a resource takes.
type ResourceList struct {
	// Tool is the name of the tool, in the form of any tool name. It must not
	// name a tool the add-on declares.
	Tool string `json:"tool"`
	// Items is the key of the result array. It defaults to
	// DefaultResourceListItems.
	Items string `json:"items,omitempty"`
}

// ItemsKey returns the key of the result array.
func (l ResourceList) ItemsKey() string {
	if l.Items == "" {
		return DefaultResourceListItems
	}
	return l.Items
}

func validateResourceList(c Capabilities) error {
	if c.Resources == nil || c.Resources.List == nil {
		return nil
	}
	l := c.Resources.List
	if !namespace.MatchString(l.Tool) || (l.Items != "" && !toolPropertyName.MatchString(l.Items)) {
		return fmt.Errorf("invalid resources list")
	}
	if slices.Contains(c.Tools, l.Tool) || slices.Contains(c.AdminTools, l.Tool) {
		return fmt.Errorf("resources list tool %q is also a declared tool", l.Tool)
	}
	for _, d := range c.ToolDefinitions {
		if d.Name == l.Tool {
			return fmt.Errorf("resources list tool %q is also a declared tool", l.Tool)
		}
	}
	return nil
}
