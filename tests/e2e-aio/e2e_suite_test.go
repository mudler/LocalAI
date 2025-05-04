package e2e_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/docker/go-connections/nat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var container testcontainers.Container
var client *openai.Client

var containerImage = os.Getenv("LOCALAI_IMAGE")
var containerImageTag = os.Getenv("LOCALAI_IMAGE_TAG")
var modelsDir = os.Getenv("LOCALAI_MODELS_DIR")
var apiEndpoint = os.Getenv("LOCALAI_API_ENDPOINT")
var apiKey = os.Getenv("LOCALAI_API_KEY")

const (
	defaultApiPort = "8080"
)

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI E2E test suite")
}

var _ = BeforeSuite(func() {

	var defaultConfig openai.ClientConfig
	if apiEndpoint == "" {
		startDockerImage()
		apiPort, err := container.MappedPort(context.Background(), nat.Port(defaultApiPort))
		Expect(err).To(Not(HaveOccurred()))

		defaultConfig = openai.DefaultConfig(apiKey)
		apiEndpoint = "http://localhost:" + apiPort.Port() + "/v1" // So that other tests can reference this value safely.
		defaultConfig.BaseURL = apiEndpoint
	} else {
		GinkgoWriter.Printf("docker apiEndpoint set from env: %q\n", apiEndpoint)
		defaultConfig = openai.DefaultConfig(apiKey)
		defaultConfig.BaseURL = apiEndpoint
	}

	// Wait for API to be ready
	client = openai.NewClientWithConfig(defaultConfig)

	Eventually(func() error {
		_, err := client.ListModels(context.TODO())
		return err
	}, "50m").ShouldNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	if container != nil {
		Expect(container.Terminate(context.Background())).To(Succeed())
	}
})

var _ = AfterEach(func() {
	// Add any cleanup needed after each test
})

type logConsumer struct {
}

func (l *logConsumer) Accept(log testcontainers.Log) {
	GinkgoWriter.Write([]byte(log.Content))
}

func startDockerImage() {
	// get cwd
	cwd, err := os.Getwd()
	Expect(err).To(Not(HaveOccurred()))
	md := cwd + "/models"

	if modelsDir != "" {
		md = modelsDir
	}

	proc := runtime.NumCPU()

	req := testcontainers.ContainerRequest{

		Image:        fmt.Sprintf("%s:%s", containerImage, containerImageTag),
		ExposedPorts: []string{defaultApiPort},
		LogConsumerCfg: &testcontainers.LogConsumerConfig{
			Consumers: []testcontainers.LogConsumer{
				&logConsumer{},
			},
		},
		Env: map[string]string{
			"MODELS_PATH":                   "/models",
			"DEBUG":                         "true",
			"THREADS":                       fmt.Sprint(proc),
			"LOCALAI_SINGLE_ACTIVE_BACKEND": "true",
		},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      md,
				ContainerFilePath: "/models",
				FileMode:          0o755,
			},
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort(nat.Port(defaultApiPort)),
		//	wait.ForHTTP("/v1/models").WithPort(nat.Port(apiPort)).WithStartupTimeout(50*time.Minute),
		),
	}

	GinkgoWriter.Printf("Launching Docker Container %s:%s\n", containerImage, containerImageTag)

	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	Expect(err).To(Not(HaveOccurred()))

	container = c
}
