package gallery

import (
	"fmt"
	"net/url"
	"strings"
)

// endpoints

type GalleryModel struct {
	URL             string                 `json:"url" yaml:"url"`
	Name            string                 `json:"name" yaml:"name"`
	Overrides       map[string]interface{} `json:"overrides" yaml:"overrides"`
	AdditionalFiles []File                 `json:"files" yaml:"files"`
	Gallery         *Gallery               `json:"gallery" yaml:"gallery"`
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
	} else {
		return "", fmt.Errorf("invalid URL format")
	}

	return rawURL, nil
}
