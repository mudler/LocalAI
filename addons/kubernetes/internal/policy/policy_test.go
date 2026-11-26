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
	It("defaults allow reads in every namespace, no write tool, and no Secret values", func() {
		d := policy.Defaults()
		Expect(d.AllNamespaces()).To(BeTrue())
		Expect(d.NamespaceAllowed("anything")).To(BeTrue())
		for _, t := range policy.WriteTools {
			Expect(d.ToolEnabled(t)).To(BeFalse(), t)
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

	It("stores per server and survives a reopen", func() {
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
		v := policy.Open(dir).View()
		Expect(v.Clusters).To(HaveKey("https://cluster.example:6443"))
		Expect(v.DefaultSet).To(BeFalse())
	})

	It("finds the policy of a server however the kubeconfig writes it", func() {
		s := policy.Open("")
		_, err := s.Set("https://Cluster.Example:443/", policy.Policy{Namespaces: []string{"team-a"}, WriteTools: []string{"scale"}})
		Expect(err).NotTo(HaveOccurred())
		for _, k := range []string{"https://cluster.example#context=dev", "https://cluster.example:443#context=renamed", "https://CLUSTER.example.#context=x", "https://cluster.example"} {
			p, err := s.Get(k)
			Expect(err).NotTo(HaveOccurred(), k)
			Expect(p.Namespaces).To(Equal([]string{"team-a"}), k)
		}
		p, err := s.Get("https://cluster.example:6443#context=dev")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(policy.Defaults()), "another port is another server")
	})

	It("uses the default policy an administrator set for a cluster with none", func() {
		s := policy.Open(GinkgoT().TempDir())
		_, err := s.SetDefault(policy.Policy{Namespaces: []string{"team-a"}, WriteTools: []string{"scale"}})
		Expect(err).NotTo(HaveOccurred())
		p, err := s.Get(key)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.ToolEnabled("scale")).To(BeTrue())
		v := s.View()
		Expect(v.DefaultSet).To(BeTrue())
		Expect(v.Default.Namespaces).To(Equal([]string{"team-a"}))
	})

	It("migrates entries of the old per-context key, unless they disagree", func() {
		dir := GinkgoT().TempDir()
		old := `{"clusters":{
			"https://a.example:6443#context=dev":{"namespaces":["team-a"],"writeTools":["scale"]},
			"https://a.example:6443#context=ops":{"namespaces":["team-a"],"writeTools":["scale"]},
			"https://b.example:6443#context=dev":{"namespaces":["team-b"],"writeTools":["scale"]},
			"https://b.example:6443#context=ops":{"namespaces":["*"],"writeTools":["scale","drain"]}}}`
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(old), 0o600)).To(Succeed())
		s := policy.Open(dir)
		p, err := s.Get("https://a.example:6443#context=new")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Namespaces).To(Equal([]string{"team-a"}))
		p, err = s.Get("https://b.example:6443#context=ops")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(policy.Defaults()), "disagreeing entries do not apply")
		v := s.View()
		Expect(v.Legacy).To(Equal([]string{"https://b.example:6443#context=dev", "https://b.example:6443#context=ops"}))
		_, err = s.Set("https://a.example:6443", policy.Policy{Namespaces: []string{"team-c"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(policy.Open(dir).View().Legacy).To(HaveLen(2), "a save keeps the entries not migrated")
		_, err = s.Set("https://b.example:6443", policy.Policy{Namespaces: []string{"team-b"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(policy.Open(dir).View().Legacy).To(BeEmpty())
	})

	It("gives every cluster the defaults while the file is damaged, and keeps the file aside on a save", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "policy.json")
		for _, broken := range []string{"{broken", `{"clusters":{"https://a.example":{"namespaces":[]}}}`, `{"default":{"namespaces":["Bad_NS"]},"clusters":{}}`} {
			Expect(os.WriteFile(path, []byte(broken), 0o600)).To(Succeed())
			p, err := policy.Open(dir).Get(key)
			Expect(err).NotTo(HaveOccurred())
			Expect(p).To(Equal(policy.Defaults()), broken)
			Expect(policy.Open(dir).View().Broken).To(BeTrue(), broken)
		}
		s := policy.Open(dir)
		_, err := s.Set(key, policy.Policy{Namespaces: []string{"team-a"}})
		Expect(err).NotTo(HaveOccurred())
		aside, _ := filepath.Glob(path + ".damaged-*")
		Expect(aside).To(HaveLen(1))
		b, _ := os.ReadFile(aside[0])
		Expect(string(b)).To(ContainSubstring("Bad_NS"))
		v := policy.Open(dir).View()
		Expect(v.Broken).To(BeFalse())
		Expect(v.Clusters).To(HaveLen(1))
	})

	It("refuses a key that is not a server", func() {
		_, err := policy.Open("").Set("not-a-server", policy.Defaults())
		Expect(err).To(HaveOccurred())
		_, err = policy.Open("").Set("https://127.0.0.1:6443", policy.Defaults())
		Expect(err).To(HaveOccurred())
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
