package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/utils"
)

// Torrent transfer modes.
const (
	// TorrentModeSwarm fetches from web seeds and from BitTorrent peers at the
	// same time. Peers come from the trackers of the magnet link and from
	// peers it names; DHT is a separate opt-in.
	TorrentModeSwarm = "swarm"
	// TorrentModeWebseed uses HTTP web seeds only. It opens no listening
	// socket, no DHT socket and no tracker or peer connection, so it behaves
	// like an HTTP download with BitTorrent piece verification.
	TorrentModeWebseed = "webseed"
)

// TorrentSettings control how magnet, .torrent and pirateface:// downloads use
// the network. Defaults come from the environment, read once at start-up;
// tests and embedders may assign TorrentConfig.
type TorrentSettings struct {
	// Mode is TorrentModeSwarm (default) or TorrentModeWebseed.
	// CLI: --torrent-mode, env LOCALAI_TORRENT_MODE.
	Mode string
	// ListenPort is the TCP and uTP port for incoming peers. Zero, the default,
	// keeps the client reachable on the loopback interface only: LocalAI
	// still dials out to peers but accepts nothing from the network, and asks
	// no router to forward a port. A positive port listens on all interfaces
	// and lets the client ask the router (UPnP and NAT-PMP) to forward it.
	// CLI: --torrent-listen-port, env LOCALAI_TORRENT_LISTEN_PORT.
	ListenPort int
	// DHT turns on the distributed hash table, which opens a UDP socket and
	// finds peers for magnet links that name no tracker. Off by default.
	// CLI: --torrent-dht, env LOCALAI_TORRENT_DHT.
	DHT bool
	// MetadataTimeout bounds the wait for the torrent's file list when it has
	// to come from peers (a magnet link with no .torrent source).
	// CLI: --torrent-metadata-timeout, env LOCALAI_TORRENT_METADATA_TIMEOUT.
	MetadataTimeout time.Duration
	// Fallback lets a download that fails over BitTorrent retry from Hugging
	// Face when the source is known to mirror a Hugging Face revision (Pirate
	// Face). CLI: --torrent-fallback, env LOCALAI_TORRENT_FALLBACK, default true.
	Fallback bool
}

// DefaultTorrentSettings is the configuration LocalAI starts with. The CLI
// replaces TorrentConfig with the user's flags and environment at start-up.
func DefaultTorrentSettings() TorrentSettings {
	return TorrentSettings{
		Mode:            TorrentModeSwarm,
		MetadataTimeout: 90 * time.Second,
		Fallback:        true,
	}
}

// TorrentConfig is the active BitTorrent configuration.
var TorrentConfig = DefaultTorrentSettings()

// ErrNoPeers means no peer or web seed delivered the torrent's file list in
// time, so nothing could be downloaded.
var ErrNoPeers = errors.New("no peers or web seeds found")

// ErrTorrentFileSelection means a multi-file torrent was given with nothing
// that says which file to download, or with a selector that matches none or
// several.
var ErrTorrentFileSelection = errors.New("cannot choose a file in the torrent")

// maxTorrentFileBytes bounds a .torrent file read into memory.
const maxTorrentFileBytes = maxAPIResponseBytes

// ---- client lifecycle ------------------------------------------------------

var sharedTorrent struct {
	sync.Mutex
	client *torrent.Client
	refs   int
}

// torrentLocks serialises downloads of one torrent: two files of the same
// torrent share its pieces and its info hash, which a client holds only once.
var torrentLocks sync.Map // hex info hash -> *sync.Mutex

func lockTorrent(hash string) func() {
	v, _ := torrentLocks.LoadOrStore(hash, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// newTorrentClientConfig builds the client configuration for the settings. It
// is separate from the client so a test can check the network posture without
// opening sockets.
func newTorrentClientConfig(s TorrentSettings, scratch string) *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	// Every torrent is added with its own storage. This default only stops the
	// library from creating a database in the working directory.
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   scratch,
		PieceCompletion: storage.NewMapPieceCompletion(),
		UsePartFiles:    g.Some(false),
		DisableMmap:     true,
	})
	cfg.Seed = false
	// Web seeds are served by webseedFiller, see torrent_webseed.go.
	cfg.DisableWebseeds = true
	cfg.DisablePEX = false
	cfg.NoDefaultPortForwarding = true
	cfg.NoDHT = !s.DHT
	cfg.ListenPort = 0
	cfg.ListenHost = loopbackHost

	switch {
	case s.Mode == TorrentModeWebseed:
		cfg.DisableTCP = true
		cfg.DisableUTP = true
		cfg.NoDHT = true
		cfg.DisableTrackers = true
		cfg.DisablePEX = true
		cfg.DisableIPv6 = true
	case s.ListenPort > 0:
		cfg.ListenPort = s.ListenPort
		cfg.ListenHost = func(string) string { return "" }
		cfg.NoDefaultPortForwarding = false
	}
	return cfg
}

// loopbackHost binds each network family to its own loopback address.
func loopbackHost(network string) string {
	if strings.HasSuffix(network, "6") {
		return "::1"
	}
	return "127.0.0.1"
}

func acquireTorrentClient(s TorrentSettings) (*torrent.Client, func(), error) {
	sharedTorrent.Lock()
	defer sharedTorrent.Unlock()
	if sharedTorrent.client == nil {
		scratch, err := os.MkdirTemp("", "localai-torrent-*")
		if err != nil {
			return nil, nil, err
		}
		cfg := newTorrentClientConfig(s, scratch)
		cl, err := torrent.NewClient(cfg)
		if err != nil {
			_ = os.RemoveAll(scratch)
			return nil, nil, fmt.Errorf("starting BitTorrent client: %w", err)
		}
		sharedTorrent.client = cl
		xlog.Info("BitTorrent client started", "mode", s.Mode, "listen", cl.ListenAddrs(), "dht", s.DHT && s.Mode != TorrentModeWebseed)
		go func() {
			<-cl.Closed()
			_ = os.RemoveAll(scratch)
		}()
	}
	sharedTorrent.refs++
	cl := sharedTorrent.client
	return cl, func() {
		sharedTorrent.Lock()
		defer sharedTorrent.Unlock()
		sharedTorrent.refs--
		if sharedTorrent.refs == 0 {
			sharedTorrent.client.Close()
			sharedTorrent.client = nil
		}
	}, nil
}

// ---- torrent contents ------------------------------------------------------

type torrentEntry struct {
	// Path is the file's path inside the torrent, with "/" separators and
	// without the torrent's top-level directory name.
	Path   string
	Length int64
	Offset int64
	Pad    bool
}

// torrentEntries lists the files of a torrent and refuses a torrent whose
// names could place data outside its target directory.
func torrentEntries(info *metainfo.Info) ([]torrentEntry, error) {
	if !info.IsDir() {
		name := info.BestName()
		if err := checkTorrentName(name); err != nil {
			return nil, err
		}
		return []torrentEntry{{Path: name, Length: info.Length}}, nil
	}
	if name := info.BestName(); name != metainfo.NoName && name != "" {
		if err := checkTorrentName(name); err != nil {
			return nil, err
		}
	}
	var out []torrentEntry
	for _, fi := range info.UpvertedFiles() {
		segs := fi.BestPath()
		for _, s := range segs {
			if s == "" || strings.Contains(s, "/") {
				return nil, fmt.Errorf("torrent contains an unsafe file name %q", strings.Join(segs, "/"))
			}
		}
		p := strings.Join(segs, "/")
		if err := checkTorrentName(p); err != nil {
			return nil, err
		}
		out = append(out, torrentEntry{Path: p, Length: fi.Length, Offset: fi.TorrentOffset, Pad: strings.Contains(fi.Attr, "p")})
	}
	return out, nil
}

func checkTorrentName(p string) error {
	if err := checkRelativePath(p); err != nil {
		return fmt.Errorf("torrent contains an unsafe file name: %w", err)
	}
	return nil
}

// selectTorrentFile picks the one file a LocalAI file entry wants. The order is
// the "#selector" of the URI, a single BEP 53 "so=" index, the destination
// path of the entry, and finally the only file of a single-file torrent.
func selectTorrentFile(entries []torrentEntry, selector string, so []int, dest string) (torrentEntry, error) {
	real := make([]torrentEntry, 0, len(entries))
	for _, e := range entries {
		if !e.Pad {
			real = append(real, e)
		}
	}
	if len(real) == 0 {
		return torrentEntry{}, fmt.Errorf("%w: the torrent has no files", ErrTorrentFileSelection)
	}
	pick := func(match func(torrentEntry) bool) []torrentEntry {
		var m []torrentEntry
		for _, e := range real {
			if match(e) {
				m = append(m, e)
			}
		}
		return m
	}
	if selector != "" {
		sel := strings.Trim(selector, "/")
		if m := pick(func(e torrentEntry) bool { return e.Path == sel }); len(m) == 1 {
			return m[0], nil
		}
		m := pick(func(e torrentEntry) bool { return strings.HasSuffix(e.Path, "/"+sel) })
		if len(m) == 1 {
			return m[0], nil
		}
		return torrentEntry{}, fmt.Errorf("%w: selector %q matches %d files; the torrent has %s", ErrTorrentFileSelection, selector, len(m), describeEntries(real))
	}
	if len(so) == 1 && so[0] >= 0 && so[0] < len(entries) && !entries[so[0]].Pad {
		return entries[so[0]], nil
	}
	if len(real) == 1 {
		return real[0], nil
	}
	d := filepath.ToSlash(dest)
	if m := pick(func(e torrentEntry) bool { return d == e.Path || strings.HasSuffix(d, "/"+e.Path) }); len(m) >= 1 {
		best := m[0]
		for _, e := range m[1:] {
			if len(e.Path) > len(best.Path) {
				best = e
			}
		}
		return best, nil
	}
	base := filepath.Base(d)
	if m := pick(func(e torrentEntry) bool { return filepath.Base(e.Path) == base }); len(m) == 1 {
		return m[0], nil
	}
	return torrentEntry{}, fmt.Errorf("%w: it has %d files and nothing says which one; add \"#<path in torrent>\" to the URI; the torrent has %s", ErrTorrentFileSelection, len(real), describeEntries(real))
}

func describeEntries(es []torrentEntry) string {
	const max = 8
	var names []string
	for i, e := range es {
		if i == max {
			names = append(names, fmt.Sprintf("... (%d more)", len(es)-max))
			break
		}
		names = append(names, e.Path)
	}
	return strings.Join(names, ", ")
}

// ---- resolving a source ----------------------------------------------------

type torrentPlan struct {
	infoHash string // lowercase hex; "" when only a v2 hash is known
	metainfo *metainfo.MetaInfo
	magnet   *Magnet
	selector string
	// sha256 comes from the Pirate Face index when the entry has none.
	sha256 string
	// hf names the Hugging Face repository and revision the source mirrors.
	hfOwner, hfRepo, hfRevision string
}

func (p *torrentPlan) fallbackFor(path string) URI {
	if p.hfOwner == "" || path == "" {
		return ""
	}
	return URI(fmt.Sprintf("%s%s/%s/%s@%s", HuggingFacePrefix, p.hfOwner, p.hfRepo, path, p.hfRevision))
}

func resolveTorrentPlan(ctx context.Context, u URI, dopts downloadOptions) (*torrentPlan, error) {
	plan := &torrentPlan{selector: u.TorrentSelector()}
	switch {
	case u.LooksLikePirateFace():
		ref, err := ParsePirateFaceURI(string(u))
		if err != nil {
			return nil, err
		}
		entry, file, err := resolvePirateFaceFile(ctx, ref)
		if err != nil {
			return nil, err
		}
		if entry.Magnet == "" {
			return nil, ErrPirateFaceNoMagnet
		}
		m, err := ParseMagnet(entry.Magnet)
		if err != nil {
			return nil, err
		}
		plan.magnet, plan.infoHash = m, m.InfoHash
		plan.selector = ref.Path
		if len(file.SHA256) == 64 {
			plan.sha256 = strings.ToLower(file.SHA256)
		}
		if entry.HF == nil || *entry.HF {
			plan.hfOwner, plan.hfRepo, plan.hfRevision = ref.Owner, ref.Repo, entry.Revision
		}
		if entry.Torrent != "" {
			plan.metainfo = fetchTorrentBestEffort(ctx, PirateFaceEndpoint+entry.Torrent, m.InfoHash, dopts)
		}
	case u.LooksLikeMagnet():
		m, err := ParseMagnet(string(u))
		if err != nil {
			return nil, err
		}
		plan.magnet, plan.infoHash = m, m.InfoHash
		// A Pirate Face web seed also says where its .torrent lives and which
		// Hugging Face revision it mirrors. Any other magnet link is used as is.
		for _, ws := range m.WebSeeds {
			if owner, repo, rev, ok := pirateFaceWebSeed(ws); ok && m.InfoHash != "" {
				plan.hfOwner, plan.hfRepo, plan.hfRevision = owner, repo, rev
				plan.metainfo = fetchTorrentBestEffort(ctx, PirateFaceEndpoint+"/api/torrents/"+m.InfoHash+".torrent", m.InfoHash, dopts)
				break
			}
		}
	default:
		src, _ := splitSelector(string(u))
		data, err := readTorrentFile(ctx, src, dopts)
		if err != nil {
			return nil, err
		}
		mi, err := metainfo.Load(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("reading torrent file %q: %w", src, err)
		}
		plan.metainfo = mi
		plan.infoHash = mi.HashInfoBytes().HexString()
	}
	return plan, nil
}

func readTorrentFile(ctx context.Context, src string, dopts downloadOptions) ([]byte, error) {
	if strings.HasPrefix(src, HTTPPrefix) || strings.HasPrefix(src, HTTPSPrefix) {
		req, err := newDownloadRequest(ctx, http.MethodGet, URI(src).ResolveURL(), dopts.bearerToken)
		if err != nil {
			return nil, err
		}
		resp, err := downloadHTTPClient().Do(req)
		if err != nil {
			return nil, asTransient(fmt.Errorf("fetching torrent file %q: %w", src, err))
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			err := fmt.Errorf("fetching torrent file %q: status %d", src, resp.StatusCode)
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
				return nil, asTransient(err)
			}
			return nil, err
		}
		return readCapped(resp.Body, src)
	}
	p := strings.TrimPrefix(src, LocalPrefix)
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("opening torrent file: %w", err)
	}
	defer func() { _ = f.Close() }()
	return readCapped(f, p)
}

func readCapped(r io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxTorrentFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading torrent file %q: %w", name, err)
	}
	if len(data) > maxTorrentFileBytes {
		return nil, fmt.Errorf("torrent file %q is larger than %d bytes", name, maxTorrentFileBytes)
	}
	return data, nil
}

// fetchTorrentBestEffort downloads a .torrent the index publishes. A failure
// is not an error: the magnet link still leads to the same torrent through
// peers. A file whose info hash differs from the magnet's is dropped.
func fetchTorrentBestEffort(ctx context.Context, src, wantHash string, dopts downloadOptions) *metainfo.MetaInfo {
	data, err := readTorrentFile(ctx, src, downloadOptions{})
	if err != nil {
		xlog.Debug("could not fetch torrent file, using the magnet link", "url", src, "error", err)
		return nil
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		xlog.Debug("torrent file is not valid, using the magnet link", "url", src, "error", err)
		return nil
	}
	if wantHash != "" && mi.HashInfoBytes().HexString() != wantHash {
		xlog.Warn("torrent file does not match the magnet link, ignoring it", "url", src)
		return nil
	}
	return mi
}

// ---- download --------------------------------------------------------------

// infoCache keeps the file lists learnt from peers, so the next file of the
// same torrent does not wait for them again.
var infoCache = struct {
	sync.Mutex
	m map[string][]byte
}{m: map[string][]byte{}}

func cachedInfo(hash string) []byte {
	infoCache.Lock()
	defer infoCache.Unlock()
	return infoCache.m[hash]
}

func storeInfo(hash string, info []byte) {
	infoCache.Lock()
	defer infoCache.Unlock()
	if len(infoCache.m) >= 64 {
		clear(infoCache.m)
	}
	infoCache.m[hash] = info
}

// downloadTorrentFile downloads one file of a torrent to filePath. It writes
// <filePath>.partial while it runs and renames it when every piece has passed
// the torrent's hash check and the optional SHA-256 matches.
func (uri URI) downloadTorrentFile(ctx context.Context, filePath, sha string, fileN, total int, status func(string, string, string, float64), dopts downloadOptions, opts []DownloadOption) error {
	settings := TorrentConfig
	plan, err := resolveTorrentPlan(ctx, uri, dopts)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if sha == "" {
		sha = plan.sha256
	}

	chosen, err := runTorrentDownload(ctx, settings, plan, filePath, sha, fileN, total, status, dopts)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return err
	}
	fallbackPath := chosen
	if fallbackPath == "" {
		fallbackPath = plan.selector
	}
	fb := plan.fallbackFor(fallbackPath)
	if !settings.Fallback || fb == "" || errors.Is(err, ErrTorrentFileSelection) {
		return err
	}
	xlog.Warn("BitTorrent download failed, falling back to Hugging Face", "file", filePath, "error", err)
	_ = removePartialFile(filePath + PartialFileSuffix)
	return fb.DownloadFileWithContext(ctx, filePath, sha, fileN, total, status, opts...)
}

func runTorrentDownload(ctx context.Context, settings TorrentSettings, plan *torrentPlan, filePath, sha string, fileN, total int, status func(string, string, string, float64), dopts downloadOptions) (chosenPath string, err error) {
	if plan.metainfo == nil && plan.infoHash != "" {
		if info := cachedInfo(plan.infoHash); info != nil {
			plan.metainfo = &metainfo.MetaInfo{InfoBytes: info}
		}
	}
	if settings.Mode == TorrentModeWebseed && plan.metainfo == nil {
		return "", errors.New("webseed-only mode needs a .torrent file for the file list, and this magnet link has none; use a .torrent source or set LOCALAI_TORRENT_MODE=swarm")
	}
	if plan.metainfo != nil && plan.magnet != nil && plan.magnet.InfoHash != "" && len(plan.metainfo.InfoBytes) > 0 {
		if got := plan.metainfo.HashInfoBytes().HexString(); got != plan.magnet.InfoHash {
			return "", fmt.Errorf("torrent file hash %s does not match the magnet link %s", got, plan.magnet.InfoHash)
		}
	}

	var spec *torrent.TorrentSpec
	if plan.metainfo != nil {
		spec, err = torrent.TorrentSpecFromMetaInfoErr(plan.metainfo)
		if err != nil {
			return "", fmt.Errorf("reading torrent: %w", err)
		}
	} else {
		spec = &torrent.TorrentSpec{}
		spec.InfoHash = metainfo.NewHashFromHex(plan.infoHash)
		if plan.infoHash == "" {
			return "", errors.New("a magnet link with only a v2 info hash cannot be downloaded: it has no btih hash")
		}
	}
	if m := plan.magnet; m != nil {
		spec.Webseeds = appendUnique(spec.Webseeds, m.WebSeeds...)
		if settings.Mode == TorrentModeSwarm {
			if len(m.Trackers) > 0 {
				spec.Trackers = append(spec.Trackers, m.Trackers)
			}
			spec.PeerAddrs = append(spec.PeerAddrs, m.PeerAddrs...)
		}
	}
	if settings.Mode == TorrentModeWebseed {
		spec.Trackers = nil
		spec.PeerAddrs = nil
		spec.Sources = nil
	}
	hash := plan.infoHash
	if hash == "" {
		hash = spec.InfoHash.HexString()
	}

	unlock := lockTorrent(hash)
	defer unlock()
	client, release, err := acquireTorrentClient(settings)
	if err != nil {
		return "", err
	}
	defer release()

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("failed to create parent directory for file %q: %v", filePath, err)
	}
	base := filepath.Base(filePath)
	partial := filePath + PartialFileSuffix
	scratch := filepath.Join(dir, base+".torrent-scratch")
	defer func() { _ = os.RemoveAll(scratch) }()

	var (
		selOnce sync.Once
		selEntr torrentEntry
		selErr  error
	)
	choose := func(info *metainfo.Info) (torrentEntry, error) {
		selOnce.Do(func() {
			var entries []torrentEntry
			entries, selErr = torrentEntries(info)
			if selErr != nil {
				return
			}
			var so []int
			if plan.magnet != nil {
				so = plan.magnet.SelectOnly
			}
			selEntr, selErr = selectTorrentFile(entries, plan.selector, so, filePath)
		})
		return selEntr, selErr
	}
	st := storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   dir,
		PieceCompletion: storage.NewMapPieceCompletion(),
		UsePartFiles:    g.Some(false),
		DisableMmap:     true,
		FilePathMaker: func(o storage.FilePathMakerOpts) string {
			// The torrent never chooses a path on disk: the wanted file goes
			// to the partial, and every other file that shares a piece with it
			// goes to a numbered scratch file. Names inside the torrent cannot
			// reach the file system.
			if e, err := choose(o.Info); err == nil && e.Offset == o.File.TorrentOffset && !e.Pad {
				return filepath.Base(partial)
			}
			return filepath.Join(filepath.Base(scratch), strconv.FormatInt(o.File.TorrentOffset, 10)+PartialFileSuffix)
		},
	})
	defer func() { _ = st.Close() }()
	spec.Storage = st

	t, _, err := client.AddTorrentSpec(spec)
	if err != nil {
		return "", fmt.Errorf("adding torrent: %w", err)
	}
	dropped := false
	drop := func() {
		if !dropped {
			dropped = true
			t.Drop()
		}
	}
	defer drop()

	// Wait for the file list. With a .torrent it is there at once; with a bare
	// magnet link it comes from peers.
	timer := time.NewTimer(settings.MetadataTimeout)
	defer timer.Stop()
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("%w for torrent %s within %s: no web seed answered and no peer sent the file list (check the trackers, set LOCALAI_TORRENT_DHT=true for trackerless links, or use a .torrent file)", ErrNoPeers, hash, settings.MetadataTimeout)
	}
	if mi := t.Metainfo(); len(mi.InfoBytes) > 0 {
		storeInfo(hash, mi.InfoBytes)
	}
	entry, err := choose(t.Info())
	if err != nil {
		return "", err
	}
	chosenPath = entry.Path
	var file *torrent.File
	for _, f := range t.Files() {
		if f.Offset() == entry.Offset && f.Length() == entry.Length && !strings.Contains(f.FileInfo().Attr, "p") {
			file = f
			break
		}
	}
	if file == nil {
		return chosenPath, fmt.Errorf("file %q not found in torrent", entry.Path)
	}
	for _, f := range t.Files() {
		f.SetPriority(torrent.PiecePriorityNone)
	}
	if settings.Mode == TorrentModeSwarm {
		file.Download()
	}

	// Web seeds first: they answer at once, while peers take a while to find.
	// Both run for the whole transfer and whichever has a piece first wins.
	seedErr := make(chan error, 1)
	var filler *webseedFiller
	if bases := appendUnique(append([]string(nil), spec.Webseeds...)); len(bases) > 0 {
		filler = newWebseedFiller(t, bases, dopts)
		fctx, fcancel := context.WithCancel(ctx)
		defer fcancel()
		go func() {
			seedErr <- filler.run(fctx, file.BeginPieceIndex(), file.EndPieceIndex())
		}()
	} else if settings.Mode == TorrentModeWebseed {
		return chosenPath, errors.New("webseed-only mode, but the torrent names no web seed")
	}

	pw := &progressWriter{
		fileName:       partial,
		total:          entry.Length,
		fileNo:         fileN,
		totalFiles:     total,
		downloadStatus: status,
		transferSink:   dopts.transferProgress,
		ctx:            ctx,
	}
	xlog.Info("Downloading over BitTorrent", "file", filePath, "torrent", hash, "path", entry.Path, "mode", settings.Mode)

	last := int64(-1)
	lastChange := time.Now()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		done := file.BytesCompleted()
		if done != last {
			last, lastChange = done, time.Now()
			pw.written = done
			pw.report()
		}
		if done >= entry.Length {
			break
		}
		if DownloadStallTimeout > 0 && time.Since(lastChange) > DownloadStallTimeout {
			return chosenPath, asTransient(fmt.Errorf("BitTorrent download of %q stalled: no data from peers or web seeds for %s (%d of %d bytes)", entry.Path, DownloadStallTimeout, done, entry.Length))
		}
		select {
		case err := <-seedErr:
			if err != nil {
				if settings.Mode == TorrentModeWebseed {
					return chosenPath, asTransient(err)
				}
				logWebseedStop(err)
			}
		case <-ctx.Done():
			drop()
			if errors.Is(context.Cause(ctx), ErrUserCancelled) {
				_ = removePartialFile(partial)
			}
			return chosenPath, ctx.Err()
		case <-tick.C:
		}
	}

	if filler != nil {
		xlog.Info("BitTorrent transfer finished", "torrent", hash, "fromWebSeed", filler.fetched.Load(), "fromPeers", func() int64 { st := t.Stats(); return st.BytesReadUsefulData.Int64() }())
	}
	// Close the torrent and its files before the partial is read or renamed.
	drop()
	_ = st.Close()

	if fi, err := os.Stat(partial); err != nil || fi.Size() != entry.Length {
		_ = removePartialFile(partial)
		return chosenPath, fmt.Errorf("BitTorrent download of %q left a partial of the wrong size", entry.Path)
	}
	if sha != "" {
		got, err := CalculateSHA(partial)
		if err != nil {
			return chosenPath, fmt.Errorf("failed to calculate SHA for file %q: %v", partial, err)
		}
		if got != sha {
			_ = removePartialFile(partial)
			return chosenPath, asTransient(fmt.Errorf("SHA mismatch for file %q ( calculated: %s != metadata: %s )", filePath, got, sha))
		}
	} else {
		xlog.Warn("BitTorrent download has no SHA-256 to check; only the torrent's piece hashes vouch for the file",
			"file", filePath, "torrent", hash)
	}
	if err := os.Rename(partial, filePath); err != nil {
		return chosenPath, fmt.Errorf("failed to rename temporary file %s -> %s: %v", partial, filePath, err)
	}
	xlog.Info("File downloaded and verified", "file", filePath)
	if utils.IsArchive(filePath) {
		basePath := filepath.Dir(filePath)
		if err := utils.ExtractArchive(filePath, basePath); err != nil {
			return chosenPath, err
		}
	}
	return chosenPath, nil
}

func appendUnique(dst []string, add ...string) []string {
	for _, a := range add {
		if a == "" {
			continue
		}
		if _, err := url.Parse(a); err != nil {
			continue
		}
		dup := false
		for _, d := range dst {
			if d == a {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, a)
		}
	}
	return dst
}
