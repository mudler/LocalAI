package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/sashabaranov/go-openai"
)

var pool *dockertest.Pool
var resource *dockertest.Resource
var client *openai.Client

var containerImage = os.Getenv("LOCALAI_IMAGE")
var containerImageTag = os.Getenv("LOCALAI_IMAGE_TAG")
var modelsDir = os.Getenv("LOCALAI_MODELS_DIR")
var apiPort = os.Getenv("LOCALAI_API_PORT")
var apiEndpoint = os.Getenv("LOCALAI_API_ENDPOINT")
var apiKey = os.Getenv("LOCALAI_API_KEY")

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI E2E test suite")
}

var _ = BeforeSuite(func() {

	if apiPort == "" {
		apiPort = "8080"
	}

	var defaultConfig openai.ClientConfig
	if apiEndpoint == "" {
		startDockerImage("")
		defaultConfig = openai.DefaultConfig(apiKey)
		apiEndpoint = "http://localhost:" + apiPort + "/v1" // So that other tests can reference this value safely.
		defaultConfig.BaseURL = apiEndpoint
	} else {
		fmt.Println("Default ", apiEndpoint)
		defaultConfig = openai.DefaultConfig(apiKey)
		defaultConfig.BaseURL = apiEndpoint
	}

	// Wait for API to be ready
	client = openai.NewClientWithConfig(defaultConfig)

	Eventually(func() error {
		_, err := client.ListModels(context.TODO())
		return err
	}, "20m").ShouldNot(HaveOccurred())
})

var _ = AfterSuite(func() {

	// if the suite failed, logs will be printed
	// to the console
	if CurrentGinkgoTestDescription().Failed {
		if resource != nil {
			logs := bytes.NewBufferString("")
			err := pool.Client.Logs(docker.LogsOptions{
				Container:    resource.Container.ID,
				OutputStream: logs,
				ErrorStream:  logs,
				Stdout:       true,
				Stderr:       true,
				Timestamps:   true,
			})
			if err != nil {
				fmt.Println("Could not take logs for failed suite", err.Error())
			}
			fmt.Println("Suite failed, printing logs")
			fmt.Println(logs.String())

			c, err := pool.Client.InspectContainer(resource.Container.ID)
			if err != nil {
				fmt.Println("Could not inspect container", err.Error())
			}
			fmt.Println("Container state")
			fmt.Println("Running:", c.State.Running)
			fmt.Println("ExitCode:", c.State.ExitCode)
			fmt.Println("Error:", c.State.Error)
		}
	}

	if resource != nil {
		Expect(pool.Purge(resource)).To(Succeed())
	}

	//dat, err := os.ReadFile(resource.Container.LogPath)
	//Expect(err).To(Not(HaveOccurred()))
	//Expect(string(dat)).To(ContainSubstring("GRPC Service Ready"))
	//fmt.Println(string(dat))
})

var _ = AfterEach(func() {
	//Expect(dbClient.Clear()).To(Succeed())
})

func startDockerImage(endpoint string) {
	p, err := dockertest.NewPool(endpoint)
	Expect(err).To(Not(HaveOccurred()))
	Expect(p.Client.Ping()).To(Succeed())

	pool = p

	// get cwd
	cwd, err := os.Getwd()
	Expect(err).To(Not(HaveOccurred()))
	md := cwd + "/models"

	if modelsDir != "" {
		md = modelsDir
	}

	proc := runtime.NumCPU()
	options := &dockertest.RunOptions{
		Repository: containerImage,
		Tag:        containerImageTag,
		//	Cmd:        []string{"server", "/data"},
		PortBindings: map[docker.Port][]docker.PortBinding{
			"8080/tcp": []docker.PortBinding{{HostPort: apiPort}},
		},
		Env:    []string{"MODELS_PATH=/models", "DEBUG=true", "THREADS=" + fmt.Sprint(proc)},
		Mounts: []string{md + ":/models"},
	}

	r, err := pool.RunWithOptions(options)
	Expect(err).To(Not(HaveOccurred()))

	resource = r
}
