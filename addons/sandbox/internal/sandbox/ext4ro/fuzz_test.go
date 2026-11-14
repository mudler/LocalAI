package ext4ro

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FuzzWalk damages a golden image with patches and walks it. A panic or a
// hang is a failure. The input is a selector for the base image and a list
// of patches. A patch is four bytes: a 24-bit offset (modulo the image size)
// and a new byte. Patches keep the inputs small, so the fuzzer spends its
// time on the structures and not on 1 MiB of zeros.
//
// Run it with: go test -fuzz FuzzWalk -fuzztime 5m
// Inputs that fail are kept in testdata/fuzz/FuzzWalk as regression tests.
func FuzzWalk(f *testing.F) {
	bases := make([][]byte, len(corruptionBases))
	for i, name := range corruptionBases {
		bases[i] = readGoldenForFuzz(name + ".img.gz")
		f.Add(uint8(i), []byte{})
	}
	// Seeds that damage the superblock, an inode and a directory block.
	f.Add(uint8(0), []byte{0x38, 0x04, 0x00, 0x00})
	f.Add(uint8(0), []byte{0x00, 0x20, 0x00, 0xFF, 0x04, 0x20, 0x00, 0xFF})
	f.Add(uint8(1), []byte{0x28, 0x08, 0x00, 0x01, 0x2c, 0x08, 0x00, 0xFF})
	f.Add(uint8(3), []byte{0x00, 0x10, 0x01, 0x00, 0x04, 0x10, 0x01, 0x03})
	f.Fuzz(func(ft *testing.T, sel uint8, patches []byte) {
		base := bases[int(sel)%len(bases)]
		img := append([]byte(nil), base...)
		for i := 0; i+4 <= len(patches); i += 4 {
			off := (int(patches[i]) | int(patches[i+1])<<8 | int(patches[i+2])<<16) % len(img)
			img[off] = patches[i+3]
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = fullWalk(img)
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			panic("ext4ro: the walk of a damaged image does not end")
		}
	})
}

func readGoldenForFuzz(name string) []byte {
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		panic(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	if !bytes.HasPrefix(b[:2048], make([]byte, 1024)) {
		// The first KiB of every image is unused; this guards a bad file.
		panic("unexpected golden image " + name)
	}
	return b
}
