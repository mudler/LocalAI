package gallery

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
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`

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
