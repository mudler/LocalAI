package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/mudler/xlog"
)

// The BitTorrent library has a web seed client of its own, but it panics on
// torrents whose files are padded to piece boundaries (BEP 47) and hashed per
// file (BEP 52), which is the layout Pirate Face publishes. So the library's
// web seeds are switched off and this filler does the same job: it reads whole
// pieces over HTTP with Range requests, writes them to the torrent's storage
// and lets the library check them against the torrent's own piece hashes. A
// piece that fails the check is thrown away, exactly as a bad piece from a
// peer would be. The swarm keeps running beside it.

const webseedWorkers = 4

// webseedMaxFailures is how many pieces in a row may fail before the filler
// gives up on its web seeds.
const webseedMaxFailures = 4

type webseedFiller struct {
	t     *torrent.Torrent
	info  *metainfo.Info
	files []metainfo.FileInfo
	bases []string
	opts  downloadOptions
	// fetched counts the bytes this filler wrote, for the log line at the end.
	fetched atomic.Int64
}

func newWebseedFiller(t *torrent.Torrent, bases []string, opts downloadOptions) *webseedFiller {
	info := t.Info()
	return &webseedFiller{t: t, info: info, files: info.UpvertedFiles(), bases: bases, opts: opts}
}

// run fills pieces [first, end) until each is complete. It returns nil when no
// piece is left, and an error when every web seed failed.
func (w *webseedFiller) run(ctx context.Context, first, end int) error {
	// A resumed download has data on disk that the library is still hashing.
	// Fetching a piece before its check ends would download it again.
	if err := w.waitForInitialCheck(ctx, first, end); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		next     = first
		failures int
		lastErr  error
		wg       sync.WaitGroup
	)
	claim := func() (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		for next < end {
			i := next
			next++
			if w.t.PieceState(i).Complete {
				continue
			}
			return i, true
		}
		return 0, false
	}
	for range webseedWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				i, ok := claim()
				if !ok {
					return
				}
				err := w.fillPiece(ctx, i)
				mu.Lock()
				if err != nil && ctx.Err() == nil {
					failures++
					lastErr = err
					if failures >= webseedMaxFailures {
						cancel()
					}
				} else if err == nil {
					failures = 0
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if failures >= webseedMaxFailures {
		return fmt.Errorf("web seed failed %d pieces in a row: %w", failures, lastErr)
	}
	return nil
}

func (w *webseedFiller) waitForInitialCheck(ctx context.Context, first, end int) error {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		pending := false
		for i := first; i < end; i++ {
			st := w.t.PieceState(i)
			if !st.Ok || st.Checking || st.QueuedForHash || st.Hashing {
				pending = true
				break
			}
		}
		if !pending {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (w *webseedFiller) pieceLen(i int) int64 {
	pl := w.info.PieceLength
	start := int64(i) * pl
	if w.info.FilesArePieceAligned() {
		for _, f := range w.files {
			if start >= f.TorrentOffset && start < f.TorrentOffset+f.Length {
				return min(pl, f.TorrentOffset+f.Length-start)
			}
		}
		return 0
	}
	return min(pl, w.info.TotalLength()-start)
}

func (w *webseedFiller) fillPiece(ctx context.Context, i int) error {
	n := w.pieceLen(i)
	if n <= 0 {
		return fmt.Errorf("piece %d lies outside the torrent's files", i)
	}
	start := int64(i) * w.info.PieceLength
	buf := make([]byte, n)
	for _, f := range w.files {
		lo, hi := max(start, f.TorrentOffset), min(start+n, f.TorrentOffset+f.Length)
		if lo >= hi {
			continue
		}
		if strings.Contains(f.Attr, "p") {
			continue // BEP 47 padding is zeros and is not served
		}
		if err := w.fetchRange(ctx, f, lo-f.TorrentOffset, buf[lo-start:hi-start]); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.t.PieceState(i).Complete {
		return nil
	}
	if _, err := w.t.Piece(i).Storage().WriteAt(buf, 0); err != nil {
		return fmt.Errorf("writing piece %d: %w", i, err)
	}
	if err := w.t.Piece(i).VerifyDataContext(ctx); err != nil {
		return err
	}
	if w.t.PieceState(i).Complete {
		w.fetched.Add(n)
	} else {
		return fmt.Errorf("piece %d from the web seed does not match the torrent's hash", i)
	}
	return nil
}

// fetchRange reads len(dst) bytes of file f starting at off into dst, trying
// each web seed in turn.
func (w *webseedFiller) fetchRange(ctx context.Context, f metainfo.FileInfo, off int64, dst []byte) error {
	var errs []error
	for _, base := range w.bases {
		u, ok := webseedFileURL(base, w.info, f)
		if !ok {
			continue
		}
		err := httpRangeInto(ctx, u, off, dst, w.opts.bearerToken)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return errors.New("no usable web seed URL for this torrent")
	}
	return errors.Join(errs...)
}

// webseedFileURL builds the URL of one file under a web seed (BEP 19). A base
// that ends in "/" is a directory holding the torrent's name; any other base
// is the file itself and only fits a single-file torrent.
func webseedFileURL(base string, info *metainfo.Info, f metainfo.FileInfo) (string, bool) {
	if !strings.HasSuffix(base, "/") {
		return base, !info.IsDir()
	}
	var segs []string
	if name := info.BestName(); name != "" && name != metainfo.NoName {
		segs = append(segs, name)
	}
	if info.IsDir() {
		segs = append(segs, f.BestPath()...)
	}
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return base + strings.Join(segs, "/"), true
}

func httpRangeInto(ctx context.Context, u string, off int64, dst []byte, bearer string) error {
	req, err := newDownloadRequest(ctx, http.MethodGet, u, bearer)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(dst))-1))
	resp, err := downloadHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK && off == 0:
		// A server that ignores Range sends the whole file from the start,
		// which is the part wanted here.
	default:
		return fmt.Errorf("web seed %s: status %d", u, resp.StatusCode)
	}
	if _, err := io.ReadFull(resp.Body, dst); err != nil {
		return fmt.Errorf("web seed %s: reading %d bytes at %d: %w", u, len(dst), off, err)
	}
	return nil
}

func logWebseedStop(err error) {
	xlog.Warn("web seed gave up, continuing with peers only", "error", err)
}
