package gallery

import "github.com/mudler/LocalAI/core/config"

type Metadata struct {
	URL         string   `json:"url,omitempty" yaml:"url,omitempty"`
	Name        string   `json:"name,omitempty" yaml:"name,omitempty"`
	Description string   `json:"description,omitempty"  yaml:"description,omitempty"`
	License     string   `json:"license,omitempty"  yaml:"license,omitempty"`
	URLs        []string `json:"urls,omitempty" yaml:"urls,omitempty"`
	Icon        string   `json:"icon,omitempty" yaml:"icon,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	// AdditionalFiles are used to add additional files to the model
	AdditionalFiles []File `json:"files,omitempty" yaml:"files,omitempty"`
	// Gallery is a reference to the gallery which contains the model
	Gallery config.Gallery `json:"gallery,omitempty" yaml:"gallery,omitempty"`
	// Installed is used to indicate if the model is installed or not
	Installed bool `json:"installed,omitempty" yaml:"installed,omitempty"`
}
