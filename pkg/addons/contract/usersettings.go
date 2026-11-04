// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
	"slices"
)

// Limits of the per-user settings an add-on declares.
const (
	// MaxUserSettings is the most settings one add-on declares.
	MaxUserSettings = 8
	// MaxUserSettingValues is the most values one setting allows.
	MaxUserSettingValues = 8
)

var (
	userSettingKey   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	userSettingValue = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
)

// ValidUserSettingKey reports whether s can be the key of a user setting.
func ValidUserSettingKey(s string) bool { return userSettingKey.MatchString(s) }

// UserSetting declares one choice that each person makes for an add-on, such
// as how the add-on behaves for them. The add-on only names the key and the
// values it understands. The host keeps the person's answer (it is not a file
// of the add-on), checks every write against this declaration and reads it
// for its own rules. A person who has not chosen has the Default.
//
// A setting is for the person who set it: it never applies to another person,
// and a connector chat or an unattended run does not read it as a widening of
// their authority; a host rule that reads a setting says how it uses it.
type UserSetting struct {
	// Key names the setting inside the add-on: lowercase letters, digits and
	// dashes, at most 40.
	Key string `json:"key"`
	// Values are the allowed answers, in the order the add-on lists them. Each
	// one has the same form as the key.
	Values []string `json:"values"`
	// Default is the answer of a person who has not chosen. It is one of Values.
	Default string `json:"default"`
	// Role names what the host does with the answer, when the host reads it
	// itself. Empty: the setting is only for the add-on's own page. A known
	// role fixes the values and the default (see roleRules), so the manifest
	// cannot make the host more permissive than its own default.
	Role string `json:"role,omitempty"`
}

// The roles a user setting can play for the host. The host defines the values
// and the default of each role.
const (
	// UserSettingRoleMemorySave is how a memory note that a model proposes is
	// saved: ask (the person confirms), auto (saved at once, with an undo) or
	// never. The host reads it for the memory kind of propose_change.
	UserSettingRoleMemorySave = "memory.save-mode"
)

// roleRules are the values and the default that each role needs.
var roleRules = map[string]struct {
	values []string
	deflt  string
}{
	UserSettingRoleMemorySave: {values: []string{"ask", "auto", "never"}, deflt: "ask"},
}

// Allows reports whether v is one of the allowed values.
func (s UserSetting) Allows(v string) bool { return slices.Contains(s.Values, v) }

func (s UserSetting) validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("user setting %q: %s", s.Key, fmt.Sprintf(format, a...))
	}
	if !userSettingKey.MatchString(s.Key) {
		return bad("the key is lowercase letters, digits and dashes, at most 40, and starts with a letter")
	}
	if len(s.Values) < 2 || len(s.Values) > MaxUserSettingValues {
		return bad("it needs 2 to %d values", MaxUserSettingValues)
	}
	seen := map[string]bool{}
	for _, v := range s.Values {
		if !userSettingValue.MatchString(v) {
			return bad("the value %q is lowercase letters, digits and dashes, at most 40, and starts with a letter", v)
		}
		if seen[v] {
			return bad("the value %q is listed twice", v)
		}
		seen[v] = true
	}
	if !seen[s.Default] {
		return bad("the default must be one of the values")
	}
	if s.Role != "" {
		rule, ok := roleRules[s.Role]
		if !ok {
			return bad("the role %q is not one the host knows", s.Role)
		}
		if s.Default != rule.deflt || len(s.Values) != len(rule.values) {
			return bad("the role %q takes the values %v and the default %q", s.Role, rule.values, rule.deflt)
		}
		for _, v := range rule.values {
			if !seen[v] {
				return bad("the role %q takes the values %v and the default %q", s.Role, rule.values, rule.deflt)
			}
		}
	}
	return nil
}

// UserSettingOf returns the declaration of key in the list.
func UserSettingOf(list []UserSetting, key string) (UserSetting, bool) {
	for _, s := range list {
		if s.Key == key {
			return s, true
		}
	}
	return UserSetting{}, false
}

// validateUserSettings checks the declaration against the manifest.
func validateUserSettings(list []UserSetting) error {
	if len(list) == 0 {
		return nil
	}
	if len(list) > MaxUserSettings {
		return fmt.Errorf("too many user settings: at most %d", MaxUserSettings)
	}
	seen, roles := map[string]bool{}, map[string]bool{}
	for _, s := range list {
		if err := s.validate(); err != nil {
			return err
		}
		if seen[s.Key] {
			return fmt.Errorf("user setting %q is declared twice", s.Key)
		}
		seen[s.Key] = true
		if s.Role != "" {
			if roles[s.Role] {
				return fmt.Errorf("user setting %q: the role %q is taken by another setting of this add-on", s.Key, s.Role)
			}
			roles[s.Role] = true
		}
	}
	return nil
}
