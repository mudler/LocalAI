package carrier_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	. "github.com/onsi/ginkgo/v2"
)

var (
	testNATSOnce sync.Once
	testNATSURL  string
	testNATSCtr  testcontainers.Container
	testNATSErr  error
)

// startTestNATS starts one NATS server for the process.
func startTestNATS() (string, error) {
	testNATSOnce.Do(func() {
		ctx := context.Background()
		var ctr testcontainers.Container
		ctr, testNATSErr = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "nats:2.10-alpine",
				ExposedPorts: []string{"4222/tcp"},
				WaitingFor:   wait.ForListeningPort("4222/tcp"),
			},
			Started: true,
		})
		if testNATSErr != nil {
			return
		}
		testNATSCtr = ctr
		host, err := ctr.Host(ctx)
		if err != nil {
			testNATSErr = err
			return
		}
		port, err := ctr.MappedPort(ctx, "4222/tcp")
		if err != nil {
			testNATSErr = err
			return
		}
		testNATSURL = fmt.Sprintf("nats://%s:%s", host, port.Port())
	})
	return testNATSURL, testNATSErr
}

var _ = AfterSuite(func() {
	if testNATSCtr != nil {
		_ = testNATSCtr.Terminate(context.Background())
	}
})

// testHandover hands a worker the address of the test NATS server at its
// registration, as the frontend does with the address of the cluster.
type testHandover struct{ url string }

func (r testHandover) WorkerURL(context.Context) (string, error) { return r.url, nil }
func (r testHandover) CAPEM() (string, error)                    { return "", nil }
func (r testHandover) ClientTLS() bool                           { return false }
