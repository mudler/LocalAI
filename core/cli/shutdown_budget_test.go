package cli

import (
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/signals"
)

var _ = Describe("the shutdown timeout flag", func() {
	It("defaults to the drain of the shutdown budget", func() {
		f, ok := reflect.TypeOf(RunCMD{}).FieldByName("ShutdownTimeout")
		Expect(ok).To(BeTrue())
		d, err := time.ParseDuration(f.Tag.Get("default"))
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(Equal(signals.DefaultDrainTimeout), "the flag and pkg/signals must give the same default")
		// The help names the extra time of the hooks and the slack.
		Expect(f.Tag.Get("help")).To(ContainSubstring((signals.WorstCase(0)).String()))
		Expect(f.Tag.Get("help")).To(ContainSubstring(signals.WorstCase(d).String()))
	})
})
