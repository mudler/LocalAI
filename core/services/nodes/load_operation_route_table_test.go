package nodes_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// The route table runs the LoadOperationControl conformance as a carrier: the
// carrier.Commands holder over a table that holds a direct (NATS) and a tunnel
// route, for a node that has one of them or both.
func init() {
	nodes.RegisterLoadOperationCarrier("the route table, for a node on the direct route", func() nodes.LoadOperationHarness {
		on, off := nodes.NewNATSLoadOperationHarness(), nodes.NewHTTPLoadOperationHarness()
		return newTableHarness(on, off, on, off, []nodes.Route{{Kind: nodes.RouteDirect}}, true)
	})
	nodes.RegisterLoadOperationCarrier("the route table, for a node on the tunnel route", func() nodes.LoadOperationHarness {
		on, off := nodes.NewHTTPLoadOperationHarness(), nodes.NewNATSLoadOperationHarness()
		return newTableHarness(on, off, off, on, []nodes.Route{{Kind: nodes.RouteTunnel}}, true)
	})
	nodes.RegisterLoadOperationCarrier("the route table, for a node with both routes whose direct route finds no route", func() nodes.LoadOperationHarness {
		on, off := nodes.NewHTTPLoadOperationHarness(), nodes.NewNATSLoadOperationHarness()
		// NATS answers that nobody serves the node, so every call moves on to
		// the tunnel, and only the conditions of the tunnel are under test.
		off.NoRoute()
		return newTableHarness(on, off, off, on, []nodes.Route{{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel}}, false)
	})
}

// tableHarness drives the route that the node uses (on) and reports what both
// routes sent. off is the other route: a call that reaches it when the node does
// not have it shows in Sent and fails the specs that count requests.
type tableHarness struct {
	on, off nodes.LoadOperationHarness
	control nodes.LoadOperationControl
	// countOff says whether the requests of off count as sent. They do not when
	// off is a route of the node that answers no route.
	countOff bool
}

func newTableHarness(on, off, direct, tun nodes.LoadOperationHarness, routes []nodes.Route, countOff bool) *tableHarness {
	t := carrier.NewRouteTable(func(context.Context, string) ([]nodes.Route, error) { return routes, nil })
	t.Pin(&carrier.WorkerRoute{Kind: nodes.RouteDirect, Commands: direct.Control().(nodes.NodeControl)})
	t.Pin(&carrier.WorkerRoute{Kind: nodes.RouteTunnel, Commands: tun.Control().(nodes.NodeControl)})
	return &tableHarness{on: on, off: off, control: carrier.NewCommands(t), countOff: countOff}
}

func (h *tableHarness) Control() nodes.LoadOperationControl { return h.control }
func (h *tableHarness) NoRoute()                            { h.on.NoRoute() }
func (h *tableHarness) TimesOut()                           { h.on.TimesOut() }
func (h *tableHarness) PathFails()                          { h.on.PathFails() }
func (h *tableHarness) InfrastructureFails()                { h.on.InfrastructureFails() }
func (h *tableHarness) WorkerRefuses()                      { h.on.WorkerRefuses() }
func (h *tableHarness) WorkerAnswers()                      { h.on.WorkerAnswers() }
func (h *tableHarness) WorkerAnswersUnreadably()            { h.on.WorkerAnswersUnreadably() }

func (h *tableHarness) Sent() []nodes.SentRequest {
	sent := h.on.Sent()
	if h.countOff {
		sent = append(sent, h.off.Sent()...)
	}
	return sent
}
