package messaging_test

import (
	"context"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
)

var (
	natsOnce sync.Once
	natsURL  string
	natsCtr  testcontainers.Container
	natsErr  error
)

// sharedNATS starts one server for the whole suite. A container per spec would
// cost more than the suite itself.
func sharedNATS() (string, error) {
	natsOnce.Do(func() {
		ctx := context.Background()
		natsCtr, natsErr = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "nats:2.10-alpine",
				ExposedPorts: []string{"4222/tcp"},
				WaitingFor:   wait.ForListeningPort("4222/tcp"),
			},
			Started: true,
		})
		if natsErr != nil {
			return
		}
		host, err := natsCtr.Host(ctx)
		if err != nil {
			natsErr = err
			return
		}
		port, err := natsCtr.MappedPort(ctx, "4222/tcp")
		if err != nil {
			natsErr = err
			return
		}
		natsURL = fmt.Sprintf("nats://%s:%s", host, port.Port())
	})
	return natsURL, natsErr
}

var _ = AfterSuite(func() {
	if natsCtr != nil {
		_ = natsCtr.Terminate(context.Background())
	}
})

var _ = Describe("NATS client", func() {
	messagingtest.RunBroadcasterConformance(func() (messaging.Broadcaster, func()) {
		url, err := sharedNATS()
		if err != nil {
			Skip("testcontainers requires Docker: " + err.Error())
		}
		c, err := messaging.New(url)
		if err != nil {
			Fail("connecting to the test NATS server: " + err.Error())
		}
		return c, c.Close
	})
})
