// SPDX-License-Identifier: MIT
package contract

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The user settings declaration", func() {
	good := UserSetting{Key: "save-mode", Values: []string{"ask", "auto", "never"}, Default: "ask"}
	with := func(list ...UserSetting) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities.UserSettings = list
		return m
	}

	It("is valid with a key, its values and a default among them", func() {
		Expect(ValidateManifest(with(good), compatible())).To(Succeed())
		Expect(ValidateManifest(with(), compatible())).To(Succeed(), "none is fine")
	})

	It("refuses a bad key, bad values and a default that is not allowed", func() {
		for name, bad := range map[string]UserSetting{
			"upper key":       {Key: "Save", Values: []string{"a", "b"}, Default: "a"},
			"key with space":  {Key: "save mode", Values: []string{"a", "b"}, Default: "a"},
			"empty key":       {Key: "", Values: []string{"a", "b"}, Default: "a"},
			"long key":        {Key: strings.Repeat("a", 41), Values: []string{"a", "b"}, Default: "a"},
			"one value":       {Key: "k", Values: []string{"a"}, Default: "a"},
			"no values":       {Key: "k", Default: "a"},
			"too many values": {Key: "k", Values: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, Default: "a"},
			"bad value":       {Key: "k", Values: []string{"a", "B C"}, Default: "a"},
			"repeated value":  {Key: "k", Values: []string{"a", "a"}, Default: "a"},
			"default missing": {Key: "k", Values: []string{"a", "b"}, Default: "c"},
			"no default":      {Key: "k", Values: []string{"a", "b"}},
		} {
			Expect(ValidateManifest(with(bad), compatible())).NotTo(Succeed(), name)
		}
	})

	It("refuses a repeated key and too many settings", func() {
		Expect(ValidateManifest(with(good, good), compatible())).NotTo(Succeed())
		var many []UserSetting
		for i := range MaxUserSettings + 1 {
			many = append(many, UserSetting{Key: "k" + strings.Repeat("a", i), Values: []string{"a", "b"}, Default: "a"})
		}
		Expect(ValidateManifest(with(many...), compatible())).NotTo(Succeed())
	})

	It("fixes the values and the default of a role the host reads", func() {
		role := func(values []string, def string) UserSetting {
			return UserSetting{Key: "save-mode", Values: values, Default: def, Role: UserSettingRoleMemorySave}
		}
		Expect(ValidateManifest(with(role([]string{"ask", "auto", "never"}, "ask")), compatible())).To(Succeed())
		Expect(ValidateManifest(with(role([]string{"never", "ask", "auto"}, "ask")), compatible())).To(Succeed(), "the order is the add-on's")
		for name, bad := range map[string]UserSetting{
			"a default that saves at once": role([]string{"ask", "auto", "never"}, "auto"),
			"a default of never":           role([]string{"ask", "auto", "never"}, "never"),
			"other values":                 role([]string{"ask", "always"}, "ask"),
			"a missing value":              role([]string{"ask", "auto"}, "ask"),
			"an extra value":               role([]string{"ask", "auto", "never", "maybe"}, "ask"),
			"an unknown role":              {Key: "k", Values: []string{"a", "b"}, Default: "a", Role: "memory.other"},
		} {
			Expect(ValidateManifest(with(bad), compatible())).NotTo(Succeed(), name)
		}
		two := role([]string{"ask", "auto", "never"}, "ask")
		other := two
		other.Key = "other-mode"
		Expect(ValidateManifest(with(two, other), compatible())).NotTo(Succeed(), "one role, one setting")
	})

	It("answers whether a value is allowed", func() {
		Expect(good.Allows("auto")).To(BeTrue())
		Expect(good.Allows("always")).To(BeFalse())
		Expect(good.Allows("")).To(BeFalse())
		got, ok := UserSettingOf([]UserSetting{good}, "save-mode")
		Expect(ok).To(BeTrue())
		Expect(got.Default).To(Equal("ask"))
		_, ok = UserSettingOf([]UserSetting{good}, "other")
		Expect(ok).To(BeFalse())
	})
})
