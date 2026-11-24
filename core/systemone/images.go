// SPDX-License-Identifier: MIT
package systemone

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

// Limits are shared by internal and public decision callers, not global HTTP limits.
const (
	MaxImages            = 8
	MaxImageDecodedBytes = 8 << 20
	MaxImageEncodedBytes = 12 << 20
	MaxImageBodyBytes    = 16 << 20
	MaxImageDimension    = 4096
	MaxImagePixels       = 16_000_000
	MaxResponseBytes     = 64 << 10
)

const InputTooLarge ErrorKind = "input_too_large"

func imageError(kind ErrorKind, message string) error {
	return &ValidationError{kind, fmt.Errorf("%s", message)}
}

// CollectImages preserves URL bytes and order (top-level first, then chat parts).
// Only message content has image semantics; arbitrary JSON domain state does not.
// Collection never fetches or decodes data. ValidateImages must precede inference.
func CollectImages(req *schema.SystemOneRequest) ([]string, error) {
	if req == nil {
		return nil, imageError(InvalidRequest, "request is required")
	}
	var images []string
	if len(req.Images) > 0 {
		if err := json.Unmarshal(req.Images, &images); err != nil {
			return nil, imageError(InvalidRequest, "images must be an array of data URLs")
		}
	}
	var state any
	if err := json.Unmarshal(req.State, &state); err != nil {
		return nil, imageError(InvalidRequest, "state is not valid JSON")
	}
	if wrapped, ok := state.(map[string]any); ok {
		state = wrapped["messages"]
	}
	messages, _ := state.([]any)
	for _, message := range messages {
		msg, _ := message.(map[string]any)
		content, _ := msg["content"].([]any)
		for _, value := range content {
			part, _ := value.(map[string]any)
			switch part["type"] {
			case "image_url":
				value := part["image_url"]
				if obj, ok := value.(map[string]any); ok {
					value = obj["url"]
				}
				url, ok := value.(string)
				if !ok {
					return nil, imageError(InvalidRequest, "image_url must contain a URL")
				}
				images = append(images, url)
			case "image":
				source, _ := part["source"].(map[string]any)
				mime, mok := source["media_type"].(string)
				data, dok := source["data"].(string)
				if source["type"] != "base64" || !mok || !dok {
					return nil, imageError(InvalidRequest, "image source must contain base64 data and media_type")
				}
				images = append(images, "data:"+mime+";base64,"+data)
			}
		}
	}
	return images, nil
}

// ValidateImages bounds encoded and decoded allocation before reading headers.
// DecodeConfig reads dimensions without allocating a pixel buffer. Native decoders
// must enforce the same bounds independently for direct RPC callers.
func ValidateImages(images []string) error {
	if len(images) > MaxImages {
		return imageError(InputTooLarge, "too many decision images")
	}
	encoded := 0
	for _, url := range images {
		if len(url) > MaxImageEncodedBytes-encoded {
			return imageError(InputTooLarge, "decision images exceed encoded aggregate limit")
		}
		encoded += len(url)
	}
	decoded, pixels := 0, int64(0)
	for _, url := range images {
		header, data, ok := strings.Cut(url, ",")
		format := ""
		switch header {
		case "data:image/png;base64":
			format = "png"
		case "data:image/jpeg;base64":
			format = "jpeg"
		}
		if !ok || format == "" || data == "" || len(data)%4 != 0 {
			return imageError(InvalidRequest, "images must be PNG or JPEG base64 data URLs")
		}
		// Go's base64 decoder tolerates CR/LF even in Strict mode; the wire contract does not.
		for i := 0; i < len(data); i++ {
			c := data[i]
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
				return imageError(InvalidRequest, "invalid base64 image data")
			}
		}
		size := base64.StdEncoding.DecodedLen(len(data))
		if strings.HasSuffix(data, "==") {
			size -= 2
		} else if strings.HasSuffix(data, "=") {
			size--
		}
		if size > MaxImageDecodedBytes-decoded {
			return imageError(InputTooLarge, "decision images exceed decoded aggregate limit")
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(data)
		if err != nil {
			return imageError(InvalidRequest, "invalid base64 image data")
		}
		decoded += len(raw)
		cfg, actual, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || actual != format || cfg.Width <= 0 || cfg.Height <= 0 {
			return imageError(InvalidRequest, "invalid image header or MIME mismatch")
		}
		if cfg.Width > MaxImageDimension || cfg.Height > MaxImageDimension {
			return imageError(InputTooLarge, "decision image dimensions exceed limit")
		}
		pixels += int64(cfg.Width) * int64(cfg.Height)
		if pixels > MaxImagePixels {
			return imageError(InputTooLarge, "decision images exceed aggregate pixel limit")
		}
		// Only allocate pixels after header bounds. DecodeConfig alone accepts
		// truncated streams and corrupt pixel payloads.
		if _, _, err := image.Decode(bytes.NewReader(raw)); err != nil {
			return imageError(InvalidRequest, "invalid image pixel data")
		}
	}
	return nil
}

// RequestBodyLimit only selects a budget; it does not replace validation.
func RequestBodyLimit(req *schema.SystemOneRequest) (int, error) {
	images, err := CollectImages(req)
	if err != nil {
		return 0, err
	}
	if len(images) > 0 {
		return MaxImageBodyBytes, nil
	}
	return MaxBodyBytes, nil
}
