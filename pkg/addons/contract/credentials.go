// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Kinds of a credential slot. Only the token exists in this version; a kind
// the host does not know is refused at install.
const CredentialKindToken = "token"

// Bounds of the credentials of a manifest and of a request.
const (
	MaxCredentialSlots = 1
	// MaxResourceKeyBytes is the longest external resource name in a request.
	MaxResourceKeyBytes = 800
	// MaxCredentialBytes is the longest credential in a request.
	MaxCredentialBytes = 4096
	// MaxResourceLabelChars is the longest CredentialSlot.ResourceLabel.
	MaxResourceLabelChars = 24
)

// CredentialSlot declares that a call of the add-on's tools needs a secret
// that the person gave the host, such as an access token for a service. The
// host seals it, keeps it with the person's binding of the add-on and puts it
// in Request.Credential for each call. The model can neither see nor set it.
//
// The slot can also name the argument that carries the external resource a
// call works on, for example a repository. The host then checks the argument
// against the list the person gave the binding, before the add-on is called,
// and puts the host ID of the entry in Request.ResourceID and the key in
// Request.ResourceKey. The check is the host's, so an add-on cannot widen it.
type CredentialSlot struct {
	// ID names the slot. It is unique in the manifest.
	ID string `json:"id"`
	// Title is the name the person sees, such as "Access token".
	Title string `json:"title"`
	// Kind is CredentialKindToken.
	Kind string `json:"kind"`
	// ResourceArgument is the name of the string argument that holds the
	// external resource key. A tool whose schema declares it must require it,
	// and a call is refused when the key is not on the list. A tool without it
	// runs with the credential and no resource.
	ResourceArgument string `json:"resourceArgument,omitempty"`
	// ResourceLabel is the word for the resource in the lines that say which
	// connection acted, such as "repository" in "as Work, repository
	// acme/widgets". Without it the host says "resource".
	ResourceLabel string `json:"resourceLabel,omitempty"`
}

// validateCredentials checks the credentials of the manifest. The binding that
// holds a credential belongs to a connector kind, so the add-on declares one.
// A tool of such an add-on is a read tool or a write tool. A write tool names
// its resource with the slot argument, which must be required; the write mode
// of the person's binding decides whether it runs at all, whether it waits for
// a confirmation, and the host holds it to the list of the binding like a
// read. Schema, create and action tools cannot carry a credential.
func validateCredentials(c Capabilities) error {
	if len(c.Credentials) == 0 {
		return nil
	}
	if len(c.Credentials) > MaxCredentialSlots {
		return fmt.Errorf("at most %d credential slots", MaxCredentialSlots)
	}
	if c.Connector == nil || len(c.Connector.Kinds) == 0 {
		return fmt.Errorf("credentials need a connector kind to hold them")
	}
	seen := map[string]bool{}
	for _, s := range c.Credentials {
		if !namespace.MatchString(s.ID) || seen[s.ID] || s.Kind != CredentialKindToken || !plainText(s.Title, 80) {
			return fmt.Errorf("invalid credential slot")
		}
		seen[s.ID] = true
		if s.ResourceArgument != "" && !toolPropertyName.MatchString(s.ResourceArgument) {
			return fmt.Errorf("invalid credential resource argument")
		}
		if s.ResourceLabel != "" && !plainText(s.ResourceLabel, MaxResourceLabelChars) {
			return fmt.Errorf("invalid credential resource label")
		}
	}
	slot := c.Credentials[0]
	for _, d := range c.ToolDefinitions {
		if d.Kind != ToolKindRead && d.Kind != ToolKindWrite {
			return fmt.Errorf("tool %s: an add-on with credentials declares read and write tools only", d.Name)
		}
		if d.Enumerates {
			if d.Kind != ToolKindRead || d.UIOnly || !c.wildcardKind() {
				return fmt.Errorf("tool %s: enumerates needs a read tool and a connector kind with wildcardResources", d.Name)
			}
			if slot.ResourceArgument != "" {
				if _, has := toolProperties(d)[slot.ResourceArgument]; has {
					return fmt.Errorf("tool %s: an enumerating tool names no resource", d.Name)
				}
			}
		}
		if _, has := toolProperties(d)[ResourceArgument]; has {
			return fmt.Errorf("tool %s: an add-on with credentials names its resources with %s, not %s", d.Name, slot.ResourceArgument, ResourceArgument)
		}
		if slot.ResourceArgument == "" {
			if d.Kind == ToolKindWrite {
				return fmt.Errorf("tool %s: a write tool needs the credential slot to name a resource argument", d.Name)
			}
			continue
		}
		prop, has := toolProperties(d)[slot.ResourceArgument]
		if !has {
			if d.Kind == ToolKindWrite {
				return fmt.Errorf("tool %s: a write tool must declare %s", d.Name, slot.ResourceArgument)
			}
			continue
		}
		var p struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(prop, &p) != nil || p.Type != "string" || !requiredProperty(d, slot.ResourceArgument) {
			return fmt.Errorf("tool %s: %s must be a required string", d.Name, slot.ResourceArgument)
		}
	}
	return nil
}

// AnyResource is the resource key that stands for everything the credential
// reaches: the entry a binding lists to allow it, and the key the host asks
// for on behalf of an enumerating tool (see ToolDefinition.Enumerates).
const AnyResource = "*"

// wildcardKind reports a connector kind that lets bindings list a wildcard.
func (c Capabilities) wildcardKind() bool {
	if c.Connector == nil {
		return false
	}
	for _, k := range c.Connector.Kinds {
		if k.WildcardResources {
			return true
		}
	}
	return false
}

func toolProperties(d ToolDefinition) map[string]json.RawMessage {
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(d.InputSchema, &root)
	return root.Properties
}

func requiredProperty(d ToolDefinition, name string) bool {
	var root struct {
		Required []string `json:"required"`
	}
	_ = json.Unmarshal(d.InputSchema, &root)
	for _, r := range root.Required {
		if r == name {
			return true
		}
	}
	return false
}

// CredentialResource returns the external resource key a call of the tool
// carries, per the slot: the empty string for a tool that does not declare the
// slot's resource argument. found tells whether the tool declares it.
func (s CredentialSlot) CredentialResource(d ToolDefinition, arguments json.RawMessage) (key string, found bool, err error) {
	if s.ResourceArgument == "" {
		return "", false, nil
	}
	if _, has := toolProperties(d)[s.ResourceArgument]; !has {
		return "", false, nil
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(arguments, &args) != nil {
		return "", true, fmt.Errorf("invalid arguments")
	}
	if json.Unmarshal(args[s.ResourceArgument], &key) != nil || key == "" {
		return "", true, fmt.Errorf("the %s argument is required", s.ResourceArgument)
	}
	return key, true, nil
}

// validResourceKey reports an external resource key that fits a request: not
// empty, bounded, with no control character.
func validResourceKey(k string) bool {
	if k == "" || len(k) > MaxResourceKeyBytes || !utf8.ValidString(k) {
		return false
	}
	for _, r := range k {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ErrRateLimited is the add-on's answer that the service behind it asks the
// caller to wait. Nothing was changed. The host turns it into a tool result
// that tells the model not to repeat the call before RetryAfter seconds.
type ErrRateLimited struct {
	// RetryAfter is how many seconds to wait, at least 1.
	RetryAfter int
}

// MaxRetryAfter bounds the wait an add-on may ask for, in seconds.
const MaxRetryAfter = 3600

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("the service asks to wait %d s", e.RetryAfter)
}

// Seconds is the wait, kept between 1 and MaxRetryAfter.
func (e *ErrRateLimited) Seconds() int { return min(max(e.RetryAfter, 1), MaxRetryAfter) }
