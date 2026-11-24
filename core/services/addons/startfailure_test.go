// SPDX-License-Identifier: MIT
package addons

import (
	"errors"
	"fmt"

	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The start failure of an add-on", func() {
	It("gives the reason for people of a refused launch, and nothing for any other failure", func() {
		m := &Manager{startFailed: map[string]error{
			"sandbox": fmt.Errorf("start: %w", &addonruntime.StartRefusal{Reason: "WSL is not installed"}),
			"tables":  addonruntime.ErrRuntime,
			"other":   errors.New("internal detail"),
		}}
		Expect(m.StartFailure("sandbox")).To(Equal("WSL is not installed"))
		Expect(m.StartFailure("tables")).To(BeEmpty())
		Expect(m.StartFailure("other")).To(BeEmpty())
		Expect(m.StartFailure("missing")).To(BeEmpty())
	})
})
