package main

// The image half of the vision path. Strata ships a small resident program,
// strata-vision (llama.cpp's mtmd library plus the model's mmproj file), that
// turns an image into the embeddings the text model reads at its
// <|image_pad|> tokens. The engine, started with --vision, takes those
// embeddings in a GENI request.
//
//	strata-vision --mmproj <file> --model <text model gguf, first shard> [--gpu] [--threads N] [--max-tokens N]
//	  READY <n_embd>
//	  ENC <image file> <output file>  ->  OK <n_tokens> <nx> <ny> <ms>  |  ERR <message>
//	  QUIT
//
// The output file is one record: int32 {'SVE1', n_tokens, nx, ny, n_embd}
// followed by n_tokens x n_embd float32. A GENI request takes the records of
// all its images, in prompt order, concatenated in one file.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/webp"
)

const (
	// maxImageBytes bounds one decoded image, whatever its source.
	maxImageBytes = 32 << 20
	// encodedCacheSize is how many encoded images are kept on disk. A chat
	// client sends all earlier images again with each turn.
	encodedCacheSize = 64
)

type visionConfig struct {
	Exe       string
	MMProj    string
	TextModel string // first shard of the text model: the encoder reads its vocabulary only
	GPU       bool
	Threads   int
	MaxTokens int
	Timeout   time.Duration
	WorkDir   string
}

// imageEncoder drives one strata-vision process. The process starts with the
// first image, so a text-only workload never pays for it.
type imageEncoder struct {
	cfg visionConfig

	mu    sync.Mutex
	cmd   *exec.Cmd
	done  chan struct{} // closed when the process has been reaped
	stdin io.WriteCloser
	out   *bufio.Reader
	dir   string

	cache map[string]encodedImage // by hash of the normalized bytes
	order []string                // oldest first
}

type encodedImage struct {
	file   string
	tokens int
}

func newImageEncoder(cfg visionConfig) *imageEncoder {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	return &imageEncoder{cfg: cfg, cache: map[string]encodedImage{}}
}

func (v *imageEncoder) ensureDirLocked() error {
	if v.dir != "" {
		if st, err := os.Stat(v.dir); err == nil && st.IsDir() {
			return nil
		}
		v.cache, v.order = map[string]encodedImage{}, nil
	}
	d, err := os.MkdirTemp(v.cfg.WorkDir, "strata-vision-")
	if err != nil {
		return err
	}
	// Both the engine and the encoder split their request lines at spaces.
	if strings.ContainsAny(d, " =") {
		_ = os.RemoveAll(d)
		return fmt.Errorf("the scratch directory %q has a space or '=' in its path; set TMPDIR to another directory", d)
	}
	v.dir = d
	return nil
}

func (v *imageEncoder) aliveLocked() bool {
	if v.cmd == nil || v.done == nil {
		return false
	}
	select {
	case <-v.done:
		return false
	default:
		return true
	}
}

func (v *imageEncoder) startLocked() error {
	if v.aliveLocked() {
		return nil
	}
	v.cmd, v.done = nil, nil
	if err := v.ensureDirLocked(); err != nil {
		return err
	}
	args := []string{"--mmproj", v.cfg.MMProj, "--model", v.cfg.TextModel}
	if v.cfg.GPU {
		args = append(args, "--gpu")
	}
	if v.cfg.Threads > 0 {
		args = append(args, "--threads", strconv.Itoa(v.cfg.Threads))
	}
	if v.cfg.MaxTokens > 0 {
		args = append(args, "--max-tokens", strconv.Itoa(v.cfg.MaxTokens))
	}
	exe, err := cleanExecutable(v.cfg.Exe)
	if err != nil {
		return fmt.Errorf("image encoder: %w", err)
	}
	cmd := exec.Command(exe, args...) // #nosec G204 -- absolute regular file from the vision_exe option, no shell, arguments passed as argv
	cmd.Dir = v.dir
	setChildAttrs(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errBuf tailBuffer
	cmd.Stderr = &errBuf
	// The goroutine that starts the child stays on its OS thread until the
	// child is reaped, so PDEATHSIG does not fire early (see supervise).
	started := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		lockThreadForChild()
		err := cmd.Start()
		started <- err
		if err != nil {
			close(done)
			return
		}
		_ = cmd.Wait()
		close(done)
	}()
	if err := <-started; err != nil {
		return fmt.Errorf("start image encoder: %w", err)
	}
	v.cmd, v.done, v.stdin, v.out = cmd, done, in, bufio.NewReaderSize(out, 64*1024)
	line, err := v.readLineLocked("the image encoder did not start", v.cfg.Timeout)
	if err != nil {
		v.stopLocked()
		return fmt.Errorf("%w%s", err, errBuf.suffix())
	}
	if !strings.HasPrefix(line, "READY") {
		v.stopLocked()
		return fmt.Errorf("image encoder said %q instead of READY%s", line, errBuf.suffix())
	}
	return nil
}

// readLineLocked reads one line within the timeout. On a timeout the encoder is
// killed so that the next image starts a fresh one.
func (v *imageEncoder) readLineLocked(what string, timeout time.Duration) (string, error) {
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	out := v.out
	go func() {
		s, err := out.ReadString('\n')
		ch <- res{strings.TrimRight(s, "\r\n"), err}
	}()
	select {
	case r := <-ch:
		if r.err != nil && r.s == "" {
			return "", fmt.Errorf("%s: the encoder exited", what)
		}
		return r.s, nil
	case <-time.After(timeout):
		if v.cmd != nil && v.cmd.Process != nil {
			_ = v.cmd.Process.Kill()
		}
		return "", fmt.Errorf("%s: no answer within %s", what, timeout)
	}
}

func (v *imageEncoder) stopLocked() {
	if v.cmd == nil {
		return
	}
	if v.stdin != nil {
		_, _ = io.WriteString(v.stdin, "QUIT\n")
		_ = v.stdin.Close()
	}
	if v.cmd.Process != nil {
		select {
		case <-v.done:
		case <-time.After(3 * time.Second):
			_ = v.cmd.Process.Kill()
			<-v.done
		}
	}
	v.cmd, v.done, v.stdin, v.out = nil, nil, nil, nil
}

// Close stops the encoder and removes its scratch files.
func (v *imageEncoder) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stopLocked()
	if v.dir != "" {
		_ = os.RemoveAll(v.dir)
		v.dir = ""
	}
	v.cache, v.order = map[string]encodedImage{}, nil
}

// Alive reports whether the encoder process is running.
func (v *imageEncoder) Alive() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.aliveLocked()
}

// Pid returns the process id of the encoder, or 0.
func (v *imageEncoder) Pid() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.aliveLocked() && v.cmd.Process != nil {
		return v.cmd.Process.Pid
	}
	return 0
}

// EncodeAll encodes the images and writes their records, in order, to one
// file. It returns the file, the number of image tokens of each image, and a
// key for each image. The file stays in the encoder's scratch directory until
// release is called.
func (v *imageEncoder) EncodeAll(sources []string) (file string, counts []int, release func(), err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureDirLocked(); err != nil {
		return "", nil, nil, err
	}
	keep := map[string]bool{}
	var parts []string
	for _, src := range sources {
		data, err := loadImage(src)
		if err != nil {
			return "", nil, nil, err
		}
		data, err = normalizeImage(data)
		if err != nil {
			return "", nil, nil, err
		}
		sum := sha256.Sum256(data)
		key := hex.EncodeToString(sum[:16])
		enc, ok := v.cache[key]
		if ok {
			v.touchLocked(key)
		} else {
			enc, err = v.encodeLocked(key, data)
			if err != nil {
				return "", nil, nil, err
			}
		}
		keep[key] = true
		parts = append(parts, enc.file)
		counts = append(counts, enc.tokens)
		v.trimLocked(keep)
	}
	var combined *os.File
	combined, err = os.CreateTemp(v.dir, "req-*.sve")
	if err != nil {
		return "", nil, nil, err
	}
	for _, p := range parts {
		var f *os.File
		var err error
		if filepath.Dir(filepath.Clean(p)) != v.dir {
			err = fmt.Errorf("%q is outside the encoder directory", p)
		} else {
			f, err = os.Open(filepath.Clean(p)) // #nosec G304 -- checked above to be a file inside the encoder's own directory
		}
		if err == nil {
			_, err = io.Copy(combined, f)
			_ = f.Close()
		}
		if err != nil {
			_ = combined.Close()
			_ = os.Remove(combined.Name())
			return "", nil, nil, fmt.Errorf("read encoded image: %w", err)
		}
	}
	if err := combined.Close(); err != nil {
		return "", nil, nil, err
	}
	name := combined.Name()
	return name, counts, func() { _ = os.Remove(name) }, nil
}

func (v *imageEncoder) touchLocked(key string) {
	for i, k := range v.order {
		if k == key {
			v.order = append(append(v.order[:i:i], v.order[i+1:]...), key)
			return
		}
	}
}

func (v *imageEncoder) trimLocked(keep map[string]bool) {
	for len(v.order) > encodedCacheSize {
		dropped := false
		for i, k := range v.order {
			if keep[k] {
				continue
			}
			_ = os.Remove(v.cache[k].file)
			delete(v.cache, k)
			v.order = append(v.order[:i:i], v.order[i+1:]...)
			dropped = true
			break
		}
		if !dropped {
			return
		}
	}
}

func (v *imageEncoder) encodeLocked(key string, data []byte) (encodedImage, error) {
	if err := v.startLocked(); err != nil {
		return encodedImage{}, err
	}
	img := filepath.Join(v.dir, key+".img")
	out := filepath.Join(v.dir, key+".sve")
	if err := os.WriteFile(img, data, 0o600); err != nil {
		return encodedImage{}, err
	}
	defer func() { _ = os.Remove(img) }()
	if _, err := io.WriteString(v.stdin, "ENC "+filepath.Base(img)+" "+filepath.Base(out)+"\n"); err != nil {
		v.stopLocked()
		return encodedImage{}, fmt.Errorf("image encoder: %w", err)
	}
	line, err := v.readLineLocked("the image could not be encoded", v.cfg.Timeout)
	if err != nil {
		v.stopLocked()
		return encodedImage{}, err
	}
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != "OK" {
		msg := strings.TrimSpace(strings.TrimPrefix(line, "ERR"))
		return encodedImage{}, fmt.Errorf("the image could not be encoded: %s", msg)
	}
	n, err := strconv.Atoi(f[1])
	if err != nil || n < 1 {
		return encodedImage{}, fmt.Errorf("image encoder returned %q", line)
	}
	enc := encodedImage{file: out, tokens: n}
	v.cache[key] = enc
	v.order = append(v.order, key)
	return enc, nil
}

// tailBuffer keeps the last bytes written to it, for error messages.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tailBuffer) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := strings.TrimSpace(string(t.b)); s != "" {
		return ": " + s
	}
	return ""
}

// loadImage returns the bytes of an image given as LocalAI sends it: plain
// base64, a data: URL, or an http(s) URL.
func loadImage(src string) ([]byte, error) {
	switch {
	case strings.HasPrefix(src, "data:"):
		_, b64, ok := strings.Cut(src, ",")
		if !ok {
			return nil, errors.New("the image's data: URL has no payload")
		}
		return decodeBase64Image(b64)
	case strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"):
		return downloadImage(src)
	default:
		return decodeBase64Image(src)
	}
}

func decodeBase64Image(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("the image is not valid base64: %w", err)
	}
	if len(b) > maxImageBytes {
		return nil, fmt.Errorf("the image is over %d MiB", maxImageBytes>>20)
	}
	return b, nil
}

func downloadImage(url string) ([]byte, error) {
	c := &http.Client{Timeout: time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, fmt.Errorf("the image URL could not be read: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the image URL answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("the image URL could not be read: %w", err)
	}
	if len(b) > maxImageBytes {
		return nil, fmt.Errorf("the image URL's file is over %d MiB", maxImageBytes>>20)
	}
	return b, nil
}

// normalizeImage passes the formats the encoder's decoder reads (JPEG, PNG,
// BMP, GIF) through and converts anything else (WebP) to PNG. A JPEG with an
// EXIF orientation is not turned upright: the Go decoder does not read it.
func normalizeImage(data []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}),
		bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(data, []byte("BM")),
		bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return data, nil
	}
	var (
		img image.Image
		err error
	)
	switch {
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		img, err = webp.Decode(bytes.NewReader(data))
	default:
		err = errors.New("unknown image format")
	}
	if err != nil {
		return nil, fmt.Errorf("the image could not be read: %w (JPEG, PNG, BMP, GIF and WebP work)", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
