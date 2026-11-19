package application

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("the policy for interrupted model journals", func() {
	fresh := &gallery.Journal{Phase: gallery.PhaseCommitting, Instance: "other", UpdatedAt: time.Now()}
	stale := &gallery.Journal{Phase: gallery.PhaseCommitting, Instance: "other", UpdatedAt: time.Now().Add(-time.Hour)}
	mine := &gallery.Journal{Phase: gallery.PhaseCommitting, Instance: "me", UpdatedAt: time.Now()}

	It("acts on every journal standalone", func() {
		act := journalPolicy(&config.ApplicationConfig{})
		Expect(act(fresh)).To(BeTrue())
	})

	It("acts only on its own and stale journals in distributed mode", func() {
		act := journalPolicy(&config.ApplicationConfig{Distributed: config.DistributedConfig{Enabled: true, InstanceID: "me"}})
		Expect(act(mine)).To(BeTrue())
		Expect(act(stale)).To(BeTrue())
		Expect(act(fresh)).To(BeFalse(), "another frontend may still be writing it")
	})
})
