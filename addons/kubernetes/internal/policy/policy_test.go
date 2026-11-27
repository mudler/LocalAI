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
	It("defaults allow reads in every namespace, Secret values included, and no write tool", func() {
		d := policy.Defaults()
		Expect(d.AllNamespaces()).To(BeTrue())
		Expect(d.NamespaceAllowed("anything")).To(BeTrue())
		for _, t := range policy.WriteTools {
			Expect(d.ToolEnabled(t)).To(BeFalse(), t)
		}
		Expect(d.ReadSecretValues).To(BeTrue())
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
		old := `{"default":{"namespaces":["*"],"writeTools":["scale","drain"],"readSecretValues":true},"clusters":{
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
		Expect(p).To(Equal(policy.Damaged()), "disagreeing entries do not apply, and the server does not fall back to a wider default")
		v := s.View()
		Expect(v.Clusters).To(HaveKeyWithValue("https://b.example:6443", policy.Damaged()), "the page shows the policy in force")
		Expect(v.Legacy).To(Equal([]string{"https://b.example:6443#context=dev", "https://b.example:6443#context=ops"}))
		_, err = s.Set("https://a.example:6443", policy.Policy{Namespaces: []string{"team-c"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(policy.Open(dir).View().Legacy).To(HaveLen(2), "a save keeps the entries not migrated")
		p, err = policy.Open(dir).Get("https://b.example:6443")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(policy.Damaged()), "a save of another cluster keeps it closed")
		_, err = s.Set("https://b.example:6443", policy.Policy{Namespaces: []string{"team-b"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(policy.Open(dir).View().Legacy).To(BeEmpty())
		p, err = policy.Open(dir).Get("https://b.example:6443")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Namespaces).To(Equal([]string{"team-b"}))
	})

	It("gives every cluster the damaged policy while the file is damaged, and keeps the file aside on a save", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "policy.json")
		for _, broken := range []string{"{broken", `{"clusters":{"https://a.example":{"namespaces":["team-a"],"writeTools":["rm-rf"]}}}`, `{"default":{"namespaces":["Bad_NS"]},"clusters":{}}`} {
			Expect(os.WriteFile(path, []byte(broken), 0o600)).To(Succeed())
			p, err := policy.Open(dir).Get(key)
			Expect(err).NotTo(HaveOccurred())
			Expect(p).To(Equal(policy.Damaged()), broken)
			Expect(p.ReadSecretValues).To(BeFalse(), "a damaged file never shows Secret values")
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

	It("gives a damaged file no namespace, no write and hidden values: never more than an administrator set", func() {
		d := policy.Damaged()
		Expect(d.Namespaces).To(BeEmpty())
		Expect(d.NamespaceAllowed("default")).To(BeFalse())
		Expect(d.AllNamespaces()).To(BeFalse())
		Expect(d.WriteTools).To(BeEmpty())
		Expect(d.ReadSecretValues).To(BeFalse())
	})

	It("stores the damaged policy as the default when a save starts a new file, so the other clusters get no more", func() {
		dir := GinkgoT().TempDir()
		bad := `{"default":{"namespaces":["team-a"],"writeTools":[],"readSecretValues":false},"clusters":{"https://a.example":{"namespaces":["Bad_NS"]},"https://b.example":{"namespaces":["team-b"],"writeTools":[],"readSecretValues":false}}}`
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(bad), 0o600)).To(Succeed())
		s := policy.Open(dir)
		p, err := s.Get("https://b.example")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(policy.Damaged()))
		_, err = s.Set("https://a.example", policy.Policy{Namespaces: []string{"team-a"}})
		Expect(err).NotTo(HaveOccurred())
		for _, other := range []string{"https://b.example", "https://c.example"} {
			p, err = policy.Open(dir).Get(other)
			Expect(err).NotTo(HaveOccurred())
			Expect(p).To(Equal(policy.Damaged()), other+" keeps the damaged policy until an administrator enters its policy again")
		}
		v := policy.Open(dir).View()
		Expect(v.Broken).To(BeFalse())
		Expect(v.DefaultSet).To(BeTrue())
		Expect(v.Default).To(Equal(policy.Damaged()))
		Expect(v.Clusters).To(HaveLen(1))
	})

	It("keeps old entries whose key cannot be read apart, and a save removes them", func() {
		dir := GinkgoT().TempDir()
		old := `{"default":{"namespaces":["*"],"writeTools":["scale","drain"],"readSecretValues":true},"clusters":{
			"https://a.example:6443#context=dev":{"namespaces":["team-a"]},
			"not a server":{"namespaces":["team-x"]},
			"https://127.0.0.1:6443#context=dev":{"namespaces":["team-y"]},
			"https://b.example:6443#context=dev":{"namespaces":["team-b"]},
			"https://b.example:6443#context=ops":{"namespaces":["*"]}}}`
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(old), 0o600)).To(Succeed())
		s := policy.Open(dir)
		v := s.View()
		Expect(v.Broken).To(BeFalse())
		Expect(v.Invalid).To(Equal([]string{"https://127.0.0.1:6443#context=dev", "not a server"}))
		Expect(v.Legacy).To(Equal([]string{"https://b.example:6443#context=dev", "https://b.example:6443#context=ops"}))
		p, err := s.Get("https://a.example:6443")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Namespaces).To(Equal([]string{"team-a"}))
		_, err = s.Set("https://c.example", policy.Policy{Namespaces: []string{"team-c"}})
		Expect(err).NotTo(HaveOccurred())
		v = policy.Open(dir).View()
		Expect(v.Invalid).To(BeEmpty(), "they can never apply, so a save removes them")
		Expect(v.Legacy).To(HaveLen(2))
	})

	It("migrates keys written another way, unless they disagree", func() {
		dir := GinkgoT().TempDir()
		old := `{"clusters":{
			"https://a.example:0443":{"namespaces":["team-a"]},
			"https://[2001:0db8::1]":{"namespaces":["team-v6"]},
			"https://[2001:db8:0::1]":{"namespaces":["team-v6"]},
			"https://[::ffff:10.0.0.1]":{"namespaces":["team-x"]},
			"https://10.0.0.1":{"namespaces":["team-y"]},
			"https://d.example..":{"namespaces":["team-d1"]},
			"https://d.example.":{"namespaces":["team-d2"]}}}`
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(old), 0o600)).To(Succeed())
		s := policy.Open(dir)
		get := func(k string) policy.Policy {
			p, err := s.Get(k)
			Expect(err).NotTo(HaveOccurred())
			return p
		}
		Expect(get("https://a.example").Namespaces).To(Equal([]string{"team-a"}))
		Expect(get("https://[2001:db8::1]").Namespaces).To(Equal([]string{"team-v6"}))
		Expect(get("https://10.0.0.1").Namespaces).To(Equal([]string{"team-y"}), "the entry already written one way wins")
		Expect(get("https://d.example")).To(Equal(policy.Damaged()), "disagreeing forms do not apply")
		Expect(s.View().Legacy).To(Equal([]string{"https://d.example.", "https://d.example.."}))
	})

	It("writes a key one way, so writing it again gives the same key", func() {
		for _, k := range []string{"https://x.example.", "https://x.example..", "https://[2001:db8:0::1]", "https://[::ffff:10.0.0.1]:0443", "https://x.example:06443#context=dev"} {
			a, err := policy.ServerKey(k)
			Expect(err).NotTo(HaveOccurred(), k)
			b, err := policy.ServerKey(a)
			Expect(err).NotTo(HaveOccurred(), a)
			Expect(b).To(Equal(a), k)
		}
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
		b.ReadSecretValues = false
		Expect(a.Hash()).NotTo(Equal(b.Hash()))
		Expect(a.Hash()).To(Equal(policy.Defaults().Hash()))
	})

	It("names the rule in a refusal", func() {
		Expect(policy.Refuse(policy.RuleNamespaces, "team-b is not allowed").Error()).To(Equal("refused by the connection policy (namespaces): team-b is not allowed"))
	})
})
