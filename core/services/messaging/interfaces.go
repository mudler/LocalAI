package messaging

// Publisher publishes JSON-encoded messages to broadcast subjects.
type Publisher interface {
	Publish(subject string, data any) error
}

// Subscription represents a subscription that can be unsubscribed.
type Subscription interface {
	Unsubscribe() error
}

// Broadcaster is the whole messaging surface: a publish reaches every
// subscriber on every replica.
//
// There is no second interface. Request/reply became a control RPC on the
// worker's tunnel, queue groups became a claim queue on the job store, and what
// is left is fan-out, which is one method more than Publisher. The wider
// MessagingClient that used to sit here is deleted rather than shrunk: two
// exported names for one method set in one package is an invitation for the
// next author to pick whichever the surrounding file already imported.
//
// IsConnected and Close are deliberately NOT here, and their absence is stated
// rather than left to be inferred.
//
// IsConnected has no production consumer. It is asserted by specs and read by
// logging, and a carrier's consumers must not branch on it. "The carrier is
// down" is not one of the four conditions a node's state can be in, and no code
// may turn it into evidence that a worker is absent.
//
// Close DOES have real callers, and they hold a CONCRETE type rather than this
// interface, which is why the interface can omit it.
type Broadcaster interface {
	Publisher
	Subscribe(subject string, handler func([]byte)) (Subscription, error)
}

// Broadcaster has one implementation a deployment runs on, *pgbus.Bus, and it
// cannot be named here, because pgbus imports this package. The conformance
// assertion lives in interfaces_test.go alongside the test double's, which is
// also where the method-set pin lives: a conformance assertion stays true
// however many methods grow back, so it cannot say that the retired halves are
// gone.
//
// The NATS client that used to be asserted on this line is deleted. There is no
// second carrier: the last family that needed one, agent.<name>.cancel, is a
// control verb on the agent worker's own tunnel, and no component of a
// distributed deployment opens a connection to a message broker.
