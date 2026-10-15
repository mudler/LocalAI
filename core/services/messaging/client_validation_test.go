package messaging_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// The live client checks a subject before it touches the connection. A client
// with no connection at all proves the order: if any method reached the
// connection first it would panic or fail with a connection error instead of
// the subject sentinel, and no NATS server is needed to find out.
var _ = Describe("Client subject validation", func() {
	var c *messaging.Client

	BeforeEach(func() {
		c = &messaging.Client{}
	})

	calls := map[string]func(subject string) error{
		"Publish": func(s string) error { return c.Publish(s, map[string]string{"k": "v"}) },
		"Request": func(s string) error {
			_, err := c.Request(s, []byte("{}"), time.Second)
			return err
		},
		"Subscribe": func(s string) error {
			_, err := c.Subscribe(s, func([]byte) {})
			return err
		},
		"QueueSubscribe": func(s string) error {
			_, err := c.QueueSubscribe(s, "q", func([]byte) {})
			return err
		},
		"SubscribeReply": func(s string) error {
			_, err := c.SubscribeReply(s, func([]byte, func([]byte)) {})
			return err
		},
		"QueueSubscribeReply": func(s string) error {
			_, err := c.QueueSubscribeReply(s, "q", func([]byte, func([]byte)) {})
			return err
		},
	}

	for name, call := range calls {
		It(name+" refuses an unserved root before using the connection", func() {
			Expect(call("bogus.thing")).To(MatchError(messaging.ErrUnservedSubject))
		})

		It(name+" refuses a multi-token wildcard before using the connection", func() {
			Expect(call("jobs.>")).To(MatchError(messaging.ErrUnsupportedWildcard))
		})
	}
})
