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

// The two things that must answer one contract, asserted where the contract
// lives rather than at whichever adopter is migrated next.
//
// *pgbus.Bus is the carrier a deployment runs on and *testutil.FakeBus is what
// every consumer's spec runs against. A double that drifts out of the interface
// makes each adopter's suite fail in turn, in packages that do not own the
// interface and cannot say why.
//
// There were three. *messaging.Client, the NATS connection, is deleted: it
// carried one family, agent.<name>.cancel, and that family is a control verb on
// the agent worker's own tunnel now, so the type had no caller and the module
// had no reason to require a broker client.
//
// This file is package messaging_test because messaging cannot import testutil
// or pgbus: both import messaging.
var (
	_ messaging.Broadcaster = (*testutil.FakeBus)(nil)
	_ messaging.Broadcaster = (*pgbus.Bus)(nil)
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

// The two Describes that stood here pinned the NATS client's method set: that
// it was a Broadcaster plus its own lifecycle, and that QueueSubscribe,
// QueueSubscribeReply, SubscribeReply, Request and Conn could not come back on
// it. They are retired rather than moved, because a method set is a property of
// a type and the type is gone.
//
// What they were really guarding is the carrier, not the type, and that guard
// survives twice over. The retired halves cannot come back on the interface,
// which the "is fan-out and nothing else" spec above still asserts by count.
// And the connection itself cannot come back at all without a broker client in
// the module, which nats_absent_test.go asserts against go.mod and go.sum: a
// deleted method is one file away from being written again, while a deleted
// require has to be re-added on purpose and shows up in a diff.
