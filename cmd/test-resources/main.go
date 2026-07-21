// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/core/services/cloudproxy/mitm"
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
	if len(args) >= 6 && args[0] == "run" && args[4] == "--" {
		return runOffline(args[1], args[2], args[3], args[5:])
	}
	if len(args) != 4 {
		return errors.New("usage: test-resources <prepare|update> TARGET MANIFEST_DIR CACHE_DIR | test-resources run TARGET MANIFEST_DIR CACHE_DIR -- COMMAND")
	}
	target, manifestDir, cacheDir := args[1], args[2], args[3]
	if args[0] == "update" {
		return update(target, manifestDir, cacheDir)
	}
	if args[0] != "prepare" {
		return errors.New("usage: test-resources <prepare|update> TARGET MANIFEST_DIR CACHE_DIR")
	}
	return prepare(target, manifestDir, cacheDir)
}

func prepare(target, manifestDir, cacheDir string) error {
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
	locked, ok := lock.Bundles[target]
	if !ok {
		return fmt.Errorf("cache bundle is not locked for target %q; run `make update-test-resources TARGET=%s`", target, target)
	}
	if digest, ok := strings.CutPrefix(locked, "sha256:"); ok {
		bundlePath := filepath.Join(cacheDir, "bundles", target+".tar")
		if err := testresources.RestoreBundle(cacheDir, bundlePath, digest); err != nil {
			return preparationError(target, err)
		}
	}
	materialized := filepath.Join(cacheDir, "materialized", target)
	if err := os.MkdirAll(materialized, 0o755); err != nil {
		return err
	}
	index, err := testresources.LoadHTTPIndex(cacheDir)
	if err != nil {
		return preparationError(target, err)
	}
	for _, resource := range manifest.HTTP {
		_, err := testresources.VerifyBlob(cacheDir, resource.SHA256)
		if err != nil {
			return preparationError(target, err)
		}
		entry, ok := index[testresources.RequestKey(resource.Method, resource.URL, resource.Headers())]
		if !ok || entry.Digest != resource.SHA256 {
			return preparationError(target, fmt.Errorf("HTTP cache entry missing or mismatched: %s %s", resource.Method, resource.URL))
		}
	}
	for _, resource := range manifest.Files {
		path, err := testresources.VerifyBlob(cacheDir, resource.SHA256)
		if err != nil {
			return preparationError(target, err)
		}
		environmentPath := path
		if resource.Destination != "" {
			destination := filepath.Join(materialized, resource.Destination)
			if err := copyFile(path, destination); err != nil {
				return err
			}
			environmentPath = destination
		}
		if resource.Environment != "" {
			if err := os.Setenv(resource.Environment, environmentPath); err != nil {
				return err
			}
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
	return nil
}

func update(target, manifestDir, cacheDir string) error {
	if os.Getenv("LOCALAI_TEST_RESOURCES_ONLINE") != "1" {
		return errors.New("update requires explicit online record mode: LOCALAI_TEST_RESOURCES_ONLINE=1")
	}
	manifest, err := testresources.LoadManifest(filepath.Join(manifestDir, target+".json"))
	if err != nil {
		return err
	}
	client := httpclient.New()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	index, err := testresources.LoadHTTPIndex(cacheDir)
	if err != nil {
		return err
	}
	for _, resource := range manifest.HTTP {
		entry, err := fetchHTTP(client, resource, cacheDir)
		if err != nil {
			return err
		}
		index[testresources.RequestKey(resource.Method, resource.URL, resource.Headers())] = entry
	}
	if err := testresources.WriteHTTPIndex(cacheDir, index); err != nil {
		return err
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
	bundlePath := filepath.Join(cacheDir, "bundles", target+".tar")
	digest, err := testresources.PackBundle(cacheDir, bundlePath, manifest)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(manifestDir, "lock.json")
	lock, err := testresources.LoadLock(lockPath)
	if err != nil {
		return err
	}
	lock.Bundles[target] = "sha256:" + digest
	return testresources.WriteLock(lockPath, lock)
}

func fetchHTTP(client *http.Client, resource testresources.HTTP, cacheDir string) (testresources.HTTPEntry, error) {
	request, err := http.NewRequest(resource.Method, resource.URL, nil)
	if err != nil {
		return testresources.HTTPEntry{}, err
	}
	request.Header = resource.Headers()
	response, err := client.Do(request)
	if err != nil {
		return testresources.HTTPEntry{}, fmt.Errorf("fetch %s: %w", resource.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	size, err := storeVerified(response.Body, resource.SHA256, cacheDir)
	if err != nil {
		return testresources.HTTPEntry{}, err
	}
	return testresources.HTTPEntry{Digest: resource.SHA256, Size: size, Status: response.StatusCode, Header: testresources.SanitizeHeaders(response.Header)}, nil
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
	_, storeErr := storeVerified(response.Body, expected, cacheDir)
	return errors.Join(storeErr, response.Body.Close())
}

func storeVerified(reader io.Reader, expected, cacheDir string) (int64, error) {
	directory := filepath.Join(cacheDir, "blobs", "sha256")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 0, err
	}
	temporary, err := os.CreateTemp(directory, ".record-*")
	if err != nil {
		return 0, err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hash), reader)
	closeErr := temporary.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return 0, err
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if actual != expected {
		return 0, fmt.Errorf("resource digest mismatch: expected sha256:%s, got sha256:%s", expected, actual)
	}
	if err := os.Rename(temporaryName, testresources.BlobPath(cacheDir, expected)); err != nil {
		return 0, err
	}
	return size, nil
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
	_, storeErr := storeVerified(stdout, expected, cacheDir)
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

func runOffline(target, manifestDir, cacheDir string, command []string) error {
	manifest, err := testresources.LoadManifest(filepath.Join(manifestDir, target+".json"))
	if err != nil {
		return err
	}
	if err := prepare(target, manifestDir, cacheDir); err != nil {
		return err
	}
	dockerNetwork := ""
	if runtime.GOOS == "linux" && (len(manifest.Images) > 0 || target == "aio") {
		dockerNetwork = fmt.Sprintf("localai-test-%d", os.Getpid())
		create := exec.Command("docker", "network", "create", "--internal", dockerNetwork)
		create.Stdout, create.Stderr = io.Discard, os.Stderr
		if err := create.Run(); err != nil {
			return fmt.Errorf("create internal test Docker network: %w", err)
		}
		defer func() { _ = exec.Command("docker", "network", "rm", dockerNetwork).Run() }()
	}
	index, err := testresources.LoadHTTPIndex(cacheDir)
	if err != nil {
		return err
	}
	hosts := make([]string, 0, len(manifest.HTTP))
	seen := map[string]bool{}
	for _, resource := range manifest.HTTP {
		parsed, err := url.Parse(resource.URL)
		if err != nil {
			return err
		}
		if parsed.Hostname() != "" && !seen[parsed.Hostname()] {
			hosts = append(hosts, parsed.Hostname())
			seen[parsed.Hostname()] = true
		}
	}
	caDir := filepath.Join(cacheDir, "ca")
	ca, err := mitm.LoadOrCreateCA(caDir)
	if err != nil {
		return err
	}
	server, err := mitm.NewServer(mitm.Config{
		Addr: "127.0.0.1:0", CA: ca, InterceptHosts: hosts, AllowPlainHTTP: true, InterceptAll: true,
		Handler: func(w http.ResponseWriter, r *http.Request, _ string) {
			key := testresources.RequestKey(r.Method, r.URL.String(), r.Header)
			entry, ok := index[key]
			if !ok {
				http.Error(w, "undeclared test HTTP request: "+key, http.StatusGatewayTimeout)
				return
			}
			if err := testresources.ReplayResponse(w, cacheDir, entry); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
			}
		},
	})
	if err != nil {
		return err
	}
	if err := server.Start(); err != nil {
		return err
	}
	defer server.Stop()
	proxyURL := "http://" + server.Addr()
	caPath := filepath.Join(caDir, "ca.crt")
	env := append(os.Environ(),
		"LOCALAI_TEST_OFFLINE=1", "HTTP_PROXY="+proxyURL, "HTTPS_PROXY="+proxyURL,
		"ALL_PROXY="+proxyURL, "http_proxy="+proxyURL, "https_proxy="+proxyURL,
		"all_proxy="+proxyURL, "SSL_CERT_FILE="+caPath, "CURL_CA_BUNDLE="+caPath,
		"REQUESTS_CA_BUNDLE="+caPath, "GIT_SSL_CAINFO="+caPath, "NODE_EXTRA_CA_CERTS="+caPath,
		"NO_PROXY=localhost,127.0.0.0/8,::1,172.16.0.0/12,192.168.0.0/16",
		"no_proxy=localhost,127.0.0.0/8,::1,172.16.0.0/12,192.168.0.0/16",
		"TESTCONTAINERS_RYUK_DISABLED=true",
	)
	if dockerNetwork != "" {
		env = append(env, "LOCALAI_TEST_DOCKER_NETWORK="+dockerNetwork)
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
