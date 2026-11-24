package studio

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif" // decoders for fitImage
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"path"
	"strings"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Limits of the media that goes to the model inside the message.
const (
	maxNativeImage = 20 << 20
	maxNativeVideo = 100 << 20
	maxNativeTotal = 128 << 20
	// maxImageSide is the longest side of an image in the message. Vision
	// models scale an image down to about this size anyway, and a cloud model
	// bills the pixels and refuses a request that is too large, so a photo
	// from a phone is scaled down before it goes.
	maxImageSide = 2048
	// maxImagePixels caps what fitImage decodes, so a small file that claims
	// a huge size cannot take the memory of the server.
	maxImagePixels = 100_000_000
)

var (
	imageTypes = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
	videoTypes = map[string]string{".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".mkv": "video/x-matroska", ".avi": "video/x-msvideo"}
)

// nativeMedia turns the images and videos among the attached files into parts
// of the message, when the chat's model takes them. The files are kept as they
// are. A model that takes neither gets the file path and the describe_image
// tool, as before. A file that is too large for the message stays a file.
func (s *Service) nativeMedia(model string, up []session.Upload) ([]session.Upload, []session.Media, error) {
	if s.d.Modalities == nil || len(up) == 0 {
		return up, nil, nil
	}
	m := s.d.Modalities(model)
	if !m.Vision && !m.Video {
		return up, nil, nil
	}
	out := make([]session.Upload, len(up))
	var (
		parts []session.Media
		total int64
	)
	for i, u := range up {
		out[i] = u
		ext := strings.ToLower(path.Ext(u.Name))
		var (
			kind  session.MediaKind
			limit int64
		)
		switch {
		case m.Vision && imageTypes[ext]:
			kind, limit = session.MediaImage, maxNativeImage
		case m.Video && videoTypes[ext] != "":
			kind, limit = session.MediaVideo, maxNativeVideo
		}
		if kind == "" || u.Body == nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(u.Body, limit+1))
		if err != nil {
			return up, nil, err
		}
		if int64(len(data)) > limit || total+int64(len(data)) > maxNativeTotal {
			// Too large for the message: only the head is in memory, the rest keeps streaming.
			out[i].Body = io.MultiReader(bytes.NewReader(data), u.Body)
			continue
		}
		out[i].Body = bytes.NewReader(data)
		mime := videoTypes[ext]
		if kind == session.MediaImage {
			if mime = http.DetectContentType(data); !strings.HasPrefix(mime, "image/") {
				continue // the name says image, the bytes do not: leave it a file
			}
		}
		sent := data
		if kind == session.MediaImage {
			sent, mime = fitImage(data, mime)
		}
		total += int64(len(sent))
		parts = append(parts, session.Media{Kind: kind, DataURI: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(sent)})
	}
	return out, parts, nil
}

// fitImage scales an image down so that its longest side is maxImageSide,
// and returns it with its media type. An image that is small enough or that
// cannot be decoded is returned as it is. An opaque image becomes a JPEG; one
// with transparency stays a PNG. The pixels count, not only the bytes: a
// model bills and scales by the size of the image.
func fitImage(data []byte, mime string) ([]byte, string) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || max(cfg.Width, cfg.Height) <= maxImageSide || cfg.Width*cfg.Height > maxImagePixels {
		return data, mime
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mime
	}
	w, h := maxImageSide, cfg.Height*maxImageSide/cfg.Width
	if cfg.Height > cfg.Width {
		w, h = cfg.Width*maxImageSide/cfg.Height, maxImageSide
	}
	dst := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	outType := "image/png"
	if dst.Opaque() {
		outType = "image/jpeg"
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&out, dst)
	}
	if err != nil {
		return data, mime
	}
	return out.Bytes(), outType
}

// keepVideos holds the videos of a message for the first model request of the
// turn and returns the other parts. The agent harness cannot carry a video, so
// the model client adds it to the request itself (see takeVideos).
func (s *Service) keepVideos(k string, parts []session.Media) []session.Media {
	var rest []session.Media
	var videos []string
	for _, p := range parts {
		if p.Kind == session.MediaVideo {
			videos = append(videos, p.DataURI)
			continue
		}
		rest = append(rest, p)
	}
	if len(videos) > 0 {
		s.mu.Lock()
		s.videos[k] = videos
		s.mu.Unlock()
	}
	return rest
}

// takeVideos returns the videos waiting for a chat's next model request, once.
func (s *Service) takeVideos(p, conv string) []string {
	k := key(p, conv)
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.videos[k]
	delete(s.videos, k)
	return v
}
