package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubWorkQueue struct{}

func (stubWorkQueue) Enqueue(context.Context, messaging.WorkKind, any) error { return nil }

var _ = Describe("agentPoolOptions", func() {
	It("leaves the work queue unset without distributed services", func() {
		app := &Application{applicationConfig: &config.ApplicationConfig{}}

		opts := app.agentPoolOptions()

		// Strict comparison: the agent pool reads a non-nil interface as
		// distributed mode, and Gomega's BeNil would accept a typed nil.
		Expect(opts.WorkQueue == nil).To(BeTrue())
	})

	It("hands the agent pool the distributed work queue", func() {
		queue := stubWorkQueue{}
		app := &Application{
			applicationConfig: &config.ApplicationConfig{},
			distributed:       &DistributedServices{WorkQueue: queue},
		}

		opts := app.agentPoolOptions()

		Expect(opts.WorkQueue).To(Equal(messaging.WorkQueue(queue)))
	})
})
