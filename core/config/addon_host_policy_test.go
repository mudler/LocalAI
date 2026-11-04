// SPDX-License-Identifier: MIT
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Addon host policy", func() {
	write := func(body string) string {
		p := filepath.Join(GinkgoT().TempDir(), "policy.json")
		Expect(os.WriteFile(p, []byte(body), 0600)).To(Succeed())
		return p
	}
	It("loads exact grants and accepts an empty mapping", func() {
		got, err := LoadAddonHostPolicy(write(`{"schemaVersion":1,"grants":{"sandbox":["workspace.","egress."]}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{"sandbox": {"workspace.", "egress."}}))
		got, err = LoadAddonHostPolicy(write(`{"schemaVersion":1,"grants":{}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
	DescribeTable("rejects strict schema violations", func(body string) {
		_, err := LoadAddonHostPolicy(write(body))
		Expect(err).To(HaveOccurred())
	},
		Entry("missing version", `{"grants":{}}`),
		Entry("missing grants", `{"schemaVersion":1}`),
		Entry("null grants", `{"schemaVersion":1,"grants":null}`),
		Entry("null prefixes", `{"schemaVersion":1,"grants":{"a":null}}`),
		Entry("version 3", `{"schemaVersion":3,"grants":{}}`),
		Entry("version 0", `{"schemaVersion":0,"grants":{}}`),
		Entry("string version", `{"schemaVersion":"1","grants":{}}`),
		Entry("fraction version", `{"schemaVersion":1.0,"grants":{}}`),
		Entry("unknown key", `{"schemaVersion":1,"grants":{},"extra":true}`),
		Entry("case variant", `{"SchemaVersion":1,"grants":{}}`),
		Entry("duplicate key", `{"schemaVersion":1,"schemaVersion":1,"grants":{}}`),
		Entry("duplicate addon", `{"schemaVersion":1,"grants":{"a":[],"a":[]}}`),
		Entry("escaped duplicate", `{"schemaVersion":1,"grants":{"a":[],"\u0061":[]}}`),
		Entry("duplicate prefix", `{"schemaVersion":1,"grants":{"a":["x.","x."]}}`),
		Entry("invalid addon", `{"schemaVersion":1,"grants":{"A":["x."]}}`),
		Entry("invalid prefix", `{"schemaVersion":1,"grants":{"a":["X."]}}`),
		Entry("untrimmed prefix", `{"schemaVersion":1,"grants":{"a":[" x."]}}`),
		Entry("exact operation", `{"schemaVersion":1,"grants":{"a":["workspace.read"]}}`),
		Entry("non string", `{"schemaVersion":1,"grants":{"a":[1]}}`),
		Entry("trailing JSON", `{"schemaVersion":1,"grants":{}} {}`),
		Entry("malformed", `{"schemaVersion":1,`),
		Entry("array", `[]`),
	)
	It("enforces file type, absolute paths, existence and size", func() {
		for _, p := range []string{"relative.json", "https://example.com/policy.json", GinkgoT().TempDir(), filepath.Join(GinkgoT().TempDir(), "absent")} {
			_, err := LoadAddonHostPolicy(p)
			Expect(err).To(HaveOccurred(), p)
		}
		body := `{"schemaVersion":1,"grants":{}}`
		_, err := LoadAddonHostPolicy(write(body + strings.Repeat(" ", 65536-len(body))))
		Expect(err).NotTo(HaveOccurred())
		_, err = LoadAddonHostPolicy(write(body + strings.Repeat(" ", 65537-len(body))))
		Expect(err).To(HaveOccurred())
	})
	It("permits a symlink to an explicitly selected regular file", func() {
		path := write(`{"schemaVersion":1,"grants":{}}`)
		link := filepath.Join(GinkgoT().TempDir(), "selected.json")
		Expect(os.Symlink(path, link)).To(Succeed())
		got, err := LoadAddonHostPolicy(link)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
	It("rejects an unreadable file when the operating system denies access", func() {
		path := write(`{"schemaVersion":1,"grants":{}}`)
		Expect(os.Chmod(path, 0000)).To(Succeed())
		if os.Geteuid() == 0 {
			Skip("root can read mode-0000 files")
		}
		_, err := LoadAddonHostPolicy(path)
		Expect(err).To(HaveOccurred())
	})
	It("bounds addon and prefix counts", func() {
		entries := []string{}
		for i := 0; i < 129; i++ {
			entries = append(entries, fmt.Sprintf(`"a-%d":[]`, i))
		}
		_, err := LoadAddonHostPolicy(write(`{"schemaVersion":1,"grants":{` + strings.Join(entries[:128], ",") + `}}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = LoadAddonHostPolicy(write(`{"schemaVersion":1,"grants":{` + strings.Join(entries, ",") + `}}`))
		Expect(err).To(HaveOccurred())
		prefixes := []string{}
		for i := 0; i < 33; i++ {
			prefixes = append(prefixes, fmt.Sprintf(`"x.%s."`, string(rune('a'+i/26))+string(rune('a'+i%26))))
		}
		_, err = LoadAddonHostPolicy(write(`{"schemaVersion":1,"grants":{"a":[` + strings.Join(prefixes[:32], ",") + `]}}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = LoadAddonHostPolicy(write(`{"schemaVersion":1,"grants":{"a":[` + strings.Join(prefixes, ",") + `]}}`))
		Expect(err).To(HaveOccurred())
	})
	It("resolves presence without union, always opening the selected policy", func() {
		empty, env, flag := "", "env=x.", "flag=y."
		bad := write(`{"schemaVersion":2,"grants":{"unused":["q."]},"deny":{"*":["egress."]}}`)
		policy := write(`{"schemaVersion":1,"grants":{"file":["z."]}}`)
		for _, tc := range []struct {
			flag, env, policy *string
			want              map[string][]string
			source            string
			ignored           bool
		}{
			{nil, nil, nil, map[string][]string{}, "empty", false},
			{nil, nil, &policy, map[string][]string{"file": {"z."}}, "policy", false},
			{nil, &env, &bad, map[string][]string{"env": {"x."}}, "environment", true},
			{&flag, &env, &bad, map[string][]string{"flag": {"y."}}, "flag", true},
			{&empty, &env, &bad, map[string][]string{}, "flag", true},
			{nil, &empty, &bad, map[string][]string{}, "environment", true},
			{nil, nil, &empty, map[string][]string{}, "empty", false},
		} {
			got, err := ResolveAddonHostOps(tc.flag, tc.env, tc.policy, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Grants).To(Equal(tc.want))
			Expect(got.Source).To(Equal(tc.source))
			Expect(got.GrantsIgnored).To(Equal(tc.ignored))
		}
		missing := "/not-present-policy.json"
		_, err := ResolveAddonHostOps(nil, nil, &missing, nil, nil)
		Expect(err).To(HaveOccurred())
		_, err = ResolveAddonHostOps(&flag, nil, &missing, nil, nil)
		Expect(err).To(HaveOccurred(), "an invalid selected file stops startup even when a flat list replaces its grants")
	})
	It("snapshots programmatic grants including an exact empty decision", func() {
		original := map[string][]string{"a": {"x."}}
		option := WithAddonHostOps(original)
		original["a"][0] = "changed."
		c := &ApplicationConfig{}
		option(c)
		Expect(c.AddonHostOps).To(Equal(map[string][]string{"a": {"x."}}))
		c.AddonHostOps["a"][0] = "other."
		second := &ApplicationConfig{}
		option(second)
		Expect(second.AddonHostOps).To(Equal(map[string][]string{"a": {"x."}}))
		WithAddonHostOps(map[string][]string{})(c)
		Expect(c.AddonHostOps).To(BeEmpty())
	})

	Describe("schemaVersion 2 and the deny list", func() {
		It("reads grants and deny, and accepts a deny-only file", func() {
			p, err := LoadAddonHostPolicyFile(write(`{"schemaVersion":2,"grants":{"sandbox":["workspace.","egress."]},"deny":{"*":["connector."],"web":["workspace.admin"],"telegram":["*"]}}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Grants).To(Equal(map[string][]string{"sandbox": {"workspace.", "egress."}}))
			Expect(p.Deny).To(Equal(map[string][]string{"*": {"connector."}, "web": {"workspace.admin"}, "telegram": {"*"}}))
			p, err = LoadAddonHostPolicyFile(write(`{"schemaVersion":2,"deny":{"*":["egress."]}}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Grants).To(BeEmpty())
			Expect(p.Deny).To(HaveLen(1))
		})
		DescribeTable("rejects", func(body, message string) {
			_, err := LoadAddonHostPolicyFile(write(body))
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
			Entry("deny in a v1 file", `{"schemaVersion":1,"grants":{},"deny":{}}`, "deny requires schemaVersion 2"),
			Entry("deny before the version in a v1 file", `{"deny":{},"schemaVersion":1,"grants":{}}`, "deny requires schemaVersion 2"),
			Entry("v1 without grants", `{"schemaVersion":1}`, "requires schemaVersion and grants"),
			Entry("version 3", `{"schemaVersion":3,"grants":{}}`, "requires a newer LocalAI"),
			Entry("a version that is not an integer", `{"schemaVersion":"2","grants":{}}`, "supported versions: 1, 2"),
			Entry("a missing version", `{"grants":{}}`, "requires schemaVersion"),
			Entry("duplicate deny key", `{"schemaVersion":2,"deny":{},"deny":{}}`, "duplicate"),
			Entry("duplicate deny add-on", `{"schemaVersion":2,"deny":{"a":["x."],"a":["y."]}}`, "duplicate"),
			Entry("duplicate deny entry", `{"schemaVersion":2,"deny":{"a":["x.","x."]}}`, "duplicate"),
			Entry("star next to another entry", `{"schemaVersion":2,"deny":{"a":["*","x."]}}`, "only entry"),
			Entry("star as a grants key", `{"schemaVersion":2,"grants":{"*":["x."]}}`, "invalid policy addon ID"),
			Entry("star as a grant", `{"schemaVersion":2,"grants":{"a":["*"]}}`, "invalid"),
			Entry("a bad deny entry", `{"schemaVersion":2,"deny":{"a":["X."]}}`, "invalid"),
			Entry("a bad deny key", `{"schemaVersion":2,"deny":{"A":["x."]}}`, "invalid policy addon ID"),
			Entry("null deny", `{"schemaVersion":2,"deny":null}`, "expected"),
			Entry("unknown key in v2", `{"schemaVersion":2,"grant":{}}`, "unknown policy key"),
			Entry("trailing data", `{"schemaVersion":2,"deny":{}} {}`, "trailing"),
		)
		It("bounds the deny list", func() {
			ids := []string{}
			for i := 0; i < 129; i++ {
				ids = append(ids, fmt.Sprintf(`"a-%d":["x."]`, i))
			}
			_, err := LoadAddonHostPolicyFile(write(`{"schemaVersion":2,"deny":{` + strings.Join(ids[:128], ",") + `}}`))
			Expect(err).NotTo(HaveOccurred())
			_, err = LoadAddonHostPolicyFile(write(`{"schemaVersion":2,"deny":{` + strings.Join(ids, ",") + `}}`))
			Expect(err).To(HaveOccurred())
			entries := []string{}
			for i := 0; i < 33; i++ {
				entries = append(entries, fmt.Sprintf(`"x.%s."`, string(rune('a'+i/26))+string(rune('a'+i%26))))
			}
			_, err = LoadAddonHostPolicyFile(write(`{"schemaVersion":2,"deny":{"a":[` + strings.Join(entries[:32], ",") + `]}}`))
			Expect(err).NotTo(HaveOccurred())
			_, err = LoadAddonHostPolicyFile(write(`{"schemaVersion":2,"deny":{"a":[` + strings.Join(entries, ",") + `]}}`))
			Expect(err).To(HaveOccurred())
		})
		It("keeps the 64 KiB file limit", func() {
			body := `{"schemaVersion":2,"deny":{}}`
			_, err := LoadAddonHostPolicyFile(write(body + strings.Repeat(" ", 65537-len(body))))
			Expect(err).To(HaveOccurred())
		})
		It("parses the flag grammar and unions it with the file", func() {
			got, err := ParseAddonHostDeny("*=egress., web=workspace.admin ,telegram=*")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(map[string][]string{"*": {"egress."}, "web": {"workspace.admin"}, "telegram": {"*"}}))
			Expect(ParseAddonHostDeny("")).To(BeEmpty())
			for _, bad := range []string{"x", "a=", "A=x.", "a=X.", "a=x.,a=x.", "a=*,a=x.", "=x.", "a=workspace", "*=*,*=x."} {
				_, err := ParseAddonHostDeny(bad)
				Expect(err).To(HaveOccurred(), bad)
			}
			Expect(MergeAddonHostDeny(map[string][]string{"a": {"x."}}, map[string][]string{"a": {"x.", "y."}, "b": {"*"}})).
				To(Equal(map[string][]string{"a": {"x.", "y."}, "b": {"*"}}))
			Expect(MergeAddonHostDeny(map[string][]string{"a": {"x."}}, map[string][]string{"a": {"*"}})).
				To(Equal(map[string][]string{"a": {"*"}}))
		})
		It("unions the file deny with the flag deny and applies it under a flat list", func() {
			policy := write(`{"schemaVersion":2,"grants":{"f":["w."]},"deny":{"a":["x."]}}`)
			ops, deny := "o=y.", "a=z.,*=*"
			got, err := ResolveAddonHostOps(&ops, nil, &policy, &deny, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Grants).To(Equal(map[string][]string{"o": {"y."}}))
			Expect(got.GrantsIgnored).To(BeTrue())
			Expect(got.Deny).To(Equal(map[string][]string{"a": {"x.", "z."}, "*": {"*"}}))
			bad := "nonsense"
			_, err = ResolveAddonHostOps(nil, nil, &policy, &bad, nil)
			Expect(err).To(HaveOccurred())
		})
	})
})
