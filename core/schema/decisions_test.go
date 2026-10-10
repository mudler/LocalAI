// SPDX-License-Identifier: MIT
package schema_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Decisions probability serialization", func() {
	DescribeTable("preserves label presence",
		func(wire string) {
			var probability schema.DecisionProbability
			Expect(json.Unmarshal([]byte(wire), &probability)).To(Succeed())
			encoded, err := json.Marshal(probability)
			Expect(err).NotTo(HaveOccurred())
			Expect(encoded).To(MatchJSON(wire))
		},
		Entry("empty score label", `{"value":0,"label":"","probability":0.5}`),
		Entry("nonempty score label", `{"value":1,"label":"high","probability":0.5}`),
		Entry("absent choice label", `{"value":true,"probability":0.5}`),
	)
})
