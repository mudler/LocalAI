package cli

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
)

// TorrentFlags control how magnet links, .torrent files and pirateface://
// model URIs are downloaded. The defaults open no listening port to the
// network and no DHT socket; see docs/content/features/model-gallery.md.
type TorrentFlags struct {
	TorrentMode            string        `name:"torrent-mode" env:"LOCALAI_TORRENT_MODE" default:"swarm" enum:"swarm,webseed" help:"How to download magnet, .torrent and pirateface:// URIs. swarm: HTTP web seeds and BitTorrent peers together. webseed: web seeds only; opens no socket and contacts no tracker or peer" group:"torrent"`
	TorrentListenPort      int           `name:"torrent-listen-port" env:"LOCALAI_TORRENT_LISTEN_PORT" default:"0" help:"TCP and uTP port for incoming BitTorrent peers. 0 (the default) accepts no connection from the network and asks no router to forward a port; a positive port listens on all interfaces and enables UPnP/NAT-PMP port mapping" group:"torrent"`
	TorrentDHT             bool          `name:"torrent-dht" env:"LOCALAI_TORRENT_DHT" default:"false" help:"Use the BitTorrent DHT to find peers. It opens a UDP socket; needed only for magnet links that name no tracker" group:"torrent"`
	TorrentMetadataTimeout time.Duration `name:"torrent-metadata-timeout" env:"LOCALAI_TORRENT_METADATA_TIMEOUT" default:"90s" help:"How long to wait for peers to send a torrent's file list when it is not in a .torrent file" group:"torrent"`
	TorrentFallback        bool          `name:"torrent-fallback" env:"LOCALAI_TORRENT_FALLBACK" default:"true" negatable:"" help:"When a Pirate Face download fails over BitTorrent, retry the same file from Hugging Face" group:"torrent"`
	PirateFaceURL          string        `name:"pirateface-url" env:"LOCALAI_PIRATEFACE_URL" default:"https://pirateface.co" help:"Pirate Face site that pirateface:// URIs and model page URLs resolve against" group:"torrent"`
}

// Apply installs the flags in the downloader. Commands that download call it
// before their first download.
func (t TorrentFlags) Apply() error {
	switch t.TorrentMode {
	case "":
		// A command built without kong's defaults, such as in a test, keeps
		// the settings it has.
		return nil
	case downloader.TorrentModeSwarm, downloader.TorrentModeWebseed:
	default:
		return fmt.Errorf("invalid --torrent-mode %q: use %s or %s", t.TorrentMode, downloader.TorrentModeSwarm, downloader.TorrentModeWebseed)
	}
	if t.TorrentListenPort < 0 || t.TorrentListenPort > 65535 {
		return fmt.Errorf("invalid --torrent-listen-port %d", t.TorrentListenPort)
	}
	downloader.TorrentConfig = downloader.TorrentSettings{
		Mode:            t.TorrentMode,
		ListenPort:      t.TorrentListenPort,
		DHT:             t.TorrentDHT,
		MetadataTimeout: t.TorrentMetadataTimeout,
		Fallback:        t.TorrentFallback,
	}
	if t.TorrentMetadataTimeout <= 0 {
		downloader.TorrentConfig.MetadataTimeout = downloader.DefaultTorrentSettings().MetadataTimeout
	}
	if t.PirateFaceURL != "" {
		downloader.PirateFaceEndpoint = trimSlash(t.PirateFaceURL)
	}
	return nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
