package localai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

// Public decision input has its own limits; the internal router's serialized
// request budget is independent. No global endpoint limits are raised.
const systemOneMaxImageBytes = 32 << 10 // aggregate encoded data URL bytes
const systemOneMaxImages = 8

var errSystemOneImagesUnsupported = errors.New("image input is not supported by the NER decision path")
var errSystemOneImagesTooLarge = errors.New("decision images exceed 32 KiB encoded aggregate limit")

func systemOneInputStatus(err error) int {
	if errors.Is(err, errSystemOneImagesUnsupported) {
		return http.StatusNotImplemented
	}
	if errors.Is(err, errSystemOneImagesTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// systemOneImages recognizes the same top-level URLs and chat image_url parts
// as native decisions. It never flattens or rewrites the caller's state.
func systemOneImages(req *schema.SystemOneRequest) ([]string, error) {
	var images []string
	if len(req.Images) > 0 && string(req.Images) != "null" {
		if err := json.Unmarshal(req.Images, &images); err != nil {
			return nil, fmt.Errorf("images must be an array of data URLs")
		}
	}
	var state any
	if err := json.Unmarshal(req.State, &state); err != nil {
		return nil, err
	}

	// Only chat content parts have image semantics. Arbitrary objects in state
	// remain domain data, even if they happen to contain a type field.
	if wrapped, ok := state.(map[string]any); ok {
		state = wrapped["messages"]
	}
	messages, _ := state.([]any)
	for _, message := range messages {
		msg, _ := message.(map[string]any)
		content, _ := msg["content"].([]any)
		for _, value := range content {
			part, _ := value.(map[string]any)
			if part["type"] != "image_url" {
				continue
			}
			image := part["image_url"]
			if object, ok := image.(map[string]any); ok {
				image = object["url"]
			}
			url, ok := image.(string)
			if !ok {
				return nil, fmt.Errorf("image_url must contain a URL")
			}
			images = append(images, url)
		}
	}

	return images, nil
}

func validateSystemOneImages(req *schema.SystemOneRequest) error {
	images, err := systemOneImages(req)
	if err != nil {
		return err
	}
	if len(images) > systemOneMaxImages {
		return fmt.Errorf("at most %d decision images are allowed", systemOneMaxImages)
	}
	total := 0
	for _, image := range images {
		total += len(image)
		if total > systemOneMaxImageBytes {
			return errSystemOneImagesTooLarge
		}
		header, data, ok := strings.Cut(image, ",")
		if !ok || !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
			return fmt.Errorf("images must be base64 image data URLs")
		}
		if _, err := base64.StdEncoding.Strict().DecodeString(data); err != nil {
			return fmt.Errorf("invalid base64 image data")
		}
	}
	return nil
}
