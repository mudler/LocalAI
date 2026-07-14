package audio

// Copied from VoxInput

import (
	"encoding/binary"
	"io"
)

// WAVHeader represents the WAV file header (44 bytes for PCM)
type WAVHeader struct {
	// RIFF Chunk (12 bytes)
	ChunkID   [4]byte
	ChunkSize uint32
	Format    [4]byte

	// fmt Subchunk (16 bytes)
	Subchunk1ID   [4]byte
	Subchunk1Size uint32
	AudioFormat   uint16
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16

	// data Subchunk (8 bytes)
	Subchunk2ID   [4]byte
	Subchunk2Size uint32
}

func NewWAVHeader(pcmLen uint32) WAVHeader {
	header := WAVHeader{
		ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
		Format:        [4]byte{'W', 'A', 'V', 'E'},
		Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
		Subchunk1Size: 16, // PCM = 16 bytes
		AudioFormat:   1,  // PCM
		NumChannels:   1,  // Mono
		SampleRate:    16000,
		ByteRate:      16000 * 2, // SampleRate * BlockAlign (mono, 2 bytes per sample)
		BlockAlign:    2,         // 16-bit = 2 bytes per sample
		BitsPerSample: 16,
		Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
		Subchunk2Size: pcmLen,
	}

	header.ChunkSize = 36 + header.Subchunk2Size

	return header
}

func (h *WAVHeader) Write(writer io.Writer) error {
	return binary.Write(writer, binary.LittleEndian, h)
}

// NewWAVHeaderWithRate creates a WAV header for mono 16-bit PCM at the given sample rate.
func NewWAVHeaderWithRate(pcmLen, sampleRate uint32) WAVHeader {
	header := WAVHeader{
		ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
		Format:        [4]byte{'W', 'A', 'V', 'E'},
		Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
		Subchunk1Size: 16,
		AudioFormat:   1,
		NumChannels:   1,
		SampleRate:    sampleRate,
		ByteRate:      sampleRate * 2,
		BlockAlign:    2,
		BitsPerSample: 16,
		Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
		Subchunk2Size: pcmLen,
	}
	header.ChunkSize = 36 + header.Subchunk2Size
	return header
}

// WAVHeaderSize is the size of a standard PCM WAV header in bytes.
const WAVHeaderSize = 44

// wavDataChunk walks the RIFF sub-chunks of an in-memory WAV and returns the
// `data` chunk payload (a sub-slice of data, not a copy) plus the sample rate
// from `fmt `. ok is false when data isn't a RIFF/WAVE stream or carries no
// data chunk — callers then fall back to treating the input as raw PCM.
//
// Walking the chunks rather than assuming the canonical 44-byte header is what
// keeps an 18/40-byte extensible `fmt `, or JUNK/LIST/bext metadata before or
// after `data` (e.g. ffmpeg's trailing "Lavf" tag), from being spliced into
// the PCM as an audible click.
func wavDataChunk(data []byte) (pcm []byte, sampleRate int, ok bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, false
	}
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if size < 0 || body+size > len(data) {
			// Truncated/garbage size — clamp to what's left so a short final
			// chunk doesn't drop an otherwise valid data chunk.
			size = len(data) - body
		}
		switch id {
		case "fmt ":
			if size >= 16 {
				sampleRate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			}
		case "data":
			return data[body : body+size], sampleRate, true
		}
		// Chunks are word-aligned: an odd size is followed by a pad byte.
		off = body + size + (size & 1)
	}
	return nil, 0, false
}

// StripWAVHeader removes a WAV header from audio data, returning raw PCM. If
// the data isn't a recognisable WAV (e.g. it's already raw PCM) it is returned
// unchanged. Locates the `data` chunk by walking the RIFF structure rather
// than assuming a fixed 44-byte header — see [wavDataChunk].
func StripWAVHeader(data []byte) []byte {
	if pcm, _, ok := wavDataChunk(data); ok {
		return pcm
	}
	return data
}

// ParseWAV returns the raw PCM of a WAV's `data` chunk along with the sample
// rate from `fmt `. If the data isn't a recognisable WAV it is returned as-is
// with sampleRate=0. Walks the RIFF structure — see [wavDataChunk].
func ParseWAV(data []byte) (pcm []byte, sampleRate int) {
	if pcm, sr, ok := wavDataChunk(data); ok {
		return pcm, sr
	}
	return data, 0
}
