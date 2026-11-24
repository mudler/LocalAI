// SPDX-License-Identifier: MIT

// Package sandboxpolicy is the administrator's policy for sandbox profiles:
// which profile a new chat gets, who may use each profile, the memory and disk
// quota of one sandbox, and the profiles pinned to their previous image. It is
// a value type with no authority of its own. The owner of the host settings
// stores it (hostsettings), and the chat service reads it. The default is
// open: with no policy, everyone may use every profile.
package sandboxpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The roles an administrator can name.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Limits of the policy.
const (
	MaxProfiles    = 32
	MaxUsers       = 256
	MaxUserChars   = 256
	MinMemoryMiB   = 256
	MinDiskMiB     = 64
	maxPolicyBytes = 64 << 10
)

// ErrInvalid marks a policy the host refuses.
var ErrInvalid = errors.New("invalid sandbox profile policy")

// Access says who may use one profile. A profile with no roles and no users is
// open to everyone. Otherwise a user may use it when the user has one of the
// roles, or is one of the users.
type Access struct {
	Roles []string `json:"roles,omitempty"`
	Users []string `json:"users,omitempty"`
}

// Open says the access restricts nobody.
func (a Access) Open() bool { return len(a.Roles) == 0 && len(a.Users) == 0 }

// Policy is the policy of the profiles.
type Policy struct {
	// Default is the profile a new chat gets. Empty: InitialProfile.
	Default string `json:"default,omitempty"`
	// Access restricts who may use a profile. A profile that is not listed is
	// open.
	Access map[string]Access `json:"access,omitempty"`
	// MemoryMiB and DiskMiB cap the sandbox of any profile. Zero: no cap.
	MemoryMiB int `json:"memory_mib,omitempty"`
	DiskMiB   int `json:"disk_mib,omitempty"`
	// PinPrevious lists the profiles that run their previous image, after a
	// release whose new image does not work.
	PinPrevious []string `json:"pin_previous,omitempty"`
}

// Normalized returns p with empty values and nil slices and maps made
// canonical, so two equal policies compare equal.
func (p Policy) Normalized() Policy {
	if p.Access == nil {
		p.Access = map[string]Access{}
	}
	for id, a := range p.Access {
		if a.Open() {
			delete(p.Access, id)
		}
	}
	if p.PinPrevious == nil {
		p.PinPrevious = []string{}
	}
	return p
}

// decode reads a policy and drops the empty lists and maps, so a policy that
// was written with every key reads back equal to one that was never set.
func decode(raw []byte) (Policy, error) {
	type plain Policy
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var v plain
	if err := d.Decode(&v); err != nil {
		return Policy{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if d.More() {
		return Policy{}, fmt.Errorf("%w: trailing data", ErrInvalid)
	}
	return Policy(v), nil
}

// canonical drops the empty lists and maps and the open entries of Access.
func (p Policy) canonical() Policy {
	access := map[string]Access{}
	for id, a := range p.Access {
		if !a.Open() {
			access[id] = a
		}
	}
	p.Access = access
	if len(access) == 0 {
		p.Access = nil
	}
	if len(p.PinPrevious) == 0 {
		p.PinPrevious = nil
	}
	return p
}

// UnmarshalJSON reads a policy as Parse does, but does not validate it: the
// owner store validates the whole policy when it opens.
func (p *Policy) UnmarshalJSON(raw []byte) error {
	v, err := decode(raw)
	*p = v.canonical()
	return err
}

// Parse reads a policy from JSON. Unknown fields, repeated keys and trailing
// data are refused.
func Parse(raw []byte) (Policy, error) {
	if len(raw) > maxPolicyBytes {
		return Policy{}, fmt.Errorf("%w: too large", ErrInvalid)
	}
	p, err := decode(raw)
	if err != nil {
		return Policy{}, err
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p.canonical(), nil
}

// Validate checks the shape of the policy. It does not know the profiles of the
// add-on: a policy may name a profile that a later release adds.
func (p Policy) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	if p.Default != "" && !contract.ValidProfileID(p.Default) {
		return bad("default is not a profile ID")
	}
	if len(p.Access) > MaxProfiles || len(p.PinPrevious) > MaxProfiles {
		return bad("too many profiles")
	}
	for id, a := range p.Access {
		if !contract.ValidProfileID(id) {
			return bad("access names %q, which is not a profile ID", id)
		}
		if len(a.Users) > MaxUsers {
			return bad("too many users for %s", id)
		}
		for _, r := range a.Roles {
			if r != RoleAdmin && r != RoleUser {
				return bad("role %q is not admin or user", r)
			}
		}
		for _, u := range a.Users {
			if u == "" || len(u) > MaxUserChars {
				return bad("a user of %s is empty or too long", id)
			}
		}
	}
	if p.MemoryMiB != 0 && (p.MemoryMiB < MinMemoryMiB || p.MemoryMiB > contract.MaxProfileMemoryMiB) {
		return bad("memory_mib must be 0 or %d to %d", MinMemoryMiB, contract.MaxProfileMemoryMiB)
	}
	if p.DiskMiB != 0 && (p.DiskMiB < MinDiskMiB || p.DiskMiB > contract.MaxProfileDiskMiB) {
		return bad("disk_mib must be 0 or %d to %d", MinDiskMiB, contract.MaxProfileDiskMiB)
	}
	seen := map[string]bool{}
	for _, id := range p.PinPrevious {
		if !contract.ValidProfileID(id) || seen[id] {
			return bad("pin_previous has %q, which is repeated or not a profile ID", id)
		}
		seen[id] = true
	}
	return nil
}

// Allows says whether a user with a role may use a profile. The fallback
// profile is open to everyone whatever the policy says: a chat must always
// have somewhere to run.
func (p Policy) Allows(profile, user, role string) bool {
	if profile == contract.FallbackProfile {
		return true
	}
	a, ok := p.Access[profile]
	if !ok || a.Open() {
		return true
	}
	return (role != "" && slices.Contains(a.Roles, role)) || (user != "" && slices.Contains(a.Users, user))
}

// InitialProfile is the profile of a new chat when the administrator has set
// no default. A provider that does not declare it, or whose image is missing,
// runs the fallback profile instead; the fallback stays contract.FallbackProfile.
const InitialProfile = "media"

// DefaultProfile is the profile of a new chat.
func (p Policy) DefaultProfile() string {
	if p.Default == "" {
		return InitialProfile
	}
	return p.Default
}

// Pinned says whether the profile runs its previous image.
func (p Policy) Pinned(profile string) bool { return slices.Contains(p.PinPrevious, profile) }
