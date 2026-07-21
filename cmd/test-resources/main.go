// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/internal/testresources"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "test-resources:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 4 {
		return errors.New("usage: test-resources <prepare|update> TARGET MANIFEST_DIR CACHE_DIR")
	}
	target, manifestDir, cacheDir := args[1], args[2], args[3]
	if args[0] == "update" {
		return update(target, manifestDir, cacheDir)
	}
	if args[0] != "prepare" {
		return errors.New("usage: test-resources <prepare|update> TARGET MANIFEST_DIR CACHE_DIR")
	}
	manifest, err := testresources.LoadManifest(filepath.Join(manifestDir, target+".json"))
	if err != nil {
		return fmt.Errorf("%w; run `make update-test-resources TARGET=%s`", err, target)
	}
	if manifest.Target != target {
		return fmt.Errorf("manifest target %q does not match %q", manifest.Target, target)
	}
	lock, err := testresources.LoadLock(filepath.Join(manifestDir, "lock.json"))
	if err != nil {
		return err
	}
	if _, ok := lock.Bundles[target]; !ok {
		return fmt.Errorf("cache bundle is not locked for target %q; run `make update-test-resources TARGET=%s`", target, target)
	}
	materialized := filepath.Join(cacheDir, "materialized", target)
	if err := os.MkdirAll(materialized, 0o755); err != nil {
		return err
	}
	index := map[string]string{}
	for _, resource := range manifest.HTTP {
		path, err := testresources.VerifyBlob(cacheDir, resource.SHA256)
		if err != nil {
			return preparationError(target, err)
		}
		index[resource.Method+" "+resource.URL] = path
	}
	for _, resource := range manifest.Files {
		path, err := testresources.VerifyBlob(cacheDir, resource.SHA256)
		if err != nil {
			return preparationError(target, err)
		}
		if resource.Destination != "" {
			destination := filepath.Join(materialized, resource.Destination)
			if err := copyFile(path, destination); err != nil {
				return err
			}
		}
		if resource.Environment != "" {
			index["env:"+resource.Environment] = path
		}
	}
	for _, resource := range manifest.Images {
		path, err := testresources.VerifyBlob(cacheDir, resource.SHA256)
		if err != nil {
			return preparationError(target, err)
		}
		cmd := exec.Command("docker", "load", "--input", path)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("load declared image %s: %w", resource.Reference, err)
		}
	}
	return writeIndex(filepath.Join(cacheDir, "index.json"), index)
}

func update(target, manifestDir, cacheDir string) error {
	if os.Getenv("LOCALAI_TEST_RESOURCES_ONLINE") != "1" {
		return errors.New("update requires explicit online record mode: LOCALAI_TEST_RESOURCES_ONLINE=1")
	}
	manifest, err := testresources.LoadManifest(filepath.Join(manifestDir, target+".json"))
	if err != nil {
		return err
	}
	client := httpclient.New(httpclient.WithFollowRedirects())
	for _, resource := range manifest.HTTP {
		if resource.Method != http.MethodGet && resource.Method != http.MethodHead {
			return fmt.Errorf("recording HTTP method %s requires the replay proxy recorder", resource.Method)
		}
		if resource.Method == http.MethodHead {
			if err := storeVerified(strings.NewReader(""), resource.SHA256, cacheDir); err != nil {
				return err
			}
			continue
		}
		if err := fetch(client, resource.URL, resource.SHA256, cacheDir); err != nil {
			return err
		}
	}
	for _, resource := range manifest.Files {
		if err := fetch(client, resource.URL, resource.SHA256, cacheDir); err != nil {
			return err
		}
	}
	for _, resource := range manifest.Images {
		if err := pullAndPack(resource.Reference, resource.SHA256, cacheDir); err != nil {
			return err
		}
	}
	return nil
}

func fetch(client *http.Client, rawURL, expected, cacheDir string) error {
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	if response.StatusCode != http.StatusOK {
		closeErr := response.Body.Close()
		if closeErr != nil {
			return errors.Join(fmt.Errorf("fetch %s: status %s", rawURL, response.Status), closeErr)
		}
		return fmt.Errorf("fetch %s: status %s", rawURL, response.Status)
	}
	storeErr := storeVerified(response.Body, expected, cacheDir)
	return errors.Join(storeErr, response.Body.Close())
}

func storeVerified(reader io.Reader, expected, cacheDir string) error {
	directory := filepath.Join(cacheDir, "blobs", "sha256")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".record-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(temporary, hash), reader)
	closeErr := temporary.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("resource digest mismatch: expected sha256:%s, got sha256:%s", expected, actual)
	}
	return os.Rename(temporaryName, testresources.BlobPath(cacheDir, expected))
}

func pullAndPack(reference, expected, cacheDir string) error {
	if !strings.Contains(reference, "@sha256:") {
		return fmt.Errorf("refusing mutable image reference %s", reference)
	}
	if err := exec.Command("docker", "pull", reference).Run(); err != nil {
		return fmt.Errorf("pull image %s: %w", reference, err)
	}
	cmd := exec.Command("docker", "save", reference)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	storeErr := storeVerified(stdout, expected, cacheDir)
	waitErr := cmd.Wait()
	return errors.Join(storeErr, waitErr)
}

func preparationError(target string, err error) error {
	return fmt.Errorf("%w; run `make test-resources TARGET=%s` during the network-enabled preparation phase", err, target)
}

func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_ = in.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, in.Close(), out.Close())
}

func writeIndex(path string, index map[string]string) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
