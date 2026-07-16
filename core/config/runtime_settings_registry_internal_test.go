package config

import (
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("runtime settings registry", func() {
	// The registry is the single description of every runtime setting. If a
	// RuntimeSettings field has no registry row, the setting will be echoed,
	// persisted, or re-applied inconsistently - the exact bug class behind
	// #10845 (threads/context_size/f16 saved but ignored on restart). This
	// spec makes that a red test instead of a silent runtime regression.
	It("owns every RuntimeSettings field exactly once", func() {
		structFields := map[string]bool{}
		t := reflect.TypeOf(RuntimeSettings{})
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			Expect(name).NotTo(BeEmpty(), "RuntimeSettings.%s needs a json tag", t.Field(i).Name)
			structFields[name] = true
		}
		owned := map[string]int{}
		for _, f := range runtimeSettingsFields {
			for _, n := range f.jsonNames {
				owned[n]++
			}
		}
		for name := range structFields {
			Expect(owned[name]).To(Equal(1),
				"RuntimeSettings field %q must be owned by exactly one registry row - add it to runtimeSettingsFields in runtime_settings_registry.go", name)
		}
		for name := range owned {
			Expect(structFields[name]).To(BeTrue(),
				"registry row %q owns no RuntimeSettings field - remove or fix the row", name)
		}
	})
})
