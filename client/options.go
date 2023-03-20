package client

import "net/http"

type ClientOption func(c *Client)

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.client = httpClient
	}
}

func WithEndpoint(endpoint string) ClientOption {
	return func(c *Client) {
		c.endpoint = endpoint
	}
}

type InputOption func(d *InputData)

func NewInputData(opts ...InputOption) *InputData {
	data := &InputData{}
	for _, opt := range opts {
		opt(data)
	}
	return data
}

func WithTopP(topP float64) InputOption {
	return func(d *InputData) {
		d.TopP = topP
	}
}

func WithTopK(topK int) InputOption {
	return func(d *InputData) {
		d.TopK = topK
	}
}

func WithTemperature(temperature float64) InputOption {
	return func(d *InputData) {
		d.Temperature = temperature
	}
}

func WithTokens(tokens int) InputOption {
	return func(d *InputData) {
		d.Tokens = tokens
	}
}
