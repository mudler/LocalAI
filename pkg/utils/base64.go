package utils

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

var base64DownloadClient http.Client = http.Client{
	Timeout: 30 * time.Second,
}

var dataURIPattern = regexp.MustCompile(`^data:([^;]+);base64,`)

// GetContentURIAsBase64 checks if the string is an URL, if it's an URL downloads the content in memory encodes it in base64 and returns the base64 string, otherwise returns the string by stripping base64 data headers
func GetContentURIAsBase64(s string) (string, error) {
	if strings.HasPrefix(s, "http") || strings.HasPrefix(s, "https") {
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
		log.Debug().Msgf("Found data URI prefix: %s", match)
		return strings.Replace(s, match, "", 1), nil
	}

	return "", fmt.Errorf("not valid base64 data type string")
}
