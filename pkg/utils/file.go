package utils

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/rs/zerolog/log"
)

func CreateTempFileFromMultipartFile(file *multipart.FileHeader, tempDir string, tempPattern string) (string, error) {

	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Create a temporary file in the requested directory:
	outputFile, err := os.CreateTemp(tempDir, tempPattern)
	if err != nil {
		return "", err
	}
	defer outputFile.Close()

	if _, err := io.Copy(outputFile, f); err != nil {
		log.Debug().Msgf("Audio file copying error %+v - %+v - err %+v", file.Filename, outputFile, err)
		return "", err
	}

	return outputFile.Name(), nil
}

func CreateTempFileFromBase64(base64data string, tempDir string, tempPattern string) (string, error) {
	if len(base64data) == 0 {
		return "", fmt.Errorf("base64data empty?")
	}
	//base 64 decode the file and write it somewhere
	// that we will cleanup
	decoded, err := base64.StdEncoding.DecodeString(base64data)
	if err != nil {
		return "", err
	}
	// Create a temporary file in the requested directory:
	outputFile, err := os.CreateTemp(tempDir, tempPattern)
	if err != nil {
		return "", err
	}
	defer outputFile.Close()
	// write the base64 result
	writer := bufio.NewWriter(outputFile)
	_, err = writer.Write(decoded)
	if err != nil {
		return "", err
	}
	return outputFile.Name(), nil
}

func CreateTempFileFromUrl(url string, tempDir string, tempPattern string) (string, error) {
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.CreateTemp(tempDir, tempPattern)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return out.Name(), err
}
