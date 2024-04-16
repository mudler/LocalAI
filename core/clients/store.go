package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Define a struct to hold the store API client
type StoreClient struct {
	BaseURL string
	Client  *http.Client
}

type SetRequest struct {
	Keys   [][]float32 `json:"keys"`
	Values []string    `json:"values"`
}

type GetRequest struct {
	Keys [][]float32 `json:"keys"`
}

type GetResponse struct {
	Keys   [][]float32 `json:"keys"`
	Values []string    `json:"values"`
}

type DeleteRequest struct {
	Keys [][]float32 `json:"keys"`
}

type FindRequest struct {
	TopK int       `json:"topk"`
	Key  []float32 `json:"key"`
}

type FindResponse struct {
	Keys         [][]float32 `json:"keys"`
	Values       []string    `json:"values"`
	Similarities []float32   `json:"similarities"`
}

// Constructor for StoreClient
func NewStoreClient(baseUrl string) *StoreClient {
	return &StoreClient{
		BaseURL: baseUrl,
		Client:  &http.Client{},
	}
}

// Implement Set method
func (c *StoreClient) Set(req SetRequest) error {
	return c.doRequest("stores/set", req)
}

// Implement Get method
func (c *StoreClient) Get(req GetRequest) (*GetResponse, error) {
	body, err := c.doRequestWithResponse("stores/get", req)
	if err != nil {
		return nil, err
	}

	var resp GetResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// Implement Delete method
func (c *StoreClient) Delete(req DeleteRequest) error {
	return c.doRequest("stores/delete", req)
}

// Implement Find method
func (c *StoreClient) Find(req FindRequest) (*FindResponse, error) {
	body, err := c.doRequestWithResponse("stores/find", req)
	if err != nil {
		return nil, err
	}

	var resp FindResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// Helper function to perform a request without expecting a response body
func (c *StoreClient) doRequest(path string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/"+path, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API request to %s failed with status code %d", path, resp.StatusCode)
	}

	return nil
}

// Helper function to perform a request and parse the response body
func (c *StoreClient) doRequestWithResponse(path string, data interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/"+path, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request to %s failed with status code %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}
