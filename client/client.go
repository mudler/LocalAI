package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type Prediction struct {
	Prediction string `json:"prediction"`
}

type Client struct {
	baseURL  string
	client   *http.Client
	endpoint string
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:  baseURL,
		client:   &http.Client{},
		endpoint: "/predict",
	}
}

type InputData struct {
	Text        string  `json:"text"`
	TopP        float64 `json:"topP,omitempty"`
	TopK        int     `json:"topK,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Tokens      int     `json:"tokens,omitempty"`
}

func (c *Client) Predict(text string, opts ...InputOption) (string, error) {
	input := NewInputData(opts...)
	input.Text = text

	// encode input data to JSON format
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return "", err
	}

	// create HTTP request
	url := c.baseURL + c.endpoint
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(inputBytes))
	if err != nil {
		return "", err
	}

	// set request headers
	req.Header.Set("Content-Type", "application/json")

	// send request and get response
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	// decode response body to Prediction struct
	var prediction Prediction
	err = json.NewDecoder(resp.Body).Decode(&prediction)
	if err != nil {
		return "", err
	}

	return prediction.Prediction, nil
}
