package messaging_test

import (
	"reflect"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The three things that must answer one contract, asserted where the contract
// lives rather than at whichever adopter is migrated next.
//
// *pgbus.Bus is the carrier a deployment runs on, *messaging.Client is the one
// carrier left that a deployment still dials (see below), and *testutil.FakeBus
// is what every consumer's spec runs against. A double that drifts out of the
// interface makes each adopter's suite fail in turn, in packages that do not
// own the interface and cannot say why.
//
// This file is package messaging_test because messaging cannot import testutil
// or pgbus: both import messaging.
var (
	_ messaging.Broadcaster = (*testutil.FakeBus)(nil)
	_ messaging.Broadcaster = (*pgbus.Bus)(nil)
	_ messaging.Broadcaster = (*messaging.Client)(nil)
)

// exportedMethods names the exported method set of t, sorted, so a spec can
// assert what a type CAN be asked to do rather than what it happens to be
// asked today.
func exportedMethods(t reflect.Type) []string {
	names := make([]string, 0, t.NumMethod())
	for i := range t.NumMethod() {
		names = append(names, t.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

var _ = Describe("the messaging surface", func() {
	// Broadcaster is the whole interface. There is no second one, and the
	// count is asserted rather than the membership alone, because a method
	// added to this interface is a family being invited back onto a carrier
	// this deployment is being taken off.
	It("is fan-out and nothing else", func() {
		Expect(exportedMethods(reflect.TypeOf((*messaging.Broadcaster)(nil)).Elem())).
			To(Equal([]string{"Publish", "Subscribe"}),
				"Broadcaster grew a method: request/reply became a control RPC on the worker's tunnel and queue groups became a claim queue, and neither may come back through this interface")
	})

	It("has no interface that can queue-subscribe or request", func() {
		// MessagingClient is gone, and this is what says so from outside the
		// package: a name reintroduced for the retired halves would have to be
		// exported to be usable, and every consumer of this package holds a
		// Broadcaster.
		Expect(exportedMethods(reflect.TypeOf((*messaging.Publisher)(nil)).Elem())).
			To(Equal([]string{"Publish"}))
		Expect(exportedMethods(reflect.TypeOf((*messaging.Subscription)(nil)).Elem())).
			To(Equal([]string{"Unsubscribe"}))
	})
})

// The NATS client survives this commit, and it survives for exactly one family.
//
// agent.<name>.cancel is the one fan-out family that did not move to the
// PostgreSQL carrier: its only subscriber is the agent WORKER, which has no
// database and cannot join that carrier at all, so a cancel published there
// would reach no worker and be reported as sent. Both ends of that family still
// dial NATS, which is why messaging.New, the connect options and the TLS files
// are all still here.
//
// What the client may no longer do is everything else. Its queue and
// request/reply halves are deleted, so a family being put BACK on NATS is now a
// build error at the call site rather than a line that compiles, publishes
// successfully, and is delivered onto a carrier nobody subscribes to.
//
// The method set is pinned by NAME and not by a conformance assertion, because
// a conformance assertion cannot express absence: *messaging.Client satisfying
// Broadcaster stays true no matter how many methods are added back.
var _ = Describe("the NATS client's method set", func() {
	It("is a Broadcaster plus its own lifecycle, and nothing more", func() {
		Expect(exportedMethods(reflect.TypeOf((*messaging.Client)(nil)))).
			To(Equal([]string{"Close", "ConfirmRoundTrip", "IsConnected", "OnReconnect", "Publish", "Subscribe"}),
				"the NATS client grew a method back: it is the cancel family's carrier and may carry nothing else")
	})

	It("cannot be handed a queue group or a request", func() {
		// Named individually so the failure message says WHICH half came back.
		// As a set assertion alone, re-adding Request reads as an off-by-one.
		t := reflect.TypeOf((*messaging.Client)(nil))
		// Conn is in this list and is the reason the list is checkable at all:
		// while it existed, every name above it was one c.Conn().X() away, so
		// the deletions would have been a naming convention rather than a
		// constraint. ConfirmRoundTrip is what replaced it, and it hands back
		// an error rather than the connection.
		for _, retired := range []string{"QueueSubscribe", "QueueSubscribeReply", "SubscribeReply", "Request", "Conn"} {
			_, found := t.MethodByName(retired)
			Expect(found).To(BeFalse(),
				"%s is back on *messaging.Client; that half of the carrier was retired and its call sites moved to the worker's tunnel or to the claim queue", retired)
		}
	})
})
