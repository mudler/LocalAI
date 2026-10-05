package nodes

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pingStub is a NodeCommandSender that answers PingNode and nothing else.
type pingStub struct {
	NodeCommandSender
	err error
}

func (p pingStub) PingNode(string) error { return p.err }

var _ = Describe("Scheduler liveness on the control path", func() {
	node := &BackendNode{ID: "n1", Name: "n1"}

	answers := func(err error) bool {
		r := &SmartRouter{unloader: pingStub{err: err}}
		return r.nodeAnswersOnBus(node)
	}

	It("excludes a node only when there is no route to it", func() {
		Expect(answers(fmt.Errorf("wrapped: %w", ErrNoRoute))).To(BeFalse())
	})

	It("keeps a node whose ping timed out", func() {
		Expect(answers(context.DeadlineExceeded)).To(BeTrue())
	})

	It("keeps a node whose ping failed for any other reason", func() {
		Expect(answers(errors.New("connection reset"))).To(BeTrue())
	})

	It("keeps every node when no command sender is configured", func() {
		r := &SmartRouter{}
		Expect(r.nodeAnswersOnBus(node)).To(BeTrue())
	})
})
