package downloader

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	HuggingFacePrefix = "huggingface://"
	OCIPrefix         = "oci://"
	OllamaPrefix      = "ollama://"
	HTTPPrefix        = "http://"
	HTTPSPrefix       = "https://"
	GithubURI         = "github:"
	GithubURI2        = "github://"
	LocalPrefix       = "file://"
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
		strings.HasPrefix(string(u), GithubURI) ||
		strings.HasPrefix(string(u), OllamaPrefix) ||
		strings.HasPrefix(string(u), OCIPrefix) ||
		strings.HasPrefix(string(u), GithubURI2)
}

func (s URI) LooksLikeOCI() bool {
	return strings.HasPrefix(string(s), OCIPrefix) || strings.HasPrefix(string(s), OllamaPrefix)
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
	case strings.HasPrefix(string(s), HuggingFacePrefix):
		repository := strings.Replace(string(s), HuggingFacePrefix, "", 1)
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

func (uri URI) DownloadFile(filePath, sha string, fileN, total int, downloadStatus func(string, string, string, float64)) error {
	url := uri.ResolveURL()
	if uri.LooksLikeOCI() {
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

		if strings.HasPrefix(url, OllamaPrefix) {
			url = strings.TrimPrefix(url, OllamaPrefix)
			return oci.OllamaFetchModel(url, filePath, progressStatus)
		}

		url = strings.TrimPrefix(url, OCIPrefix)
		img, err := oci.GetImage(url, "", nil, nil)
		if err != nil {
			return fmt.Errorf("failed to get image %q: %v", url, err)
		}

		return oci.ExtractOCIImage(img, filepath.Dir(filePath))
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

	// Download file
	resp, err := http.Get(url)
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

	// save partial download to dedicated file
	tmpFilePath := filePath + ".partial"

	// remove tmp file
	err = removePartialFile(tmpFilePath)
	if err != nil {
		return err
	}

	// Create and write file content
	outFile, err := os.Create(tmpFilePath)
	if err != nil {
		return fmt.Errorf("failed to create file %q: %v", tmpFilePath, err)
	}
	defer outFile.Close()

	progress := &progressWriter{
		fileName:       tmpFilePath,
		total:          resp.ContentLength,
		hash:           sha256.New(),
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
