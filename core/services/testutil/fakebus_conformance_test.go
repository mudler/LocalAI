package testutil_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("FakeBus", func() {
	messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
		bus := testutil.NewFakeBus()
		return messagingtest.Carrier{Bus: bus, Peer: bus, ServesControlRoots: true}
	})
})
