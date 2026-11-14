package vz

import (
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
)

// mkUpper makes the sparse ext4 image of an upper layer. The file system has
// no journal, so the host reads the image without a journal replay, and the
// guest agent makes the folders it needs when it mounts the image.
func mkUpper(path string, size int64) error {
	return ext4mk.FormatFile(path, ext4mk.Options{Size: size, RootUID: 1000, RootGID: 1000})
}
