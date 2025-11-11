package hfapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

// Model represents a model from the Hugging Face API
type Model struct {
	ModelID        string                 `json:"modelId"`
	Author         string                 `json:"author"`
	Downloads      int                    `json:"downloads"`
	LastModified   string                 `json:"lastModified"`
	PipelineTag    string                 `json:"pipelineTag"`
	Private        bool                   `json:"private"`
	Tags           []string               `json:"tags"`
	CreatedAt      string                 `json:"createdAt"`
	UpdatedAt      string                 `json:"updatedAt"`
	Sha            string                 `json:"sha"`
	Config         map[string]interface{} `json:"config"`
	ModelIndex     string                 `json:"model_index"`
	LibraryName    string                 `json:"library_name"`
	MaskToken      string                 `json:"mask_token"`
	TokenizerClass string                 `json:"tokenizer_class"`
}

// FileInfo represents file information from HuggingFace
type FileInfo struct {
	Type    string   `json:"type"`
	Oid     string   `json:"oid"`
	Size    int64    `json:"size"`
	Path    string   `json:"path"`
	LFS     *LFSInfo `json:"lfs,omitempty"`
	XetHash string   `json:"xetHash,omitempty"`
}

// LFSInfo represents LFS (Large File Storage) information
type LFSInfo struct {
	Oid         string `json:"oid"`
	Size        int64  `json:"size"`
	PointerSize int    `json:"pointerSize"`
}

// ModelFile represents a file in a model repository
type ModelFile struct {
	Path     string
	Size     int64
	SHA256   string
	IsReadme bool
}

// ModelDetails represents detailed information about a model
type ModelDetails struct {
	ModelID       string
	Author        string
	Files         []ModelFile
	ReadmeFile    *ModelFile
	ReadmeContent string
}

// SearchParams represents the parameters for searching models
type SearchParams struct {
	Sort      string `json:"sort"`
	Direction int    `json:"direction"`
	Limit     int    `json:"limit"`
	Search    string `json:"search"`
}

// Client represents a Hugging Face API client
type Client struct {
	baseURL string
	client  *http.Client
}

// NewClient creates a new Hugging Face API client
func NewClient() *Client {
	return &Client{
		baseURL: "https://huggingface.co/api/models",
		client:  &http.Client{},
	}
}

// SearchModels searches for models using the Hugging Face API
func (c *Client) SearchModels(params SearchParams) ([]Model, error) {
	req, err := http.NewRequest("GET", c.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("sort", params.Sort)
	q.Add("direction", fmt.Sprintf("%d", params.Direction))
	q.Add("limit", fmt.Sprintf("%d", params.Limit))
	q.Add("search", params.Search)
	req.URL.RawQuery = q.Encode()

	// Make the HTTP request
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch models. Status code: %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the JSON response
	var models []Model
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return models, nil
}

// GetLatest fetches the latest GGUF models
func (c *Client) GetLatest(searchTerm string, limit int) ([]Model, error) {
	params := SearchParams{
		Sort:      "lastModified",
		Direction: -1,
		Limit:     limit,
		Search:    searchTerm,
	}

	return c.SearchModels(params)
}

// BaseURL returns the current base URL
func (c *Client) BaseURL() string {
	return c.baseURL
}

// SetBaseURL sets a new base URL (useful for testing)
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// ListFiles lists all files in a HuggingFace repository
func (c *Client) ListFiles(repoID string) ([]FileInfo, error) {
	baseURL := strings.TrimSuffix(c.baseURL, "/api/models")
	url := fmt.Sprintf("%s/api/models/%s/tree/main", baseURL, repoID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch files. Status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var files []FileInfo
	if err := json.Unmarshal(body, &files); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return files, nil
}

// GetFileSHA gets the SHA256 checksum for a specific file by searching through the file list
func (c *Client) GetFileSHA(repoID, fileName string) (string, error) {
	files, err := c.ListFiles(repoID)
	if err != nil {
		return "", fmt.Errorf("failed to list files: %w", err)
	}

	for _, file := range files {
		if filepath.Base(file.Path) == fileName {
			if file.LFS != nil && file.LFS.Oid != "" {
				// The LFS OID contains the SHA256 hash
				return file.LFS.Oid, nil
			}
			// If no LFS, return the regular OID
			return file.Oid, nil
		}
	}

	return "", fmt.Errorf("file %s not found", fileName)
}

// GetModelDetails gets detailed information about a model including files and checksums
func (c *Client) GetModelDetails(repoID string) (*ModelDetails, error) {
	files, err := c.ListFiles(repoID)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	details := &ModelDetails{
		ModelID: repoID,
		Author:  strings.Split(repoID, "/")[0],
		Files:   make([]ModelFile, 0, len(files)),
	}

	// Process each file
	for _, file := range files {
		fileName := filepath.Base(file.Path)
		isReadme := strings.Contains(strings.ToLower(fileName), "readme")

		// Extract SHA256 from LFS or use OID
		sha256 := ""
		if file.LFS != nil && file.LFS.Oid != "" {
			sha256 = file.LFS.Oid
		} else {
			sha256 = file.Oid
		}

		modelFile := ModelFile{
			Path:     file.Path,
			Size:     file.Size,
			SHA256:   sha256,
			IsReadme: isReadme,
		}

		details.Files = append(details.Files, modelFile)

		// Set the readme file
		if isReadme && details.ReadmeFile == nil {
			details.ReadmeFile = &modelFile
		}
	}

	return details, nil
}

// GetReadmeContent gets the content of a README file
func (c *Client) GetReadmeContent(repoID, readmePath string) (string, error) {
	baseURL := strings.TrimSuffix(c.baseURL, "/api/models")
	url := fmt.Sprintf("%s/%s/raw/main/%s", baseURL, repoID, readmePath)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch readme content. Status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// FilterFilesByQuantization filters files by quantization type
func FilterFilesByQuantization(files []ModelFile, quantization string) []ModelFile {
	var filtered []ModelFile
	for _, file := range files {
		fileName := filepath.Base(file.Path)
		if strings.Contains(strings.ToLower(fileName), strings.ToLower(quantization)) {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

// FindPreferredModelFile finds the preferred model file based on quantization preferences
func FindPreferredModelFile(files []ModelFile, preferences []string) *ModelFile {
	for _, preference := range preferences {
		for i := range files {
			fileName := filepath.Base(files[i].Path)
			if strings.Contains(strings.ToLower(fileName), strings.ToLower(preference)) {
				return &files[i]
			}
		}
	}
	return nil
}
