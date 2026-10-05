// SPDX-License-Identifier: MIT
package metadata

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMetadata(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Backend response metadata")
}

var _ = Describe("usage convention", func() {
	It("round trips usage with arbitrary nested details", func() {
		original := Usage{InputUnits: 6, OutputUnits: 60, AccountingRule: "frame_steps_v1", Details: json.RawMessage(`{"frames":60,"future":{"mode":"fast","weights":[1,2]}}`)}
		data, err := EncodeUsage(original)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := ParseUsage(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(&original))
	})
	DescribeTable("ignores metadata without usage", func(data string) {
		usage, err := ParseUsage([]byte(data))
		Expect(err).NotTo(HaveOccurred())
		Expect(usage).To(BeNil())
	}, Entry("absent", ""), Entry("empty object", "{}"), Entry("unrelated", `{"timings":{"load":1},"custom":[true,"value"]}`))
	DescribeTable("rejects invalid counters", func(data string) {
		usage, err := ParseUsage([]byte(data))
		Expect(err).To(HaveOccurred())
		Expect(usage).To(BeNil())
	}, Entry("invalid JSON", `{`), Entry("array", `[]`), Entry("null metadata", `null`),
		Entry("null usage", `{"usage":null}`), Entry("missing input", `{"usage":{"output_units":1}}`),
		Entry("missing output", `{"usage":{"input_units":1}}`), Entry("null count", `{"usage":{"input_units":null,"output_units":1}}`),
		Entry("negative", `{"usage":{"input_units":1,"output_units":-1}}`),
		Entry("fraction", `{"usage":{"input_units":1.5,"output_units":1}}`),
		Entry("string", `{"usage":{"input_units":"1","output_units":1}}`),
		Entry("overflow", fmt.Sprintf(`{"usage":{"input_units":%d,"output_units":1}}`, math.MaxInt)))
	It("accepts explicit zero counts", func() {
		usage, err := ParseUsage([]byte(`{"usage":{"input_units":0,"output_units":0}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(usage).To(Equal(&Usage{}))
	})
	It("rejects invalid data when producing metadata", func() {
		_, err := EncodeUsage(Usage{InputUnits: -1})
		Expect(err).To(HaveOccurred())
		_, err = EncodeUsage(Usage{Details: json.RawMessage(`{`)})
		Expect(err).To(HaveOccurred())
	})
})
