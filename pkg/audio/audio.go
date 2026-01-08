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
