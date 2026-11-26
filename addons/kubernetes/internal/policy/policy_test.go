// SPDX-License-Identifier: MIT
package policy_test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const key = "https://cluster.example:6443#context=dev"

var _ = Describe("the policy of a cluster", func() {
	It("defaults allow every namespace, every write tool but exec, and no Secret values", func() {
		d := policy.Defaults()
		Expect(d.AllNamespaces()).To(BeTrue())
		Expect(d.NamespaceAllowed("anything")).To(BeTrue())
		for _, t := range policy.WriteTools {
			Expect(d.ToolEnabled(t)).To(Equal(t != "exec"), t)
		}
		Expect(d.ReadSecretValues).To(BeFalse())
	})

	It("normalizes and refuses bad input", func() {
		p, err := policy.Policy{Namespaces: []string{"team-b", "team-a", "team-a"}, WriteTools: []string{"scale", "apply", "scale"}}.Normalize()
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Namespaces).To(Equal([]string{"team-a", "team-b"}))
		Expect(p.WriteTools).To(Equal([]string{"apply", "scale"}))
		Expect(p.NamespaceAllowed("team-c")).To(BeFalse())
		p, _ = policy.Policy{Namespaces: []string{"team-a", "*"}}.Normalize()
		Expect(p.Namespaces).To(Equal([]string{"*"}))
		many := make([]string, policy.MaxNamespaces+1)
		for i := range many {
			many[i] = "ns-" + strings.Repeat("a", i%40) + string(rune('a'+i%26))
		}
		for name, bad := range map[string]policy.Policy{
			"empty":          {},
			"bad namespace":  {Namespaces: []string{"Team_A"}},
			"long namespace": {Namespaces: []string{strings.Repeat("a", 64)}},
			"unknown tool":   {Namespaces: []string{"*"}, WriteTools: []string{"rm-rf"}},
			"too many":       {Namespaces: many},
		} {
			_, err := bad.Normalize()
			Expect(err).To(HaveOccurred(), name)
		}
	})

	It("stores per cluster key and survives a reopen", func() {
		dir := GinkgoT().TempDir()
		s := policy.Open(dir)
		_, err := s.Set(key, policy.Policy{Namespaces: []string{"team-a"}, WriteTools: []string{"scale"}})
		Expect(err).NotTo(HaveOccurred())
		p, err := policy.Open(dir).Get(key)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Namespaces).To(Equal([]string{"team-a"}))
		other, err := policy.Open(dir).Get("https://other.example:6443#context=dev")
		Expect(err).NotTo(HaveOccurred())
		Expect(other).To(Equal(policy.Defaults()))
		st, err := os.Stat(filepath.Join(dir, "policy.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		all, err := policy.Open(dir).All()
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveKey(key))
	})

	It("reads a broken file as an error, not as the defaults", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte("{broken"), 0o600)).To(Succeed())
		_, err := policy.Open(dir).Get(key)
		Expect(err).To(MatchError(policy.ErrUnreadable))
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"clusters":{"k":{"namespaces":[]}}}`), 0o600)).To(Succeed())
		_, err = policy.Open(dir).Get(key)
		Expect(err).To(MatchError(policy.ErrUnreadable))
	})

	It("changes its hash with the policy", func() {
		a := policy.Defaults()
		b := policy.Defaults()
		b.ReadSecretValues = true
		Expect(a.Hash()).NotTo(Equal(b.Hash()))
		Expect(a.Hash()).To(Equal(policy.Defaults().Hash()))
	})

	It("names the rule in a refusal", func() {
		Expect(policy.Refuse(policy.RuleNamespaces, "team-b is not allowed").Error()).To(Equal("refused by the connection policy (namespaces): team-b is not allowed"))
	})
})
