package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// MaxDownloadBytes is the largest file that Telegram lets a bot download
// with getFile on the public Bot API (20 MB), as the Bots FAQ states:
// https://core.telegram.org/bots/faq.
const MaxDownloadBytes = 20 << 20

// ErrTooLarge means a file of a message is larger than the add-on hands
// over. It is the shared error of the connector add-ons, so the host tells
// the sender.
var ErrTooLarge = connector.ErrTooLarge

// albumWait is how long Updates waits before it asks again for the rest of
// an album.
const albumWait = 300 * time.Millisecond

type tgPhoto struct {
	FileID   string `json:"file_id"`
	UniqueID string `json:"file_unique_id"`
	Size     int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type tgFile struct {
	FileID   string `json:"file_id"`
	UniqueID string `json:"file_unique_id"`
	Size     int64  `json:"file_size"`
	MIME     string `json:"mime_type"`
}

// The kinds of file that a reference names. A reference is the kind, a
// colon and the file_id of Telegram; the host passes it back unchanged.
const (
	refPhoto     = "photo"
	refVoice     = "voice"
	refAudio     = "audio"
	refVideoNote = "video_note"
	refDocument  = "document"
)

var refKinds = map[string]bool{refPhoto: true, refVoice: true, refAudio: true, refVideoNote: true, refDocument: true}

// fileID is the form of a file_id of Telegram: URL-safe base64.
var fileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// filePath is the form of the path that getFile answers, such as
// "photos/file_12.jpg". Anything else is refused, so the download URL
// cannot leave the files of the bot.
var filePath = regexp.MustCompile(`^[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*(\.[A-Za-z0-9]{1,8})?$`)

// mediaRefs are the references of the files of a message that the add-on
// hands over. A message of Telegram carries at most one file; an album is
// several messages (see mergeAlbums).
func mediaRefs(m tgMessage) []string {
	ref := func(kind, id string) []string {
		if !fileID.MatchString(id) {
			return nil
		}
		return []string{kind + ":" + id}
	}
	switch {
	case len(m.Photo) > 0:
		return ref(refPhoto, bestPhoto(m.Photo).FileID)
	case m.Voice != nil:
		return ref(refVoice, m.Voice.FileID)
	case m.Audio != nil:
		return ref(refAudio, m.Audio.FileID)
	case m.VideoNote != nil:
		return ref(refVideoNote, m.VideoNote.FileID)
	case m.Document != nil:
		return ref(refDocument, m.Document.FileID)
	}
	return nil
}

// bestPhoto is the largest size of a photo that the add-on can hand over
// (at most MaxDownloadBytes; the host's limit is checked when it is read).
// Telegram sends each photo in several sizes; when none says that it fits,
// the smallest one is tried.
func bestPhoto(sizes []tgPhoto) tgPhoto {
	best, found := sizes[0], false
	for _, p := range sizes {
		if p.Size <= 0 || p.Size > MaxDownloadBytes {
			continue
		}
		if !found || p.Width*p.Height > best.Width*best.Height {
			best, found = p, true
		}
	}
	return best
}

// mergeAlbums makes one message of the items of an album that follow each
// other in the updates: one turn sees all the photos, and the caption, which
// Telegram puts on one item, goes with them. A message holds at most
// contract.MaxConnectorMedia files; the rest of a larger album starts the
// next message. The merged message has the cursor of its last item, so the
// host moves its offset past all of them.
func mergeAlbums(ups []update) []contract.ConnectorMessage {
	out := make([]contract.ConnectorMessage, 0, len(ups))
	group := ""
	for _, u := range ups {
		if n := len(out); n > 0 && u.group != "" && u.group == group {
			last := &out[n-1]
			if last.Chat == u.Chat && last.SenderID == u.SenderID && len(last.Media)+len(u.Media) <= contract.MaxConnectorMedia {
				last.Cursor = u.Cursor
				last.Media = append(last.Media, u.Media...)
				if u.Text != "" {
					last.Text = strings.TrimSpace(last.Text + "\n\n" + u.Text)
				}
				last.Mention = last.Mention || u.Mention
				last.Unsupported = last.Text == "" && len(last.Media) == 0
				continue
			}
		}
		out = append(out, u.ConnectorMessage)
		group = u.group
	}
	return out
}

// sleep waits for d, or less when ctx ends. It reports whether ctx is still live.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// MediaStream opens the file of a reference for streaming to the host. The
// size that getFile states is checked against max before a byte is read, and
// the type is decided from the first bytes, which stay in the stream. The name
// is made here, as in Media.
func (t *API) MediaStream(ctx context.Context, token, ref string, max int64) (connector.MediaStream, error) {
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || !refKinds[kind] || !fileID.MatchString(id) {
		return connector.MediaStream{}, errors.New("telegram: not a file reference")
	}
	var f struct {
		UniqueID string `json:"file_unique_id"`
		Size     int64  `json:"file_size"`
		Path     string `json:"file_path"`
	}
	err := t.call(ctx, token, "getFile", map[string]any{"file_id": id}, 0, &f)
	switch {
	case refusedBecause(err, "file is too big"):
		return connector.MediaStream{}, fmt.Errorf("telegram: Telegram lets a bot download %d MB at most: %w", MaxDownloadBytes>>20, ErrTooLarge)
	case err != nil:
		return connector.MediaStream{}, err
	case f.Size > max:
		return connector.MediaStream{}, fmt.Errorf("telegram: the file has %d bytes: %w", f.Size, ErrTooLarge)
	case !filePath.MatchString(f.Path):
		return connector.MediaStream{}, errors.New("telegram: Telegram named no file to download")
	}
	body, err := t.getStream(ctx, token, f.Path)
	if err != nil {
		return connector.MediaStream{}, redact(err, token)
	}
	rd, head := connector.Sniff(body, 512)
	mimeType, ext := fileType(kind, head)
	if mimeType == "" {
		_ = body.Close()
		return connector.MediaStream{}, fmt.Errorf("telegram: the %s is not of a type the add-on reads", strings.ReplaceAll(kind, "_", " "))
	}
	name := kind
	if fileID.MatchString(f.UniqueID) {
		name += "-" + f.UniqueID
	}
	return connector.MediaStream{Name: contract.SafeFileName(strings.ReplaceAll(name, "_", "-") + ext), MIME: mimeType, Size: f.Size,
		Body: struct {
			io.Reader
			io.Closer
		}{rd, body}}, nil
}

// getStream opens the download of a file of the bot. The context carries the
// bound of the transfer; the client has no total timeout.
func (t *API) getStream(ctx context.Context, token, path string) (io.ReadCloser, error) {
	base := t.BaseURL
	if base == "" {
		base = DefaultBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/file/bot"+url.PathEscape(token)+"/"+path, nil)
	if err != nil {
		return nil, errors.New("telegram: could not build a download request")
	}
	resp, err := t.client(contract.ConnectorTransferTimeout).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("telegram: the download failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("telegram: the download answered %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Types of the files, by what their bytes are, and the extension that the
// name gets. The name decides what the host does with a file: an image goes
// to a model that can see, audio is transcribed.
var (
	imageExts = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
	audioExts = map[string]string{"application/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/wave": ".wav", "video/mp4": ".m4a"}
	docExts   = map[string]string{"application/pdf": ".pdf", "text/plain; charset=utf-8": ".txt"}
)

// fileType is the media type and the extension of a file of a kind, or ""
// when its bytes are not of a type that the kind allows.
func fileType(kind string, data []byte) (string, string) {
	sniffed := http.DetectContentType(data)
	pick := func(exts ...map[string]string) (string, string) {
		for _, m := range exts {
			if ext, ok := m[sniffed]; ok {
				return strings.TrimSuffix(sniffed, "; charset=utf-8"), ext
			}
		}
		return "", ""
	}
	switch kind {
	case refPhoto:
		return pick(imageExts)
	case refDocument:
		return pick(imageExts, docExts)
	case refVoice, refAudio:
		if bytes.HasPrefix(data, []byte("fLaC")) {
			return "audio/flac", ".flac"
		}
		mt, ext := pick(audioExts)
		if mt == "video/mp4" {
			mt = "audio/mp4"
		}
		return mt, ext
	case refVideoNote:
		if sniffed == "video/mp4" {
			return sniffed, ".mp4"
		}
	}
	return "", ""
}
