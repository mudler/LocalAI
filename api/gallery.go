package api

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
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

				url, err := op.req.DecodeURL()
				if err != nil {
					updateError(err)
					continue
				}

				// Send a GET request to the URL
				response, err := http.Get(url)
				if err != nil {
					updateError(err)
					continue
				}
				defer response.Body.Close()

				// Read the response body
				body, err := ioutil.ReadAll(response.Body)
				if err != nil {
					updateError(err)
					continue
				}

				// Unmarshal YAML data into a Config struct
				var config gallery.Config
				err = yaml.Unmarshal(body, &config)
				if err != nil {
					updateError(fmt.Errorf("failed to unmarshal YAML: %v", err))
					continue
				}

				config.Files = append(config.Files, op.req.AdditionalFiles...)

				if err := gallery.Apply(g.modelPath, op.req.Name, &config); err != nil {
					updateError(err)
					continue
				}

				// Reload models
				if err := cm.LoadConfigs(g.modelPath); err != nil {
					updateError(err)
					continue
				}

				g.updatestatus(op.id, &galleryOpStatus{Processed: true, Message: "completed"})
			}
		}
	}()
}

// endpoints

type ApplyGalleryModelRequest struct {
	URL             string         `json:"url"`
	Name            string         `json:"name"`
	AdditionalFiles []gallery.File `json:"files"`
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

		status := g.getstatus(c.Params("uid"))
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
			ID        string `json:"uid"`
			StatusURL string `json:"status"`
		}{ID: uuid.String(), StatusURL: c.BaseURL() + "/models/jobs/" + uuid.String()})
	}
}
