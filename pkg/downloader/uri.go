package downloader

import (
	"context"
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

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAI/pkg/xio"
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

// ImageVerifier verifies the integrity of an OCI image — typically a
// cosign signature check against a sigstore policy. The downloader runs
// VerifyImage between fetching the image manifest and extracting its
// layers, so verification failure prevents any tampered bytes reaching
// disk.
//
// pkg/oci/cosignverify.Verifier satisfies this interface.
type ImageVerifier interface {
	VerifyImage(ctx context.Context, imageRef string) error
}

// TransferProgress reports the raw byte counts for an HTTP download.
// Total is negative when the server does not advertise a response length.
type TransferProgress struct {
	FileName string
	Written  int64
	Total    int64
}

// TransferProgressSink receives raw byte progress updates for an HTTP download.
type TransferProgressSink func(TransferProgress)

type downloadOptions struct {
	verifier         ImageVerifier
	bearerToken      string
	transferProgress TransferProgressSink
}

// DownloadOption configures DownloadFileWithContext / DownloadFile.
//
// Variadic at the end of the signature keeps the public API backward
// compatible: existing callers that don't care about verification keep
// compiling untouched.
type DownloadOption func(*downloadOptions)

// WithImageVerifier attaches an ImageVerifier that runs against OCI
// downloads only. No-op for tarball / HTTP / Ollama / local downloads —
// those paths use SHA256 integrity instead.
func WithImageVerifier(v ImageVerifier) DownloadOption {
	return func(o *downloadOptions) { o.verifier = v }
}

// WithBearerToken authenticates HTTP download requests with a bearer token.
// The token is stripped if a request redirects to a different origin.
func WithBearerToken(token string) DownloadOption {
	return func(o *downloadOptions) { o.bearerToken = token }
}

// WithTransferProgress attaches a sink for raw HTTP download byte progress.
func WithTransferProgress(sink TransferProgressSink) DownloadOption {
	return func(o *downloadOptions) { o.transferProgress = sink }
}

func applyDownloadOptions(opts []DownloadOption) downloadOptions {
	var o downloadOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// pinnedImageRef rewrites `repo:tag` (or `repo[@digest]`) into `repo@<digest>`
// so callers can pass the explicit digest the downloader just resolved to
// any tag-following client, eliminating TOCTOU between fetches.
func pinnedImageRef(ref, digest string) string {
	// Strip an existing @digest if present so we always emit a clean ref.
	if at := strings.LastIndex(ref, "@"); at != -1 {
		// Only treat as a digest separator when not preceded by a slash
		// (avoids breaking unusual hostnames). Conservative: just keep
		// the registry+repo portion.
		ref = ref[:at]
	}
	// Strip an existing :tag — find the rightmost colon after the last
	// slash so we don't touch the registry port (e.g. localhost:5000/foo:latest).
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		ref = ref[:colon]
	}
	return ref + "@" + digest
}

// HF_ENDPOINT is the HuggingFace endpoint, can be overridden by setting the HF_ENDPOINT environment variable.
var HF_ENDPOINT string = loadConfig()

// loadConfig returns the HuggingFace endpoint URL.
// It supports the following environment variables in order of precedence:
// 1. HF_MIRROR - if set, uses this as the mirror URL (takes precedence over HF_ENDPOINT)
// 2. HF_ENDPOINT - if set, uses this as the endpoint
// 3. Default: https://huggingface.co
//
// HF_MIRROR supports both full URLs (https://hf-mirror.com) and simple hostnames (hf-mirror.com).
// If no scheme is provided, https:// is automatically added.
func loadConfig() string {
	// Check for HF_MIRROR first (takes precedence)
	HF_MIRROR := os.Getenv("HF_MIRROR")
	if HF_MIRROR == "" {
		HF_MIRROR = os.Getenv("HF")
	}
	if HF_MIRROR != "" {
		// Normalize the mirror URL - add https:// if no scheme
		if !strings.HasPrefix(HF_MIRROR, "http://") && !strings.HasPrefix(HF_MIRROR, "https://") {
			HF_MIRROR = "https://" + HF_MIRROR
		}
		return HF_MIRROR
	}

	// Fall back to HF_ENDPOINT
	HF_ENDPOINT := os.Getenv("HF_ENDPOINT")
	if HF_ENDPOINT == "" {
		HF_ENDPOINT = "https://huggingface.co"
	}
	return HF_ENDPOINT
}

func (uri URI) ReadWithCallback(basePath string, f func(url string, i []byte) error) error {
	return uri.ReadWithAuthorizationAndCallback(context.Background(), basePath, "", f)
}

func (uri URI) ReadWithAuthorizationAndCallback(ctx context.Context, basePath string, authorization string, f func(url string, i []byte) error) error {
	url := uri.ResolveURL()

	if strings.HasPrefix(string(uri), LocalPrefix) {
		// checks if the file is symbolic, and resolve if so - otherwise, this function returns the path unmodified.
		resolvedFile, err := filepath.EvalSymlinks(url)
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
			xlog.Debug("downloader.GetURI blocked an attempt to ready a file url outside of basePath", "resolvedFile", resolvedFile, "basePath", basePath)
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
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Add("Authorization", authorization)
	}

	response, err := downloadClient.Do(req)
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
	if f := filenameFromUrl(string(u)); f != "" {
		return f, nil
	}

	f := utils.MD5(string(u))
	if strings.HasSuffix(string(u), ".yaml") || strings.HasSuffix(string(u), ".yml") {
		f = f + ".yaml"
	}

	return f, nil
}

func filenameFromUrl(urlstr string) string {
	// strip anything after @
	if strings.Contains(urlstr, "@") {
		urlstr = strings.Split(urlstr, "@")[0]
	}

	u, err := url.Parse(urlstr)
	if err != nil {
		return ""
	}
	x, err := url.QueryUnescape(u.EscapedPath())
	if err != nil {
		return ""
	}
	return filepath.Base(x)
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

func (s URI) LooksLikeOCIFile() bool {
	return strings.HasPrefix(string(s), OCIFilePrefix)
}

func (s URI) ResolveURL() string {
	switch {
	case strings.HasPrefix(string(s), LocalPrefix):
		return strings.TrimPrefix(string(s), LocalPrefix)
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

		repoPieces := strings.Split(repository, "/")
		repoID := strings.Split(repository, "@")
		if len(repoPieces) < 3 {
			return string(s)
		}

		owner := repoPieces[0]
		repo := repoPieces[1]

		branch := "main"
		filepath := strings.Join(repoPieces[2:], "/")

		if len(repoID) > 1 {
			if strings.Contains(repo, "@") {
				branch = repoID[1]
			}
			if strings.Contains(filepath, "@") {
				filepath = repoID[2]
			}
		}

		return fmt.Sprintf("%s/%s/%s/resolve/%s/%s", HF_ENDPOINT, owner, repo, branch, filepath)
	}

	// If a HuggingFace mirror is configured, rewrite direct https://huggingface.co/ URLs
	// to use the mirror. This ensures gallery entries with hardcoded URLs also benefit
	// from the mirror setting.
	if HF_ENDPOINT != "https://huggingface.co" && strings.HasPrefix(string(s), "https://huggingface.co/") {
		return HF_ENDPOINT + strings.TrimPrefix(string(s), "https://huggingface.co")
	}

	return string(s)
}

// ErrUserCancelled distinguishes a deliberate user abort from an incidental
// context cancellation (process shutdown, pod restart). Pass it as the cause
// when cancelling the download context:
//
//	ctx, cancel := context.WithCancelCause(parent)
//	cancel(downloader.ErrUserCancelled) // discards the .partial
//
// On a deliberate cancel the downloader removes the .partial (the user does not
// want a half-download lingering). On a plain cancellation it keeps the .partial
// so the next run resumes via Range instead of restarting from zero.
var ErrUserCancelled = errors.New("download cancelled by user")

func removePartialFile(tmpFilePath string) error {
	xlog.Debug("Removing temporary file", "file", tmpFilePath)
	if err := os.Remove(tmpFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		err1 := fmt.Errorf("failed to remove temporary download file %s: %v", tmpFilePath, err)
		xlog.Warn("failed to remove temporary download file", "error", err1)
		return err1
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

// downloadClient is the shared client for HTTP(S) downloads and size
// probes. It follows redirects (model hosts and CDNs rely on them) but
// strips credential headers on any cross-host hop, and sets no body
// deadline so large downloads are not truncated.
var downloadClient = httpclient.New(httpclient.WithFollowRedirects())

func newDownloadRequest(
	ctx context.Context,
	method string,
	rawURL string,
	bearerToken string,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return req, nil
}

func (uri URI) checkServerSupportsRangeHeader(ctx context.Context, bearerToken string) (bool, error) {
	req, err := newDownloadRequest(ctx, http.MethodHead, uri.ResolveURL(), bearerToken)
	if err != nil {
		return false, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Accept-Ranges") == "bytes", nil
}

// ContentLength returns the size in bytes of the resource at the URI.
// For file:// it uses os.Stat on the resolved path; for HTTP/HTTPS it uses HEAD
// and optionally a Range request if Content-Length is missing.
func (u URI) ContentLength(ctx context.Context) (int64, error) {
	urlStr := u.ResolveURL()
	if strings.HasPrefix(string(u), LocalPrefix) {
		info, err := os.Stat(urlStr)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	if !strings.HasPrefix(urlStr, HTTPPrefix) && !strings.HasPrefix(urlStr, HTTPSPrefix) {
		return 0, fmt.Errorf("unsupported URI scheme for ContentLength: %s", string(u))
	}
	req, err := http.NewRequestWithContext(ctx, "HEAD", urlStr, nil)
	if err != nil {
		return 0, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HEAD %s: status %d", urlStr, resp.StatusCode)
	}
	if resp.ContentLength >= 0 {
		return resp.ContentLength, nil
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		return 0, fmt.Errorf("HEAD %s: no Content-Length and server does not support Range", urlStr)
	}
	req2, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return 0, err
	}
	req2.Header.Set("Range", "bytes=0-0")
	resp2, err := downloadClient.Do(req2)
	if err != nil {
		return 0, err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusPartialContent && resp2.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Range request %s: status %d", urlStr, resp2.StatusCode)
	}
	cr := resp2.Header.Get("Content-Range")
	// Content-Range: bytes 0-0/12345
	if cr == "" {
		return 0, fmt.Errorf("Range request %s: no Content-Range header", urlStr)
	}
	parts := strings.Split(cr, "/")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid Content-Range: %s", cr)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("invalid Content-Range total length: %s", parts[1])
	}
	return size, nil
}

func (uri URI) DownloadFile(filePath, sha string, fileN, total int, downloadStatus func(string, string, string, float64), opts ...DownloadOption) error {
	return uri.DownloadFileWithContext(context.Background(), filePath, sha, fileN, total, downloadStatus, opts...)
}

func (uri URI) DownloadFileWithContext(ctx context.Context, filePath, sha string, fileN, total int, downloadStatus func(string, string, string, float64), opts ...DownloadOption) error {
	dopts := applyDownloadOptions(opts)
	url := uri.ResolveURL()
	if uri.LooksLikeOCI() {

		// Only Ollama wants to download to the file, for the rest, we want to download to the directory
		// so we check if filepath has any extension, otherwise we assume it's a directory.
		// Caveat: `filepath.Ext` treats any dot-suffix as an extension, so paths like
		// `backends/local-store.upgrade-tmp` (the tmp dir created by gallery.UpgradeBackend)
		// look like a "file" to this heuristic and get rewritten to their parent — which
		// then unpacks the image at `backends/` top level and clobbers the real install
		// with a flat-layout file. Guard against that by short-circuiting when the caller
		// has already created the target as a directory: OCI destinations are always dirs
		// in that case, regardless of what their suffix looks like.
		if !strings.HasPrefix(url, OllamaPrefix) {
			if fi, statErr := os.Stat(filePath); statErr == nil && fi.IsDir() {
				// Existing directory — use as-is.
			} else if filepath.Ext(filePath) != "" {
				filePath = filepath.Dir(filePath)
			}
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
			return oci.OllamaFetchModel(ctx, url, filePath, progressStatus)
		}

		if url, ok := strings.CutPrefix(url, OCIFilePrefix); ok {
			// Open the tarball
			img, err := tarball.ImageFromPath(url, nil)
			if err != nil {
				return fmt.Errorf("failed to open tarball: %s", err.Error())
			}

			return oci.ExtractOCIImage(ctx, img, url, filePath, downloadStatus)
		}

		url = strings.TrimPrefix(url, OCIPrefix)
		img, err := oci.GetImage(url, "", nil, nil)
		if err != nil {
			return fmt.Errorf("failed to get image %q: %v", url, err)
		}

		// Verify before extract so tampered bytes never reach disk. We
		// re-pin the ref to the manifest digest we just fetched: the
		// verifier would otherwise resolve the tag again, opening a tiny
		// TOCTOU window in which a registry could swap the underlying
		// manifest between the two HEADs.
		if dopts.verifier != nil {
			digest, derr := img.Digest()
			if derr != nil {
				return fmt.Errorf("resolving digest for verification of %q: %v", url, derr)
			}
			pinned := pinnedImageRef(url, digest.String())
			if verr := dopts.verifier.VerifyImage(ctx, pinned); verr != nil {
				return fmt.Errorf("image verification failed for %q: %w", url, verr)
			}
			xlog.Info("Image signature verified", "ref", pinned)
		}

		return oci.ExtractOCIImage(ctx, img, url, filePath, downloadStatus)
	}

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check if the file already exists
	fi, err := os.Stat(filePath)
	if err == nil {
		// Directories don't count as cached downloads (e.g. empty dirs left
		// by failed OCI extractions). Only skip for regular files.
		if fi.IsDir() {
			xlog.Debug("[downloader] Path is a directory, not treating as cached download", "filePath", filePath)
		} else {
			xlog.Debug("[downloader] File already exists", "filePath", filePath)
			// File exists, check SHA
			if sha != "" {
				// Verify SHA
				calculatedSHA, err := CalculateSHA(filePath)
				if err != nil {
					return fmt.Errorf("failed to calculate SHA for file %q: %v", filePath, err)
				}
				if calculatedSHA == sha {
					// SHA matches, skip downloading
					xlog.Debug("File already exists and matches the SHA. Skipping download", "file", filePath)
					return nil
				}
				// SHA doesn't match, delete the file and download again
				err = os.Remove(filePath)
				if err != nil {
					return fmt.Errorf("failed to remove existing file %q: %v", filePath, err)
				}
				xlog.Debug("Removed file (SHA doesn't match)", "file", filePath)
			} else {
				// SHA is missing, skip downloading
				xlog.Debug("File already exists. Skipping download", "file", filePath)
				return nil
			}
		}
	} else if !os.IsNotExist(err) || !URI(url).LooksLikeHTTPURL() {
		// Error occurred while checking file existence
		return fmt.Errorf("could not fetch %q: local file does not exist (%v) and %q is not a recognized downloadable URL (supported schemes: %s)", filePath, err, url, strings.Join([]string{HTTPPrefix, HTTPSPrefix, LocalPrefix, HuggingFacePrefix, HuggingFacePrefix1, OllamaPrefix, OCIPrefix, OCIFilePrefix, GithubURI2}, ", "))
	}

	xlog.Info("Downloading", "url", url)

	req, err := newDownloadRequest(ctx, http.MethodGet, url, dopts.bearerToken)
	if err != nil {
		return fmt.Errorf("failed to create request for %q: %v", filePath, err)
	}

	// save partial download to dedicated file
	tmpFilePath := filePath + ".partial"
	var startPos int64
	tmpFileInfo, statErr := os.Stat(tmpFilePath)
	switch {
	case statErr == nil:
		// A leftover partial is only usable when we can ask the server to
		// continue from where it stopped. Resume is probed only for raw
		// http(s) URIs; every other transport (local files, and schemes we do
		// not probe) has to restart, because the writer opens the partial with
		// O_APPEND and would otherwise concatenate a fresh full body onto the
		// stale bytes. Discarding here is what makes a retry after an
		// interrupted download recover on its own instead of failing forever.
		resumable := false
		if uri.LooksLikeHTTPURL() {
			support, err := uri.checkServerSupportsRangeHeader(ctx, dopts.bearerToken)
			if err != nil {
				return fmt.Errorf("failed to check if uri server supports range header: %v", err)
			}
			resumable = support
		}
		if resumable {
			startPos = tmpFileInfo.Size()
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startPos))
		} else if err := removePartialFile(tmpFilePath); err != nil {
			return err
		}
	case errors.Is(statErr, os.ErrNotExist):
		// Nothing to resume or discard: this is a fresh download.
	default:
		return fmt.Errorf("failed to check partial download file %q: %w", tmpFilePath, statErr)
	}

	var source io.ReadCloser
	var contentLength int64
	if _, e := os.Stat(uri.ResolveURL()); strings.HasPrefix(string(uri), LocalPrefix) || e == nil {
		file, err := os.Open(uri.ResolveURL())
		if err != nil {
			return fmt.Errorf("failed to open file %q: %v", uri.ResolveURL(), err)
		}
		l, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to get file size %q: %v", uri.ResolveURL(), err)
		}
		source = file
		contentLength = l.Size()
	} else {
		// Start the request
		resp, err := downloadClient.Do(req)
		if err != nil {
			// Detect cancellation via the context, not the returned error: a
			// request cancelled *with a cause* surfaces the cause error (not
			// context.Canceled) from the HTTP client. Keep the .partial for
			// resume on an incidental cancel (shutdown, restart) — large GGUFs
			// take long enough that deleting progress means they never finish —
			// but discard it on a deliberate user abort (ErrUserCancelled).
			if ctx.Err() != nil {
				if errors.Is(context.Cause(ctx), ErrUserCancelled) {
					_ = removePartialFile(tmpFilePath)
				}
				return ctx.Err()
			}
			// The transport failed before the response was established (reset
			// connection, refused dial, TLS hiccup). Nothing about it is
			// specific to this URL, so another attempt may well succeed.
			return asTransient(fmt.Errorf("failed to download file %q: %v", filePath, err))
		}
		//defer resp.Body.Close()

		if startPos > 0 && resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			_ = removePartialFile(tmpFilePath)
			// The partial has just been discarded, so a further attempt starts
			// clean and no longer needs the server to honour the range.
			return asTransient(fmt.Errorf(
				"resume request for %q returned status %d instead of 206",
				filePath,
				resp.StatusCode,
			))
		}
		if resp.StatusCode >= 400 {
			err := fmt.Errorf("failed to download url %q, invalid status code %d", url, resp.StatusCode)
			// 5xx and 429 describe the server's current state, not the request;
			// every other 4xx (missing file, bad auth) is settled and retrying
			// it only delays the real error.
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
				return asTransient(err)
			}
			return err
		}
		source = resp.Body
		// Guard against a silently-stalled stream: a dropped TCP connection
		// that never sends FIN/RST would otherwise block the body Read (and
		// thus the whole install) forever. The watchdog aborts after a window
		// of zero progress; the .partial is kept for a later resume.
		if DownloadStallTimeout > 0 {
			source = newIdleTimeoutReader(resp.Body, DownloadStallTimeout)
		}
		contentLength = resp.ContentLength + startPos
	}
	defer source.Close()

	// Create parent directory
	err = os.MkdirAll(filepath.Dir(filePath), 0750)
	if err != nil {
		return fmt.Errorf("failed to create parent directory for file %q: %v", filePath, err)
	}

	// Create and write file
	outFile, err := os.OpenFile(tmpFilePath, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("failed to create / open file %q: %v", tmpFilePath, err)
	}
	defer outFile.Close()
	if err := outFile.Chmod(0600); err != nil {
		return fmt.Errorf("failed to restrict partial file %q permissions: %v", tmpFilePath, err)
	}
	hash, err := calculateHashForPartialFile(outFile)
	if err != nil {
		return fmt.Errorf("failed to calculate hash for partial file")
	}
	progress := &progressWriter{
		fileName:       tmpFilePath,
		total:          contentLength,
		hash:           hash,
		fileNo:         fileN,
		totalFiles:     total,
		written:        startPos,
		downloadStatus: downloadStatus,
		transferSink:   dopts.transferProgress,
		ctx:            ctx,
	}

	// io.Copy reports read and write failures indistinguishably, so the source
	// is wrapped to record which side actually broke. Labelling a peer-cancelled
	// HTTP/2 stream "failed to write file" once sent an incident investigation
	// after filesystem permissions while the disk was perfectly healthy.
	tracked := &readErrorRecorder{r: source}
	_, err = xio.Copy(ctx, io.MultiWriter(outFile, progress), tracked)
	if err != nil {
		// Detect cancellation via the context (a cause-cancelled read surfaces
		// the cause, not context.Canceled). Keep the .partial for resume,
		// except on a deliberate user abort (ErrUserCancelled), which discards
		// it. A stall-guard abort leaves ctx uncancelled, so it falls through
		// to the error path below and likewise preserves the partial.
		if ctx.Err() != nil {
			if errors.Is(context.Cause(ctx), ErrUserCancelled) {
				_ = removePartialFile(tmpFilePath)
			}
			return ctx.Err()
		}
		if readErr := tracked.err; readErr != nil && errors.Is(err, readErr) {
			// The source died mid-transfer (peer cancelled the stream, the
			// connection dropped, the stall guard fired). The bytes already on
			// disk are valid, so the .partial is kept and the failure is
			// retryable from where it stopped.
			return asTransient(fmt.Errorf("failed to read %q while downloading to %q: %v", url, tmpFilePath, readErr))
		}
		// A genuine local write failure: no space, bad permissions, a broken
		// mount. Retrying writes the same bytes to the same broken target, so
		// this stays permanent. Name the partial, which is the file actually
		// being written, rather than the final blob path.
		return fmt.Errorf("failed to write file %q: %v", tmpFilePath, err)
	}

	// Check for cancellation before finalizing. Keep the .partial for resume
	// unless the user deliberately aborted.
	select {
	case <-ctx.Done():
		if errors.Is(context.Cause(ctx), ErrUserCancelled) {
			_ = removePartialFile(tmpFilePath)
		}
		return ctx.Err()
	default:
	}

	// Invariant: verify the streamed hash before promoting the temp file to
	// the final path. Renaming first would leave tampered content reachable
	// to subsequent readers even though we return an error.
	if sha != "" {
		calculatedSHA := fmt.Sprintf("%x", progress.hash.Sum(nil))
		if calculatedSHA != sha {
			xlog.Debug("SHA mismatch for file", "file", filePath, "calculated", calculatedSHA, "metadata", sha)
			_ = removePartialFile(tmpFilePath)
			return fmt.Errorf("SHA mismatch for file %q ( calculated: %s != metadata: %s )", filePath, calculatedSHA, sha)
		}
	} else {
		// Visible at the default log level so missing-digest configs are
		// noticed; silent acceptance was the historical bug.
		xlog.Warn("downloading without integrity check — supplied SHA is empty",
			"file", filePath,
			"url", url,
		)
	}

	err = os.Rename(tmpFilePath, filePath)
	if err != nil {
		return fmt.Errorf("failed to rename temporary file %s -> %s: %v", tmpFilePath, filePath, err)
	}

	xlog.Info("File downloaded and verified", "file", filePath)
	if utils.IsArchive(filePath) {
		basePath := filepath.Dir(filePath)
		xlog.Info("File is an archive, uncompressing", "file", filePath, "basePath", basePath)
		if err := utils.ExtractArchive(filePath, basePath); err != nil {
			xlog.Debug("Failed decompressing", "file", filePath, "error", err)
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

func CalculateSHA(filePath string) (string, error) {
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
