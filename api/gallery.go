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
	"time"

	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type galleryOp struct {
	req ApplyGalleryModelRequest
	id  string
}

type galleryOpStatus struct {
	Error              error   `json:"error"`
	Processed          bool    `json:"processed"`
	Message            string  `json:"message"`
	Progress           float64 `json:"progress"`
	TotalFileSize      string  `json:"file_size"`
	DownloadedFileSize string  `json:"downloaded_size"`
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

func applyGallery(modelPath string, req ApplyGalleryModelRequest, cm *ConfigMerger, downloadStatus func(string, string, string, float64)) error {
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

	if err := gallery.Apply(modelPath, req.Name, &config, req.Overrides, downloadStatus); err != nil {
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
				g.updatestatus(op.id, &galleryOpStatus{Message: "processing", Progress: 0})

				updateError := func(e error) {
					g.updatestatus(op.id, &galleryOpStatus{Error: e, Processed: true})
				}

				if err := applyGallery(g.modelPath, op.req, cm, func(fileName string, current string, total string, percentage float64) {
					g.updatestatus(op.id, &galleryOpStatus{Message: "processing", Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
					displayDownload(fileName, current, total, percentage)
				}); err != nil {
					updateError(err)
					continue
				}

				g.updatestatus(op.id, &galleryOpStatus{Processed: true, Message: "completed", Progress: 100})
			}
		}
	}()
}

var lastProgress time.Time = time.Now()
var startTime time.Time = time.Now()

func displayDownload(fileName string, current string, total string, percentage float64) {
	currentTime := time.Now()

	if currentTime.Sub(lastProgress) >= 5*time.Second {

		lastProgress = currentTime

		// calculate ETA based on percentage and elapsed time
		var eta time.Duration
		if percentage > 0 {
			elapsed := currentTime.Sub(startTime)
			eta = time.Duration(float64(elapsed)*(100/percentage) - float64(elapsed))
		}

		if total != "" {
			log.Debug().Msgf("Downloading %s: %s/%s (%.2f%%) ETA: %s", fileName, current, total, percentage, eta)
		} else {
			log.Debug().Msgf("Downloading: %s", current)
		}
	}
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
		if err := applyGallery(modelPath, r, cm, displayDownload); err != nil {
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
		if err := applyGallery(modelPath, r, cm, displayDownload); err != nil {
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
