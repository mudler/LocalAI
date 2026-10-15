package testutil_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("FakeBus", func() {
	messagingtest.RunBroadcasterConformance(func() (messaging.Broadcaster, func()) {
		return testutil.NewFakeBus(), func() {}
	})
})
