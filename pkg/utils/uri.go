package utils

import (
	"io/ioutil"
	"net/http"
	"strings"
)

func GetURI(url string, f func(i []byte) error) error {
	if strings.HasPrefix(url, "file://") {
		rawURL := strings.TrimPrefix(url, "file://")
		// Read the response body
		body, err := ioutil.ReadFile(rawURL)
		if err != nil {
			return err
		}

		// Unmarshal YAML data into a struct
		return f(body)
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
	return f(body)
}
