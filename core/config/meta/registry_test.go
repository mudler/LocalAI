package meta_test

import (
	"reflect"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("alias field metadata", func() {
	It("registers the alias field as a model-select in the alias section", func() {
		reg := meta.DefaultRegistry()
		f, ok := reg["alias"]
		Expect(ok).To(BeTrue(), "alias field should have a registry override")
		Expect(f.Section).To(Equal("alias"))
		Expect(f.Component).To(Equal("model-select"))
	})

	It("defines an alias section", func() {
		var found bool
		for _, s := range meta.DefaultSections() {
			if s.ID == "alias" {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "DefaultSections should include an alias section")
	})

	It("registers the failover section", func() {
		reg := meta.DefaultRegistry()
		Expect(reg).To(HaveKey("failover.targets"))
		Expect(reg["failover.targets"].Section).To(Equal("failover"))
		Expect(reg["failover.targets"].Component).To(Equal("failover-targets"))
		var ids []string
		for _, s := range meta.DefaultSections() {
			ids = append(ids, s.ID)
		}
		Expect(ids).To(ContainElement("failover"))
	})
})

var _ = Describe("MCP field metadata", func() {
	var fields map[string]meta.FieldMeta

	BeforeEach(func() {
		md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())
		fields = make(map[string]meta.FieldMeta, len(md.Fields))
		for _, field := range md.Fields {
			fields[field.Path] = field
		}
	})

	DescribeTable("registers embedded MCP configuration as YAML code",
		func(path, label, transportDetail string) {
			f, ok := fields[path]
			Expect(ok).To(BeTrue(), "%s should be present in generated metadata", path)
			Expect(f.Section).To(Equal("mcp"))
			Expect(f.Label).To(Equal(label))
			Expect(f.Description).To(ContainSubstring("mcpServers"))
			Expect(f.Description).To(ContainSubstring(transportDetail))
			Expect(f.Component).To(Equal("code-editor"))
			Expect(f.Language).To(Equal("yaml"))
		},
		Entry("remote servers", "mcp.remote", "Remote MCP Servers", "Streamable HTTP"),
		Entry("stdio servers", "mcp.stdio", "MCP STDIO Servers", "local commands"),
	)
})

var _ = Describe("Decisions router metadata", func() {
	It("offers decisions alongside existing classifiers", func() {
		f := meta.DefaultRegistry()["router.classifier"]
		Expect(f.Options).To(ContainElement(meta.FieldOption{Value: "decisions", Label: "Decisions (native probabilities)"}))
		md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())
		for _, field := range md.Fields {
			if field.Path == "router.classifier_model" {
				Expect(field.AutocompleteBy).NotTo(BeNil())
				Expect(field.AutocompleteBy.Field).To(Equal("router.classifier"))
				Expect(field.AutocompleteBy.Providers).To(HaveKeyWithValue("decisions", "models:decisions"))
				Expect(field.AutocompleteBy.Providers).To(HaveKeyWithValue("colbert", "models:rerank"))
				Expect(field.AutocompleteProvider).To(Equal(meta.ProviderModelsScore))
			}
		}
	})
})

var _ = Describe("backend-only upscale options", func() {
	It("does not expose scale or tile size as generic root configuration", func() {
		reg := meta.DefaultRegistry()
		Expect(reg).NotTo(HaveKey("upscale_scale"))
		Expect(reg).NotTo(HaveKey("upscale_tile_size"))
		md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), reg)
		for _, field := range md.Fields {
			Expect(field.Path).NotTo(BeElementOf("upscale_scale", "upscale_tile_size"))
		}
	})
})
