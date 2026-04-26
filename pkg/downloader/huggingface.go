package downloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type HuggingFaceScanResult struct {
	RepositoryId        string   `json:"repositoryId"`
	Revision            string   `json:"revision"`
	HasUnsafeFiles      bool     `json:"hasUnsafeFile"`
	ClamAVInfectedFiles []string `json:"clamAVInfectedFiles"`
	DangerousPickles    []string `json:"dangerousPickles"`
	ScansDone           bool     `json:"scansDone"`
}

var ErrNonHuggingFaceFile = errors.New("not a huggingface repo")
var ErrUnsafeFilesFound = errors.New("unsafe files found")

func HuggingFaceScan(uri URI) (*HuggingFaceScanResult, error) {
	cleanParts := strings.Split(uri.ResolveURL(), "/")
	// cleanParts[2] is the hostname from the URL (e.g. "huggingface.co" or "hf-mirror.com").
	// Extract the hostname from HF_ENDPOINT for comparison, since HF_ENDPOINT includes the scheme.
	hfHost := strings.TrimPrefix(strings.TrimPrefix(HF_ENDPOINT, "https://"), "http://")
	if len(cleanParts) <= 4 || (cleanParts[2] != "huggingface.co" && cleanParts[2] != hfHost) {
		return nil, ErrNonHuggingFaceFile
	}
	results, err := http.Get(fmt.Sprintf("%s/api/models/%s/%s/scan", HF_ENDPOINT, cleanParts[3], cleanParts[4]))
	if err != nil {
		return nil, err
	}
	if results.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code during HuggingFaceScan: %d", results.StatusCode)
	}
	scanResult := &HuggingFaceScanResult{}
	bodyBytes, err := io.ReadAll(results.Body)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(bodyBytes, scanResult)
	if err != nil {
		return nil, err
	}
	if scanResult.HasUnsafeFiles {
		return scanResult, ErrUnsafeFilesFound
	}
	return scanResult, nil
}
