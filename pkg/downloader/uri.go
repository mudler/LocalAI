package downloader

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/tarball"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	HuggingFacePrefix  = "huggingface://"
	HuggingFacePrefix1 = "hf://"
	HuggingFacePrefix2 = "hf.co/"
	OCIPrefix          = "oci://"
	OCIFilePrefix      = "ocifile://"
	OllamaPrefix       = "ollama://"
	HTTPPrefix         = "http://"
	HTTPSPrefix        = "https://"
	GithubURI          = "github:"
	GithubURI2         = "github://"
	LocalPrefix        = "file://"
)

type URI string

func (uri URI) DownloadWithCallback(basePath string, f func(url string, i []byte) error) error {
	return uri.DownloadWithAuthorizationAndCallback(basePath, "", f)
}

func (uri URI) DownloadWithAuthorizationAndCallback(basePath string, authorization string, f func(url string, i []byte) error) error {
	url := uri.ResolveURL()

	if strings.HasPrefix(url, LocalPrefix) {
		rawURL := strings.TrimPrefix(url, LocalPrefix)
		// checks if the file is symbolic, and resolve if so - otherwise, this function returns the path unmodified.
		resolvedFile, err := filepath.EvalSymlinks(rawURL)
		if err != nil {
			return err
		}
		resolvedBasePath, err := filepath.EvalSymlinks(basePath)
		if err != nil {
			return err
		}
		// Check if the local file is rooted in basePath
		err = utils.InTrustedRoot(resolvedFile, resolvedBasePath)
		if err != nil {
			log.Debug().Str("resolvedFile", resolvedFile).Str("basePath", basePath).Msg("downloader.GetURI blocked an attempt to ready a file url outside of basePath")
			return err
		}
		// Read the response body
		body, err := os.ReadFile(resolvedFile)
		if err != nil {
			return err
		}

		// Unmarshal YAML data into a struct
		return f(url, body)
	}

	// Send a GET request to the URL

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Add("Authorization", authorization)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	// Unmarshal YAML data into a struct
	return f(url, body)
}

func (u URI) FilenameFromUrl() (string, error) {
	f, err := filenameFromUrl(string(u))
	if err != nil || f == "" {
		f = utils.MD5(string(u))
		if strings.HasSuffix(string(u), ".yaml") || strings.HasSuffix(string(u), ".yml") {
			f = f + ".yaml"
		}
		err = nil
	}

	return f, err
}

func filenameFromUrl(urlstr string) (string, error) {
	// strip anything after @
	if strings.Contains(urlstr, "@") {
		urlstr = strings.Split(urlstr, "@")[0]
	}

	u, err := url.Parse(urlstr)
	if err != nil {
		return "", fmt.Errorf("error due to parsing url: %w", err)
	}
	x, err := url.QueryUnescape(u.EscapedPath())
	if err != nil {
		return "", fmt.Errorf("error due to escaping: %w", err)
	}
	return filepath.Base(x), nil
}

func (u URI) LooksLikeURL() bool {
	return strings.HasPrefix(string(u), HTTPPrefix) ||
		strings.HasPrefix(string(u), HTTPSPrefix) ||
		strings.HasPrefix(string(u), HuggingFacePrefix) ||
		strings.HasPrefix(string(u), HuggingFacePrefix1) ||
		strings.HasPrefix(string(u), HuggingFacePrefix2) ||
		strings.HasPrefix(string(u), GithubURI) ||
		strings.HasPrefix(string(u), OllamaPrefix) ||
		strings.HasPrefix(string(u), OCIPrefix) ||
		strings.HasPrefix(string(u), GithubURI2)
}

func (u URI) LooksLikeHTTPURL() bool {
	return strings.HasPrefix(string(u), HTTPPrefix) ||
		strings.HasPrefix(string(u), HTTPSPrefix)
}

func (u URI) LooksLikeDir() bool {
	f, err := os.Stat(string(u))
	return err == nil && f.IsDir()
}

func (s URI) LooksLikeOCI() bool {
	return strings.HasPrefix(string(s), "quay.io") ||
		strings.HasPrefix(string(s), OCIPrefix) ||
		strings.HasPrefix(string(s), OllamaPrefix) ||
		strings.HasPrefix(string(s), OCIFilePrefix) ||
		strings.HasPrefix(string(s), "ghcr.io") ||
		strings.HasPrefix(string(s), "docker.io")
}

func (s URI) ResolveURL() string {
	switch {
	case strings.HasPrefix(string(s), GithubURI2):
		repository := strings.Replace(string(s), GithubURI2, "", 1)

		repoParts := strings.Split(repository, "@")
		branch := "main"

		if len(repoParts) > 1 {
			branch = repoParts[1]
		}

		repoPath := strings.Split(repoParts[0], "/")
		org := repoPath[0]
		project := repoPath[1]
		projectPath := strings.Join(repoPath[2:], "/")

		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, project, branch, projectPath)
	case strings.HasPrefix(string(s), GithubURI):
		parts := strings.Split(string(s), ":")
		repoParts := strings.Split(parts[1], "@")
		branch := "main"

		if len(repoParts) > 1 {
			branch = repoParts[1]
		}

		repoPath := strings.Split(repoParts[0], "/")
		org := repoPath[0]
		project := repoPath[1]
		projectPath := strings.Join(repoPath[2:], "/")

		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, project, branch, projectPath)
	case strings.HasPrefix(string(s), HuggingFacePrefix) || strings.HasPrefix(string(s), HuggingFacePrefix1) || strings.HasPrefix(string(s), HuggingFacePrefix2):
		repository := strings.Replace(string(s), HuggingFacePrefix, "", 1)
		repository = strings.Replace(repository, HuggingFacePrefix1, "", 1)
		repository = strings.Replace(repository, HuggingFacePrefix2, "", 1)
		// convert repository to a full URL.
		// e.g. TheBloke/Mixtral-8x7B-v0.1-GGUF/mixtral-8x7b-v0.1.Q2_K.gguf@main -> https://huggingface.co/TheBloke/Mixtral-8x7B-v0.1-GGUF/resolve/main/mixtral-8x7b-v0.1.Q2_K.gguf
		owner := strings.Split(repository, "/")[0]
		repo := strings.Split(repository, "/")[1]

		branch := "main"
		if strings.Contains(repo, "@") {
			branch = strings.Split(repository, "@")[1]
		}
		filepath := strings.Split(repository, "/")[2]
		if strings.Contains(filepath, "@") {
			filepath = strings.Split(filepath, "@")[0]
		}

		return fmt.Sprintf("https://huggingface.co/%s/%s/resolve/%s/%s", owner, repo, branch, filepath)
	}

	return string(s)
}

func removePartialFile(tmpFilePath string) error {
	_, err := os.Stat(tmpFilePath)
	if err == nil {
		log.Debug().Msgf("Removing temporary file %s", tmpFilePath)
		err = os.Remove(tmpFilePath)
		if err != nil {
			err1 := fmt.Errorf("failed to remove temporary download file %s: %v", tmpFilePath, err)
			log.Warn().Msg(err1.Error())
			return err1
		}
	}
	return nil
}

func calculateHashForPartialFile(file *os.File) (hash.Hash, error) {
	hash := sha256.New()
	_, err := io.Copy(hash, file)
	if err != nil {
		return nil, err
	}
	return hash, nil
}

func (uri URI) checkSeverSupportsRangeHeader() (bool, error) {
	url := uri.ResolveURL()
	resp, err := http.Head(url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Accept-Ranges") == "bytes", nil
}

func (uri URI) DownloadFile(filePath, sha string, fileN, total int, downloadStatus func(string, string, string, float64)) error {
	url := uri.ResolveURL()
	if uri.LooksLikeOCI() {

		// Only Ollama wants to download to the file, for the rest, we want to download to the directory
		// so we check if filepath has any extension, otherwise we assume it's a directory
		if filepath.Ext(filePath) != "" && !strings.HasPrefix(url, OllamaPrefix) {
			filePath = filepath.Dir(filePath)
		}

		progressStatus := func(desc ocispec.Descriptor) io.Writer {
			return &progressWriter{
				fileName:       filePath,
				total:          desc.Size,
				hash:           sha256.New(),
				fileNo:         fileN,
				totalFiles:     total,
				downloadStatus: downloadStatus,
			}
		}

		if url, ok := strings.CutPrefix(url, OllamaPrefix); ok {
			return oci.OllamaFetchModel(url, filePath, progressStatus)
		}

		if url, ok := strings.CutPrefix(url, OCIFilePrefix); ok {
			// Open the tarball
			img, err := tarball.ImageFromPath(url, nil)
			if err != nil {
				return fmt.Errorf("failed to open tarball: %s", err.Error())
			}

			return oci.ExtractOCIImage(img, url, filePath, downloadStatus)
		}

		url = strings.TrimPrefix(url, OCIPrefix)
		img, err := oci.GetImage(url, "", nil, nil)
		if err != nil {
			return fmt.Errorf("failed to get image %q: %v", url, err)
		}

		return oci.ExtractOCIImage(img, url, filePath, downloadStatus)
	}

	// We need to check if url looks like an URL or bail out
	if !URI(url).LooksLikeHTTPURL() {
		return fmt.Errorf("url %q does not look like an HTTP URL", url)
	}

	// Check if the file already exists
	_, err := os.Stat(filePath)
	if err == nil {
		// File exists, check SHA
		if sha != "" {
			// Verify SHA
			calculatedSHA, err := calculateSHA(filePath)
			if err != nil {
				return fmt.Errorf("failed to calculate SHA for file %q: %v", filePath, err)
			}
			if calculatedSHA == sha {
				// SHA matches, skip downloading
				log.Debug().Msgf("File %q already exists and matches the SHA. Skipping download", filePath)
				return nil
			}
			// SHA doesn't match, delete the file and download again
			err = os.Remove(filePath)
			if err != nil {
				return fmt.Errorf("failed to remove existing file %q: %v", filePath, err)
			}
			log.Debug().Msgf("Removed %q (SHA doesn't match)", filePath)

		} else {
			// SHA is missing, skip downloading
			log.Debug().Msgf("File %q already exists. Skipping download", filePath)
			return nil
		}
	} else if !os.IsNotExist(err) {
		// Error occurred while checking file existence
		return fmt.Errorf("failed to check file %q existence: %v", filePath, err)
	}

	log.Info().Msgf("Downloading %q", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %q: %v", filePath, err)
	}

	// save partial download to dedicated file
	tmpFilePath := filePath + ".partial"
	tmpFileInfo, err := os.Stat(tmpFilePath)
	if err == nil {
		support, err := uri.checkSeverSupportsRangeHeader()
		if err != nil {
			return fmt.Errorf("failed to check if uri server supports range header: %v", err)
		}
		if support {
			startPos := tmpFileInfo.Size()
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startPos))
		} else {
			err := removePartialFile(tmpFilePath)
			if err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to check file %q existence: %v", filePath, err)
	}

	// Start the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download file %q: %v", filePath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("failed to download url %q, invalid status code %d", url, resp.StatusCode)
	}

	// Create parent directory
	err = os.MkdirAll(filepath.Dir(filePath), 0750)
	if err != nil {
		return fmt.Errorf("failed to create parent directory for file %q: %v", filePath, err)
	}

	// Create and write file
	outFile, err := os.OpenFile(tmpFilePath, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to create / open file %q: %v", tmpFilePath, err)
	}
	defer outFile.Close()
	hash, err := calculateHashForPartialFile(outFile)
	if err != nil {
		return fmt.Errorf("failed to calculate hash for partial file")
	}
	progress := &progressWriter{
		fileName:       tmpFilePath,
		total:          resp.ContentLength,
		hash:           hash,
		fileNo:         fileN,
		totalFiles:     total,
		downloadStatus: downloadStatus,
	}
	_, err = io.Copy(io.MultiWriter(outFile, progress), resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file %q: %v", filePath, err)
	}

	err = os.Rename(tmpFilePath, filePath)
	if err != nil {
		return fmt.Errorf("failed to rename temporary file %s -> %s: %v", tmpFilePath, filePath, err)
	}

	if sha != "" {
		// Verify SHA
		calculatedSHA := fmt.Sprintf("%x", progress.hash.Sum(nil))
		if calculatedSHA != sha {
			log.Debug().Msgf("SHA mismatch for file %q ( calculated: %s != metadata: %s )", filePath, calculatedSHA, sha)
			return fmt.Errorf("SHA mismatch for file %q ( calculated: %s != metadata: %s )", filePath, calculatedSHA, sha)
		}
	} else {
		log.Debug().Msgf("SHA missing for %q. Skipping validation", filePath)
	}

	log.Info().Msgf("File %q downloaded and verified", filePath)
	if utils.IsArchive(filePath) {
		basePath := filepath.Dir(filePath)
		log.Info().Msgf("File %q is an archive, uncompressing to %s", filePath, basePath)
		if err := utils.ExtractArchive(filePath, basePath); err != nil {
			log.Debug().Msgf("Failed decompressing %q: %s", filePath, err.Error())
			return err
		}
	}

	return nil
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func calculateSHA(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
