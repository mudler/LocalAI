// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"strings"
)

func modelActionFixture(actions string) []byte {
	return []byte(strings.Replace(string(extensionFixture()), `"homeCommands":`, `"modelActions":`+actions+`,"homeCommands":`, 1))
}

var _ = Describe("Optional model actions", func() {
	It("bounds model actions to one mapping per supported capability", func() {
		m, err := ParseManifest(extensionFixture())
		Expect(err).NotTo(HaveOccurred())
		for _, capability := range ModelActionCapabilities() {
			m.Capabilities.UI.ModelActions = append(m.Capabilities.UI.ModelActions, ModelAction{Capability: capability, Page: "work"})
		}
		Expect(m.Capabilities.UI.ModelActions).To(HaveLen(MaxModelActions))
		b, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		_, err = ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		m.Capabilities.UI.ModelActions = append(m.Capabilities.UI.ModelActions, ModelAction{Capability: "FLAG_IMAGE", Page: "work"})
		b, err = json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		_, err = ParseManifest(b)
		Expect(err).To(HaveOccurred())
	})
	It("declares only owning model actions for existing addon destinations", func() {
		expected := map[string][]ModelAction{
			"studio": {{Capability: "FLAG_IMAGE", Page: "studio", Path: "images"}, {Capability: "FLAG_VIDEO", Page: "studio", Path: "video"}, {Capability: "FLAG_TTS", Page: "studio", Path: "tts"}, {Capability: "FLAG_SOUND_GENERATION", Page: "studio", Path: "sound"}},
			"talk":   {{Capability: "FLAG_TRANSCRIPT", Page: "talk"}},
			"face":   {{Capability: "FLAG_FACE_RECOGNITION", Page: "recognition"}},
			"voice":  {{Capability: "FLAG_SPEAKER_RECOGNITION", Page: "recognition"}},
		}
		for id, actions := range expected {
			b, err := os.ReadFile(fmt.Sprintf("../../../addons/%s/manifest.json", id))
			Expect(err).NotTo(HaveOccurred())
			m, err := ParseManifest(b)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Capabilities.UI.ModelActions).To(Equal(actions))
		}
	})

	It("round trips declared model destinations without changing protocol or SDK", func() {
		m, err := ParseManifest(modelActionFixture(`[{"capability":"FLAG_IMAGE","page":"work","path":"images"},{"capability":"FLAG_TRANSCRIPT","page":"history"}]`))
		Expect(err).NotTo(HaveOccurred())
		b, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"modelActions":[{"capability":"FLAG_IMAGE","page":"work","path":"images"},{"capability":"FLAG_TRANSCRIPT","page":"history"}]`))
		again, err := ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(m))
	})
	It("accepts SDK route segment and combined path boundaries", func() {
		for _, path := range []string{"a_~.-9", strings.Repeat("a", 64), "a/b/c/d/e/f/g", strings.Repeat("a", 64) + "/" + strings.Repeat("b", 64) + "/" + strings.Repeat("c", 64) + "/" + strings.Repeat("d", 56)} {
			_, err := ParseManifest(modelActionFixture(`[{"capability":"FLAG_IMAGE","page":"work","path":"` + path + `"}]`))
			Expect(err).NotTo(HaveOccurred(), path)
		}
	})
	DescribeTable("rejects unsafe or ambiguous model actions", func(actions string) {
		_, err := ParseManifest(modelActionFixture(actions))
		Expect(err).To(HaveOccurred())
	},
		Entry("unknown field", `[{"capability":"FLAG_IMAGE","page":"work","url":"https://example.org"}]`),
		Entry("duplicate field", `[{"capability":"FLAG_IMAGE","page":"work","page":"history"}]`),
		Entry("case field", `[{"Capability":"FLAG_IMAGE","page":"work"}]`),
		Entry("case page", `[{"capability":"FLAG_IMAGE","Page":"work"}]`),
		Entry("case path", `[{"capability":"FLAG_IMAGE","page":"work","Path":"images"}]`),
		Entry("duplicate capability", `[{"capability":"FLAG_IMAGE","page":"work"},{"capability":"FLAG_IMAGE","page":"history"}]`),
		Entry("missing page", `[{"capability":"FLAG_IMAGE"}]`),
		Entry("undeclared page", `[{"capability":"FLAG_IMAGE","page":"outside"}]`),
		Entry("unknown capability", `[{"capability":"FLAG_FUTURE","page":"work"}]`),
		Entry("native chat", `[{"capability":"FLAG_CHAT","page":"work"}]`),
		Entry("native completion", `[{"capability":"FLAG_COMPLETION","page":"work"}]`),
		Entry("aggregate LLM", `[{"capability":"FLAG_LLM","page":"work"}]`),
		Entry("any capability", `[{"capability":"FLAG_ANY","page":"work"}]`),
		Entry("noncanonical capability", `[{"capability":"flag_image","page":"work"}]`),
		Entry("absolute", `[{"capability":"FLAG_IMAGE","page":"work","path":"/images"}]`),
		Entry("URL", `[{"capability":"FLAG_IMAGE","page":"work","path":"https://example.org"}]`),
		Entry("traversal", `[{"capability":"FLAG_IMAGE","page":"work","path":"a/../b"}]`),
		Entry("dot", `[{"capability":"FLAG_IMAGE","page":"work","path":"."}]`),
		Entry("encoded traversal", `[{"capability":"FLAG_IMAGE","page":"work","path":"%2e%2e"}]`),
		Entry("query", `[{"capability":"FLAG_IMAGE","page":"work","path":"images?model=x"}]`),
		Entry("hash", `[{"capability":"FLAG_IMAGE","page":"work","path":"images#x"}]`),
		Entry("backslash", `[{"capability":"FLAG_IMAGE","page":"work","path":"a\\b"}]`),
		Entry("empty segment", `[{"capability":"FLAG_IMAGE","page":"work","path":"a//b"}]`),
		Entry("trailing slash", `[{"capability":"FLAG_IMAGE","page":"work","path":"a/"}]`),
		Entry("empty path", `[{"capability":"FLAG_IMAGE","page":"work","path":""}]`),
		Entry("null path", `[{"capability":"FLAG_IMAGE","page":"work","path":null}]`),
		Entry("long segment", `[{"capability":"FLAG_IMAGE","page":"work","path":"`+strings.Repeat("a", 65)+`"}]`),
		Entry("too many segments", `[{"capability":"FLAG_IMAGE","page":"work","path":"a/b/c/d/e/f/g/h"}]`),
		Entry("long combined route", `[{"capability":"FLAG_IMAGE","page":"work","path":"`+strings.Repeat("a", 64)+`/`+strings.Repeat("b", 64)+`/`+strings.Repeat("c", 64)+`/`+strings.Repeat("d", 57)+`"}]`),
	)
	It("rejects noncanonical modelActions entry keys and duplicate keys", func() {
		for _, key := range []string{`"ModelActions":[]`, `"modelActions":[],"modelActions":[]`, `"modelActions":[],"ModelActions":[]`} {
			b := strings.Replace(string(extensionFixture()), `"homeCommands":`, key+`,"homeCommands":`, 1)
			_, err := ParseManifest([]byte(b))
			Expect(err).To(HaveOccurred())
		}
	})
})
