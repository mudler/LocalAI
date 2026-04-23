package utils

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

var base64DownloadClient http.Client = http.Client{
	Timeout: 30 * time.Second,
}

// Match `data:<mime>[;param=value...];base64,` — browser-produced data URIs
// often carry codec/charset params between the mime type and `;base64,`
// (e.g. MediaRecorder's `data:audio/webm;codecs=opus;base64,...`). The old
// `([^;]+)` form only tolerated exactly one segment, so anything with
// extra params failed the strip and tripped the downstream base64 decoder
// on the `data:` literal.
var dataURIPattern = regexp.MustCompile(`^data:[^,]+?;base64,`)

// GetContentURIAsBase64 checks if the string is an URL, if it's an URL downloads the content in memory encodes it in base64 and returns the base64 string, otherwise returns the string by stripping base64 data headers
func GetContentURIAsBase64(s string) (string, error) {
	if strings.HasPrefix(s, "http") || strings.HasPrefix(s, "https") {
		if err := ValidateExternalURL(s); err != nil {
			return "", fmt.Errorf("URL validation failed: %w", err)
		}

		// download the image
		resp, err := base64DownloadClient.Get(s)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		// read the image data into memory
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		// encode the image data in base64
		encoded := base64.StdEncoding.EncodeToString(data)

		// return the base64 string
		return encoded, nil
	}

	// Match any data URI prefix pattern
	if match := dataURIPattern.FindString(s); match != "" {
		xlog.Debug("Found data URI prefix", "prefix", match)
		return strings.Replace(s, match, "", 1), nil
	}

	return "", fmt.Errorf("not valid base64 data type string")
}
