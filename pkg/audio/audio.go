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

// StripWAVHeader removes a WAV header from audio data, returning raw PCM.
// If the data is too short to contain a header, it is returned unchanged.
func StripWAVHeader(data []byte) []byte {
  if len(data) > WAVHeaderSize {
    return data[WAVHeaderSize:]
  }
  return data
}

// ParseWAV strips the WAV header and returns the raw PCM along with the
// sample rate read from the header. If the data is too short to contain a
// valid header the PCM is returned as-is with sampleRate=0.
func ParseWAV(data []byte) (pcm []byte, sampleRate int) {
  if len(data) <= WAVHeaderSize {
    return data, 0
  }
  sr := int(binary.LittleEndian.Uint32(data[24:28]))
  return data[WAVHeaderSize:], sr
}
