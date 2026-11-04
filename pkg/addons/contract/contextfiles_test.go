// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The context files declaration", func() {
	notes := []ContextFile{
		{Scope: ScopeUser, Path: "Memory/index.md"},
		{Scope: ScopeAgent, Path: "Memory/agents/{agent}/index.md", Cap: 1024},
		{Scope: ScopeChannel, Path: "Memory/channels/{channel}/index.md"},
	}
	withFiles := func(files []ContextFile) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities.ContextFiles = files
		return m
	}
	host := func() HostCompatibility {
		h := compatible()
		return h
	}

	It("is valid with one file for each scope", func() {
		Expect(ValidateManifest(withFiles(notes), host())).To(Succeed())
	})

	It("refuses a path outside the Memory folder, an unsafe path and a wrong placeholder", func() {
		for _, bad := range []ContextFile{
			{Scope: ScopeUser, Path: "Documents/index.md"},
			{Scope: ScopeUser, Path: "Memory/../Documents/index.md"},
			{Scope: ScopeUser, Path: "/Memory/index.md"},
			{Scope: ScopeUser, Path: "Memory/index.txt"},
			{Scope: ScopeUser, Path: "Memory/{agent}/index.md"},
			{Scope: ScopeUser, Path: "Memory//index.md"},
			{Scope: ScopeUser, Path: "Memory\\index.md"},
			{Scope: ScopeUser, Path: "Memory/in\x00dex.md"},
			{Scope: ScopeAgent, Path: "Memory/agents/index.md"},
			{Scope: ScopeAgent, Path: "Memory/agents/{channel}/index.md"},
			{Scope: ScopeChannel, Path: "Memory/{channel}/{channel}/index.md"},
			{Scope: ScopeChannel, Path: "Memory/{x}/{channel}/index.md"},
			{Scope: ScopeUser, Path: "Memory/" + strings.Repeat("a", 200) + ".md"},
			{Scope: "everyone", Path: "Memory/index.md"},
			{Scope: "", Path: "Memory/index.md"},
		} {
			Expect(ValidateManifest(withFiles([]ContextFile{bad}), host())).NotTo(Succeed(), "%+v", bad)
		}
	})

	It("refuses a cap that is negative or above the host's index cap", func() {
		for _, c := range []int{-1, MaxContextFileBytes + 1} {
			Expect(ValidateManifest(withFiles([]ContextFile{{Scope: ScopeUser, Path: "Memory/index.md", Cap: c}}), host())).NotTo(Succeed())
		}
		Expect(ValidateManifest(withFiles([]ContextFile{{Scope: ScopeUser, Path: "Memory/index.md", Cap: MaxContextFileBytes}}), host())).To(Succeed())
	})

	It("refuses two files for one scope and more files than scopes", func() {
		twice := []ContextFile{{Scope: ScopeUser, Path: "Memory/index.md"}, {Scope: ScopeUser, Path: "Memory/other.md"}}
		Expect(ValidateManifest(withFiles(twice), host())).NotTo(Succeed())
		many := append(append([]ContextFile{}, notes...), ContextFile{Scope: ScopeUser, Path: "Memory/x.md"})
		Expect(ValidateManifest(withFiles(many), host())).NotTo(Succeed())
	})

	It("reads capabilities.contextFiles from JSON and refuses an unknown field in it", func() {
		var c Capabilities
		Expect(json.Unmarshal([]byte(`{"service":true,"contextFiles":[{"scope":"user","path":"Memory/index.md","cap":2048}]}`), &c)).To(Succeed())
		Expect(c.ContextFiles).To(Equal([]ContextFile{{Scope: ScopeUser, Path: "Memory/index.md", Cap: 2048}}))
		Expect(json.Unmarshal([]byte(`{"service":true,"contextFiles":[{"scope":"user","path":"Memory/index.md","all":true}]}`), &c)).NotTo(Succeed())
		Expect(json.Unmarshal([]byte(`{"service":true,"ContextFiles":[]}`), &c)).NotTo(Succeed())
	})
})
