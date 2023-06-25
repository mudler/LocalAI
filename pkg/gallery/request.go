package gallery

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-skynet/LocalAI/pkg/utils"
	"gopkg.in/yaml.v2"
)

// GalleryModel is the struct used to represent a model in the gallery returned by the endpoint.
// It is used to install the model by resolving the URL and downloading the files.
// The other fields are used to override the configuration of the model.
type GalleryModel struct {
	URL         string   `json:"url,omitempty" yaml:"url,omitempty"`
	Name        string   `json:"name,omitempty" yaml:"name,omitempty"`
	Description string   `json:"description,omitempty"  yaml:"description,omitempty"`
	License     string   `json:"license,omitempty"  yaml:"license,omitempty"`
	URLs        []string `json:"urls,omitempty" yaml:"urls,omitempty"`
	Icon        string   `json:"icon,omitempty" yaml:"icon,omitempty"`

	// Overrides are used to override the configuration of the model
	Overrides map[string]interface{} `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	// AdditionalFiles are used to add additional files to the model
	AdditionalFiles []File `json:"files,omitempty" yaml:"files,omitempty"`
	// Gallery is a reference to the gallery which contains the model
	Gallery Gallery `json:"gallery,omitempty" yaml:"gallery,omitempty"`
	// Installed is used to indicate if the model is installed or not
	Installed bool `json:"installed,omitempty" yaml:"installed,omitempty"`
}

const (
	githubURI = "github:"
)

func (request GalleryModel) DecodeURL() (string, error) {
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
		// check if it's a file path
	} else if strings.HasPrefix(input, "file://") {
		return input, nil
	} else {

		return "", fmt.Errorf("invalid URL format: %s", input)
	}

	return rawURL, nil
}

// Get fetches a model from a URL and unmarshals it into a struct
func (request GalleryModel) Get(i interface{}) error {
	url, err := request.DecodeURL()
	if err != nil {
		return err
	}

	return utils.GetURI(url, func(d []byte) error {
		return yaml.Unmarshal(d, i)
	})
}
