package audio

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dhowden/tag"
	"github.com/mudler/xlog"
)

// extensionFromFileType returns the file extension for tag.FileType.
func extensionFromFileType(ft tag.FileType) string {
	switch ft {
	case tag.FLAC:
		return "flac"
	case tag.MP3:
		return "mp3"
	case tag.OGG:
		return "ogg"
	case tag.M4A:
		return "m4a"
	case tag.M4B:
		return "m4b"
	case tag.M4P:
		return "m4p"
	case tag.ALAC:
		return "m4a"
	case tag.DSF:
		return "dsf"
	default:
		return ""
	}
}

// contentTypeFromFileType returns the MIME type for tag.FileType.
func contentTypeFromFileType(ft tag.FileType) string {
	switch ft {
	case tag.FLAC:
		return "audio/flac"
	case tag.MP3:
		return "audio/mpeg"
	case tag.OGG:
		return "audio/ogg"
	case tag.M4A, tag.M4B, tag.M4P, tag.ALAC:
		return "audio/mp4"
	case tag.DSF:
		return "audio/dsd"
	default:
		return ""
	}
}

// Identify reads from r and returns the detected audio extension and Content-Type.
// It uses github.com/dhowden/tag to identify the format from the stream.
// Returns ("", "", err) if the format could not be identified.
func Identify(r io.ReadSeeker) (ext string, contentType string, err error) {
	_, fileType, err := tag.Identify(r)
	if err != nil || fileType == tag.UnknownFileType {
		return "", "", err
	}
	ext = extensionFromFileType(fileType)
	contentType = contentTypeFromFileType(fileType)
	if ext == "" || contentType == "" {
		return "", "", nil
	}
	return ext, contentType, nil
}

// ContentTypeFromExtension returns the MIME type for common audio file extensions.
// Use as a fallback when Identify fails or when the file is not openable.
func ContentTypeFromExtension(path string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "flac":
		return "audio/flac"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg":
		return "audio/ogg"
	case "m4a", "m4b", "m4p":
		return "audio/mp4"
	case "webm":
		return "audio/webm"
	default:
		return ""
	}
}

// NormalizeAudioFile opens the file at path, identifies its format with tag.Identify,
// and renames the file to have the correct extension if the current one does not match.
// It returns the path to use (possibly the renamed file) and the Content-Type to set.
// If identification fails, returns (path, ContentTypeFromExtension(path)).
func NormalizeAudioFile(path string) (finalPath string, contentType string) {
	finalPath = path
	f, err := os.Open(path)
	if err != nil {
		contentType = ContentTypeFromExtension(path)
		return finalPath, contentType
	}
	defer f.Close()

	ext, ct, identifyErr := Identify(f)
	if identifyErr != nil || ext == "" || ct == "" {
		contentType = ContentTypeFromExtension(path)
		return finalPath, contentType
	}
	contentType = ct

	currentExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if currentExt == ext {
		return finalPath, contentType
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
	if baseNoExt == "" {
		baseNoExt = base
	}
	newPath := filepath.Join(dir, baseNoExt+"."+ext)
	if renameErr := os.Rename(path, newPath); renameErr != nil {
		xlog.Debug("Could not rename audio file to match type", "from", path, "to", newPath, "error", renameErr)
		return finalPath, contentType
	}
	return newPath, contentType
}
