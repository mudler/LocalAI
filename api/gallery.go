package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type galleryOp struct {
	req ApplyGalleryModelRequest
	id  string
}

type galleryOpStatus struct {
	Error     error  `json:"error"`
	Processed bool   `json:"processed"`
	Message   string `json:"message"`
}

type galleryApplier struct {
	modelPath string
	sync.Mutex
	C        chan galleryOp
	statuses map[string]*galleryOpStatus
}

func newGalleryApplier(modelPath string) *galleryApplier {
	return &galleryApplier{
		modelPath: modelPath,
		C:         make(chan galleryOp),
		statuses:  make(map[string]*galleryOpStatus),
	}
}

func applyGallery(modelPath string, req ApplyGalleryModelRequest, cm *ConfigMerger) error {
	url, err := req.DecodeURL()
	if err != nil {
		return err
	}

	// Send a GET request to the URL
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	// Unmarshal YAML data into a Config struct
	var config gallery.Config
	err = yaml.Unmarshal(body, &config)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	if err := gallery.Apply(modelPath, req.Name, &config, req.Overrides); err != nil {
		return err
	}

	// Reload models
	return cm.LoadConfigs(modelPath)
}

func (g *galleryApplier) updatestatus(s string, op *galleryOpStatus) {
	g.Lock()
	defer g.Unlock()
	g.statuses[s] = op
}

func (g *galleryApplier) getstatus(s string) *galleryOpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses[s]
}

func (g *galleryApplier) start(c context.Context, cm *ConfigMerger) {
	go func() {
		for {
			select {
			case <-c.Done():
				return
			case op := <-g.C:
				g.updatestatus(op.id, &galleryOpStatus{Message: "processing"})

				updateError := func(e error) {
					g.updatestatus(op.id, &galleryOpStatus{Error: e, Processed: true})
				}

				if err := applyGallery(g.modelPath, op.req, cm); err != nil {
					updateError(err)
					continue
				}

				g.updatestatus(op.id, &galleryOpStatus{Processed: true, Message: "completed"})
			}
		}
	}()
}

func ApplyGalleryFromFile(modelPath, s string, cm *ConfigMerger) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []ApplyGalleryModelRequest
	err = json.Unmarshal(dat, &requests)
	if err != nil {
		return err
	}

	for _, r := range requests {
		if err := applyGallery(modelPath, r, cm); err != nil {
			return err
		}
	}

	return nil
}
func ApplyGalleryFromString(modelPath, s string, cm *ConfigMerger) error {
	var requests []ApplyGalleryModelRequest
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	for _, r := range requests {
		if err := applyGallery(modelPath, r, cm); err != nil {
			return err
		}
	}

	return nil
}

// endpoints

type ApplyGalleryModelRequest struct {
	URL             string                 `json:"url"`
	Name            string                 `json:"name"`
	Overrides       map[string]interface{} `json:"overrides"`
	AdditionalFiles []gallery.File         `json:"files"`
}

const (
	githubURI = "github:"
)

func (request ApplyGalleryModelRequest) DecodeURL() (string, error) {
	input := request.URL
	var rawURL string

	if strings.HasPrefix(input, githubURI) {
		parts := strings.Split(input, ":")
		repoParts := strings.Split(parts[1], "@")
		branch := "main"

		if len(repoParts) > 1 {
			branch = repoParts[1]
		}

		repoPath := strings.Split(repoParts[0], "/")
		org := repoPath[0]
		project := repoPath[1]
		projectPath := strings.Join(repoPath[2:], "/")

		rawURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, project, branch, projectPath)
	} else if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		// Handle regular URLs
		u, err := url.Parse(input)
		if err != nil {
			return "", fmt.Errorf("invalid URL: %w", err)
		}
		rawURL = u.String()
	} else {
		return "", fmt.Errorf("invalid URL format")
	}

	return rawURL, nil
}

func getOpStatus(g *galleryApplier) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		status := g.getstatus(c.Params("uuid"))
		if status == nil {
			return fmt.Errorf("could not find any status for ID")
		}

		return c.JSON(status)
	}
}

func applyModelGallery(modelPath string, cm *ConfigMerger, g chan galleryOp) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(ApplyGalleryModelRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}
		g <- galleryOp{
			req: *input,
			id:  uuid.String(),
		}
		return c.JSON(struct {
			ID        string `json:"uuid"`
			StatusURL string `json:"status"`
		}{ID: uuid.String(), StatusURL: c.BaseURL() + "/models/jobs/" + uuid.String()})
	}
}
