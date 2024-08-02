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
	if len(cleanParts) <= 4 || cleanParts[2] != "huggingface.co" {
		return nil, ErrNonHuggingFaceFile
	}
	results, err := http.Get(fmt.Sprintf("https://huggingface.co/api/models/%s/%s/scan", cleanParts[3], cleanParts[4]))
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
