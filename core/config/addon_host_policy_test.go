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
		Entry("wrong version", `{"schemaVersion":2,"grants":{}}`),
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
	It("resolves presence without union or opening an ignored policy", func() {
		empty, env, flag := "", "env=x.", "flag=y."
		bad := "/not-present-policy.json"
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
			got, err := ResolveAddonHostOps(tc.flag, tc.env, tc.policy)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Grants).To(Equal(tc.want))
			Expect(got.Source).To(Equal(tc.source))
			Expect(got.PolicyIgnored).To(Equal(tc.ignored))
		}
		_, err := ResolveAddonHostOps(nil, nil, &bad)
		Expect(err).To(HaveOccurred())
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
})
