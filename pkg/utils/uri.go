package utils

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

const (
	githubURI = "github:"
)

func GetURI(url string, f func(url string, i []byte) error) error {
	if strings.HasPrefix(url, githubURI) {
		parts := strings.Split(url, ":")
		repoParts := strings.Split(parts[1], "@")
		branch := "main"

		if len(repoParts) > 1 {
			branch = repoParts[1]
		}

		repoPath := strings.Split(repoParts[0], "/")
		org := repoPath[0]
		project := repoPath[1]
		projectPath := strings.Join(repoPath[2:], "/")

		url = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, project, branch, projectPath)
	}

	if strings.HasPrefix(url, "file://") {
		rawURL := strings.TrimPrefix(url, "file://")
		// checks if the file is symbolic, and resolve if so - otherwise, this function returns the path unmodified.
		resolvedFile, err := filepath.EvalSymlinks(rawURL)
		if err != nil {
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
	response, err := http.Get(url)
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

func ConvertURL(s string) string {
	switch {
	case strings.HasPrefix(s, "huggingface://"):
		repository := strings.Replace(s, "huggingface://", "", 1)
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

	return s
}

func removeFile(tmpFilePath string) error {
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

func DownloadFile(url string, filePath, sha string, downloadStatus func(string, string, string, float64)) error {
	url = ConvertURL(url)
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

	// Create parent directory
	err = os.MkdirAll(filepath.Dir(filePath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create parent directory for file %q: %v", filePath, err)
	}

	// save partial download to dedicated file
	tmpFilePath := filePath + ".partial"

	// remove tmp file
	err = removeFile(tmpFilePath)
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
	if IsArchive(filePath) {
		basePath := filepath.Dir(filePath)
		log.Info().Msgf("File %q is an archive, uncompressing to %s", filePath, basePath)
		if err := ExtractArchive(filePath, basePath); err != nil {
			log.Debug().Msgf("Failed decompressing %q: %s", filePath, err.Error())
			return err
		}
	}

	return nil
}

type progressWriter struct {
	fileName       string
	total          int64
	written        int64
	downloadStatus func(string, string, string, float64)
	hash           hash.Hash
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.hash.Write(p)
	pw.written += int64(n)

	if pw.total > 0 {
		percentage := float64(pw.written) / float64(pw.total) * 100
		//log.Debug().Msgf("Downloading %s: %s/%s (%.2f%%)", pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
	} else {
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), "", 0)
	}

	return
}

// MD5 of a string
func MD5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
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
