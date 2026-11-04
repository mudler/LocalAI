package triggers

import (
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Runner timing", func() {
	// A schedule with a named time zone must work on an image without tzdata, so
	// the zone database has to be part of the binary.
	It("embeds the zone database", func() {
		out, err := exec.Command("go", "list", "-deps", ".").Output()
		Expect(err).NotTo(HaveOccurred(), "go list")
		Expect("\n"+string(out)).To(ContainSubstring("\ntime/tzdata\n"), "time/tzdata is not linked into the triggers package")
	})

	// A schedule fires on the pass after its time, so the pass interval is how late
	// a run can be, and a file trigger waits at least one pass past its quiet time.
	It("has a pass short enough for a run to start on time", func() {
		Expect(DefaultTick).To(BeNumerically("<=", 5*time.Second))
	})
})
