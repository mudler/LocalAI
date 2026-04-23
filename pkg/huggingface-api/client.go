package hfapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Model represents a model from the Hugging Face API
type Model struct {
	ModelID        string         `json:"modelId"`
	Author         string         `json:"author"`
	Downloads      int            `json:"downloads"`
	LastModified   string         `json:"lastModified"`
	PipelineTag    string         `json:"pipelineTag"`
	Private        bool           `json:"private"`
	Tags           []string       `json:"tags"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
	Sha            string         `json:"sha"`
	Config         map[string]any `json:"config"`
	ModelIndex     string         `json:"model_index"`
	LibraryName    string         `json:"library_name"`
	MaskToken      string         `json:"mask_token"`
	TokenizerClass string         `json:"tokenizer_class"`
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
	URL      string
}

// ModelDetails represents detailed information about a model
type ModelDetails struct {
	ModelID       string
	Author        string
	Files         []ModelFile
	ReadmeFile    *ModelFile
	ReadmeContent string

	// PipelineTag mirrors the HuggingFace model-level "pipeline_tag" field
	// (e.g. "text-to-speech", "sentence-similarity"). Empty when the /api/models
	// metadata endpoint is unreachable or the repo does not declare one.
	PipelineTag string

	// LibraryName mirrors the HuggingFace "library_name" field
	// (e.g. "transformers", "diffusers", "sentence-transformers"). Empty when
	// the metadata endpoint is unreachable or the repo does not declare one.
	LibraryName string
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

// GetTrending fetches models sorted by HuggingFace's trendingScore — the
// same signal the public "Trending" tab uses. Useful when picking fresh
// candidates to add to a gallery: it biases toward repos that are gaining
// attention right now, rather than strictly newest or strictly most
// downloaded overall.
func (c *Client) GetTrending(searchTerm string, limit int) ([]Model, error) {
	params := SearchParams{
		Sort:      "trendingScore",
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

// listFilesInPath lists all files in a specific path of a HuggingFace repository (recursive helper)
func (c *Client) listFilesInPath(repoID, path string) ([]FileInfo, error) {
	baseURL := strings.TrimSuffix(c.baseURL, "/api/models")
	var url string
	if path == "" {
		url = fmt.Sprintf("%s/api/models/%s/tree/main", baseURL, repoID)
	} else {
		url = fmt.Sprintf("%s/api/models/%s/tree/main/%s", baseURL, repoID, path)
	}

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

	var items []FileInfo
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	var allFiles []FileInfo
	for _, item := range items {
		switch item.Type {
		// If it's a directory/folder, recursively list its contents
		case "directory", "folder":
			// Build the subfolder path
			subPath := item.Path
			if path != "" {
				subPath = fmt.Sprintf("%s/%s", path, item.Path)
			}

			// Recursively get files from subfolder
			// The recursive call will already prepend the subPath to each file's path
			subFiles, err := c.listFilesInPath(repoID, subPath)
			if err != nil {
				return nil, fmt.Errorf("failed to list files in subfolder %s: %w", subPath, err)
			}

			allFiles = append(allFiles, subFiles...)
		case "file":
			// It's a file, prepend the current path to make it relative to root
			//	if path != "" {
			//		item.Path = fmt.Sprintf("%s/%s", path, item.Path)
			//	}
			allFiles = append(allFiles, item)
		}
	}

	return allFiles, nil
}

// ListFiles lists all files in a HuggingFace repository, including files in subfolders
func (c *Client) ListFiles(repoID string) ([]FileInfo, error) {
	return c.listFilesInPath(repoID, "")
}

// GetFileSHA gets the SHA256 checksum for a specific file by searching through the file list
func (c *Client) GetFileSHA(repoID, fileName string) (string, error) {
	files, err := c.ListFiles(repoID)
	if err != nil {
		return "", fmt.Errorf("failed to list files while getting SHA: %w", err)
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

// modelMetadataResponse mirrors the subset of fields returned by
// GET /api/models/{repoID} that we care about. The public HF endpoint uses
// snake_case (pipeline_tag, library_name) while the list endpoint used by
// SearchModels historically returned camelCase — hence the dedicated struct
// rather than reusing Model.
type modelMetadataResponse struct {
	PipelineTag string `json:"pipeline_tag"`
	LibraryName string `json:"library_name"`
}

// fetchModelMetadata hits GET /api/models/{repoID} to retrieve high-level
// model metadata such as pipeline_tag and library_name. Best-effort: a non-
// 200 response or transport error returns a zero value and a nil error so
// callers can proceed with file-only data.
func (c *Client) fetchModelMetadata(repoID string) (modelMetadataResponse, error) {
	baseURL := strings.TrimSuffix(c.baseURL, "/api/models")
	url := fmt.Sprintf("%s/api/models/%s", baseURL, repoID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return modelMetadataResponse{}, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return modelMetadataResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return modelMetadataResponse{}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return modelMetadataResponse{}, err
	}

	var m modelMetadataResponse
	if err := json.Unmarshal(body, &m); err != nil {
		return modelMetadataResponse{}, err
	}
	return m, nil
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

	// Best-effort: PipelineTag / LibraryName are advisory — some callers
	// (offline tests, restricted networks) can't reach the metadata endpoint.
	// Swallow errors so downstream file detection still works.
	if meta, err := c.fetchModelMetadata(repoID); err == nil {
		details.PipelineTag = meta.PipelineTag
		details.LibraryName = meta.LibraryName
	}

	// Process each file
	baseURL := strings.TrimSuffix(c.baseURL, "/api/models")
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

		// Construct the full URL for the file
		// Use /resolve/main/ for downloading files (handles LFS properly)
		fileURL := fmt.Sprintf("%s/%s/resolve/main/%s", baseURL, repoID, file.Path)

		modelFile := ModelFile{
			Path:     file.Path,
			Size:     file.Size,
			SHA256:   sha256,
			IsReadme: isReadme,
			URL:      fileURL,
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

// shardSuffixRegex matches the `-NNNNN-of-MMMMM.gguf` suffix that llama.cpp
// uses to split large GGUF models across multiple files. Widths of 1–6 digits
// are accepted because shard counts seen in the wild range from single digits
// (unusual) to the common 5-digit zero-padded form (e.g. `-00001-of-00014`).
var shardSuffixRegex = regexp.MustCompile(`(?i)-(\d{1,6})-of-(\d{1,6})\.gguf$`)

// SplitShardSuffix detects llama.cpp-style sharded GGUF filenames. When the
// filename ends with `-NNNNN-of-MMMMM.gguf` it returns the base filename
// (with `.gguf` re-appended), the 1-based shard index, the total shard
// count, and ok=true. Non-sharded filenames return zero values and ok=false.
func SplitShardSuffix(fileName string) (base string, index, total int, ok bool) {
	loc := shardSuffixRegex.FindStringSubmatchIndex(fileName)
	if loc == nil {
		return "", 0, 0, false
	}
	idx, err := strconv.Atoi(fileName[loc[2]:loc[3]])
	if err != nil {
		return "", 0, 0, false
	}
	tot, err := strconv.Atoi(fileName[loc[4]:loc[5]])
	if err != nil {
		return "", 0, 0, false
	}
	return fileName[:loc[0]] + ".gguf", idx, tot, true
}

// ShardGroup bundles every file that belongs to the same logical GGUF model.
// Single-file models produce a one-entry group; multi-part shard sets produce
// one group holding every part in shard-index order.
type ShardGroup struct {
	// Base is the logical filename: for sharded groups this is the common
	// prefix with `.gguf` re-appended; for single-file groups it equals the
	// sole entry's basename.
	Base string
	// Sharded is true when the group represents a multi-part shard set.
	Sharded bool
	// Total is the declared shard count (0 when Sharded is false).
	Total int
	// Files are the group's entries; sharded groups are sorted by index.
	Files []ModelFile
}

// GroupShards buckets ModelFile entries by their shard base. Files that do
// not match the sharded-filename pattern become one-entry groups. Group
// order follows the first appearance of each group in the input (so the
// historical "last-seen wins" fallback logic in the llama-cpp importer
// keeps producing the same group); shards within a group are sorted by
// their 1-based index so downstream consumers can rely on Files[0] being
// shard 1.
func GroupShards(files []ModelFile) []ShardGroup {
	groupIdx := make(map[string]int)
	var groups []ShardGroup

	for _, file := range files {
		name := filepath.Base(file.Path)
		base, _, total, isShard := SplitShardSuffix(name)
		if !isShard {
			groups = append(groups, ShardGroup{
				Base:  name,
				Files: []ModelFile{file},
			})
			continue
		}
		if idx, ok := groupIdx[base]; ok {
			groups[idx].Files = append(groups[idx].Files, file)
			if total > groups[idx].Total {
				groups[idx].Total = total
			}
			continue
		}
		groupIdx[base] = len(groups)
		groups = append(groups, ShardGroup{
			Base:    base,
			Sharded: true,
			Total:   total,
			Files:   []ModelFile{file},
		})
	}

	for i := range groups {
		if !groups[i].Sharded {
			continue
		}
		sort.SliceStable(groups[i].Files, func(a, b int) bool {
			_, ai, _, _ := SplitShardSuffix(filepath.Base(groups[i].Files[a].Path))
			_, bi, _, _ := SplitShardSuffix(filepath.Base(groups[i].Files[b].Path))
			return ai < bi
		})
	}
	return groups
}

// FindPreferredModelFile returns shard #1 of the first group whose base
// filename contains any of the quantization preferences, checking each
// preference in priority order. For single-file models this collapses to
// "the first file whose name contains the preference", preserving the
// historical behaviour while correctly pointing at shard 1 for multi-part
// GGUF models — llama.cpp's split loader needs shard 1 to walk the set.
func FindPreferredModelFile(files []ModelFile, preferences []string) *ModelFile {
	groups := GroupShards(files)
	for _, preference := range preferences {
		lowerPref := strings.ToLower(preference)
		for i := range groups {
			if strings.Contains(strings.ToLower(groups[i].Base), lowerPref) {
				return &groups[i].Files[0]
			}
		}
	}
	return nil
}
