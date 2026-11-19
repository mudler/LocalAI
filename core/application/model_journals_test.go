package application

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("the policy for interrupted model journals", func() {
	fresh := &gallery.Journal{Phase: gallery.PhaseCommitting, Instance: "other", UpdatedAt: time.Now()}
	stale := &gallery.Journal{Phase: gallery.PhaseCommitting, Instance: "other", UpdatedAt: time.Now().Add(-time.Hour)}
	mine := &gallery.Journal{Phase: gallery.PhaseCommitting, Instance: "me", UpdatedAt: time.Now()}

	It("acts on every journal standalone", func() {
		act := journalPolicy(&config.ApplicationConfig{})
		Expect(act(fresh)).To(BeTrue())
	})

	It("acts at start only on its own journals, and only with an instance ID the operator set", func() {
		act := journalPolicy(&config.ApplicationConfig{Distributed: config.DistributedConfig{Enabled: true, InstanceID: "me"}})
		Expect(act(mine)).To(BeTrue())
		Expect(act(stale)).To(BeFalse(), "the recovery pass checks the lock of another frontend's journal")
		Expect(act(fresh)).To(BeFalse())

		generated := journalPolicy(&config.ApplicationConfig{Distributed: config.DistributedConfig{Enabled: true, InstanceID: "me", InstanceIDGenerated: true}})
		Expect(generated(mine)).To(BeFalse(), "a generated ID changes at every start")
	})
})

var _ = Describe("the cluster-wide lock of a model", func() {
	It("lets one holder at a time take the lock of a model, and others take other models", func() {
		lock := modelLockFor(testutil.SetupTestDB())
		ctx := context.Background()
		release, err := lock(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		_, err = lock(ctx, "tiny")
		Expect(err).To(MatchError(gallery.ErrModelLocked))
		other, err := lock(ctx, "other")
		Expect(err).ToNot(HaveOccurred())
		other()

		release()
		again, err := lock(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		again()
	})
})
