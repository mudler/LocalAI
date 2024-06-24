package utils

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var base64DownloadClient http.Client = http.Client{
	Timeout: 30 * time.Second,
}

// this function check if the string is an URL, if it's an URL downloads the image in memory
// encodes it in base64 and returns the base64 string

// This may look weird down in pkg/utils while it is currently only used in core/config
//
//	but I believe it may be useful for MQTT as well in the near future, so I'm
//	extracting it while I'm thinking of it.
func GetImageURLAsBase64(s string) (string, error) {
	if strings.HasPrefix(s, "http") {
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

	// if the string instead is prefixed with "data:image/...;base64,", drop it
	dropPrefix := []string{"data:image/jpeg;base64,", "data:image/png;base64,"}
	for _, prefix := range dropPrefix {
		if strings.HasPrefix(s, prefix) {
			return strings.ReplaceAll(s, prefix, ""), nil
		}
	}
	return "", fmt.Errorf("not valid string")
}
