package gallery

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v2"
)

// endpoints

type GalleryModel struct {
	URL             string                 `json:"url" yaml:"url"`
	Name            string                 `json:"name" yaml:"name"`
	Overrides       map[string]interface{} `json:"overrides" yaml:"overrides"`
	AdditionalFiles []File                 `json:"files" yaml:"files"`
	Gallery         Gallery                `json:"gallery" yaml:"gallery"`
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

	if strings.HasPrefix(url, "file://") {
		rawURL := strings.TrimPrefix(url, "file://")
		// Read the response body
		body, err := ioutil.ReadFile(rawURL)
		if err != nil {
			return err
		}

		// Unmarshal YAML data into a struct
		return yaml.Unmarshal(body, i)
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

	// Unmarshal YAML data into a struct
	return yaml.Unmarshal(body, i)
}
