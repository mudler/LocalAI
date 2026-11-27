package config_test

import (
	"fmt"

	. "github.com/mudler/LocalAI/core/config"
	lc "github.com/mudler/LocalAI/pkg/enginesettings/llamacpp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the MTP mode of the model editor", func() {
	// Picking MTP in the editor writes spec_type, so the load-time defaults
	// no longer apply. The mode writes the same values instead, or a model
	// would draft differently after the person picked what it already ran.
	It("writes the values LocalAI applies to a model with an MTP head", func() {
		var got []string
		for _, m := range lc.Engine().Schema().Drafting.Modes {
			if m.ID != "mtp" {
				continue
			}
			got = append(got, "spec_type:"+m.Types[0])
			for _, k := range []string{"spec_n_max", "spec_p_min"} {
				got = append(got, fmt.Sprintf("%s:%v", k, m.Set[k]))
			}
		}
		Expect(got).To(Equal(MTPSpecOptions()))
	})
})
