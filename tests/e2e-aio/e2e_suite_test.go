package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
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
		startDockerImage()
		defaultConfig = openai.DefaultConfig(apiKey)
		apiEndpoint = "http://localhost:" + apiPort + "/v1" // So that other tests can reference this value safely.
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
	}, "20m").ShouldNot(HaveOccurred())
})

var _ = AfterSuite(func() {
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

func ShutdownModel(modelName string) func() {
	return func() {
		req := schema.BackendMonitorRequest{
			BasicModelRequest: schema.BasicModelRequest{
				Model: modelName,
			},
		}
		serialized, err := json.Marshal(req)
		Expect(err).To(BeNil())
		Expect(serialized).ToNot(BeNil())

		// r1, err := http.Post(apiEndpoint+"/backend/monitor", "application/json", bytes.NewReader(serialized))
		// Expect(err).To(BeNil())
		// Expect(r1).ToNot(BeNil())
		// b1, err := io.ReadAll(r1.Body)
		// GinkgoWriter.Printf("TEMPORARY MONITOR RESPONSE: %q\n", b1)

		resp, err := http.Post(apiEndpoint+"/backend/shutdown", "application/json", bytes.NewReader(serialized))
		Expect(err).To(BeNil())
		Expect(resp).ToNot(BeNil())
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		// if a test fails to load the model, we will recieve a 500 error when we try to shut it down.
		// We therefore handle both cases seperately
		if resp.StatusCode == 500 {
			body, err := io.ReadAll(resp.Body)
			Expect(err).To(BeNil())
			Expect(body).To(ContainSubstring(fmt.Sprintf("%s not found", modelName)), fmt.Sprintf("unexpected response during shutdown: %q", body))
		} else {
			Expect(resp.StatusCode).To(Equal(200), fmt.Sprintf("failed to shutdown model: %s, response: %+v", body, resp))
		}
	}
}

func startDockerImage() {
	p, err := dockertest.NewPool("")
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

	GinkgoWriter.Printf("Launching Docker Container %q\n%+v\n", containerImageTag, options)
	r, err := pool.RunWithOptions(options)
	Expect(err).To(Not(HaveOccurred()))

	resource = r
}
