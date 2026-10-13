package messaging_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
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
	messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
		url, err := sharedNATS()
		if err != nil {
			// This is the only spec that runs the rules against a real carrier.
			// A CI runner that lost Docker must go red, not quietly report a
			// pass with the check skipped. Local runs without Docker still skip,
			// and so does macOS CI, whose runners have no Docker by design.
			if os.Getenv("CI") != "" && runtime.GOOS != "darwin" {
				Fail("testcontainers requires Docker and CI is set: " + err.Error())
			}
			Skip("testcontainers requires Docker: " + err.Error())
		}
		c, err := messaging.New(url)
		if err != nil {
			Fail("connecting to the test NATS server: " + err.Error())
		}
		peer, err := messaging.New(url)
		if err != nil {
			c.Close()
			Fail("connecting to the test NATS server: " + err.Error())
		}
		return messagingtest.Carrier{
			Bus: c, Peer: peer,
			Cleanup:            func() { c.Close(); peer.Close() },
			ServesControlRoots: true,
		}
	})
})

// The queue groups of NATS run the shared work queue suite. Each consumer has a
// client of its own, as each worker does, so that they compete for the queue.
var _ = Describe("NATS work queue", func() {
	messagingtest.RunWorkQueueConformance(func() messagingtest.WorkQueueRig {
		url, err := sharedNATS()
		if err != nil {
			if os.Getenv("CI") != "" && runtime.GOOS != "darwin" {
				Fail("testcontainers requires Docker and CI is set: " + err.Error())
			}
			Skip("testcontainers requires Docker: " + err.Error())
		}
		producer, err := messaging.New(url)
		if err != nil {
			Fail("connecting to the test NATS server: " + err.Error())
		}
		var mu sync.Mutex
		clients := []*messaging.Client{producer}
		return messagingtest.WorkQueueRig{
			Queue: messaging.NewNATSWorkQueue(producer),
			NewConsumer: func() messaging.WorkConsumer {
				c, err := messaging.New(url)
				if err != nil {
					Fail("connecting to the test NATS server: " + err.Error())
				}
				mu.Lock()
				clients = append(clients, c)
				mu.Unlock()
				return messaging.NewNATSWorkConsumer(c)
			},
			Cleanup: func() {
				mu.Lock()
				defer mu.Unlock()
				for _, c := range clients {
					c.Close()
				}
			},
		}
	})
})
