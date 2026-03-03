package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNewWAVHeader_Valid44Bytes(t *testing.T) {
	hdr := NewWAVHeader(3200)
	var buf bytes.Buffer
	if err := hdr.Write(&buf); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if buf.Len() != WAVHeaderSize {
		t.Fatalf("header size = %d, want %d", buf.Len(), WAVHeaderSize)
	}

	b := buf.Bytes()
	// RIFF
	if string(b[0:4]) != "RIFF" {
		t.Errorf("ChunkID = %q, want RIFF", b[0:4])
	}
	// WAVE
	if string(b[8:12]) != "WAVE" {
		t.Errorf("Format = %q, want WAVE", b[8:12])
	}
	// fmt
	if string(b[12:16]) != "fmt " {
		t.Errorf("Subchunk1ID = %q, want 'fmt '", b[12:16])
	}
	// AudioFormat = 1 (PCM)
	audioFmt := binary.LittleEndian.Uint16(b[20:22])
	if audioFmt != 1 {
		t.Errorf("AudioFormat = %d, want 1", audioFmt)
	}
	// NumChannels = 1
	numCh := binary.LittleEndian.Uint16(b[22:24])
	if numCh != 1 {
		t.Errorf("NumChannels = %d, want 1", numCh)
	}
	// SampleRate = 16000
	sr := binary.LittleEndian.Uint32(b[24:28])
	if sr != 16000 {
		t.Errorf("SampleRate = %d, want 16000", sr)
	}
	// ByteRate = 32000
	br := binary.LittleEndian.Uint32(b[28:32])
	if br != 32000 {
		t.Errorf("ByteRate = %d, want 32000", br)
	}
	// data
	if string(b[36:40]) != "data" {
		t.Errorf("Subchunk2ID = %q, want 'data'", b[36:40])
	}
	// Subchunk2Size
	dataSize := binary.LittleEndian.Uint32(b[40:44])
	if dataSize != 3200 {
		t.Errorf("Subchunk2Size = %d, want 3200", dataSize)
	}
}

func TestNewWAVHeaderWithRate_CustomRate(t *testing.T) {
	hdr := NewWAVHeaderWithRate(4800, 24000)
	var buf bytes.Buffer
	if err := hdr.Write(&buf); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	b := buf.Bytes()

	sr := binary.LittleEndian.Uint32(b[24:28])
	if sr != 24000 {
		t.Errorf("SampleRate = %d, want 24000", sr)
	}
	br := binary.LittleEndian.Uint32(b[28:32])
	if br != 48000 {
		t.Errorf("ByteRate = %d, want 48000 (24000*2)", br)
	}
}

func TestStripWAVHeader_Strips44Bytes(t *testing.T) {
	pcm := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	hdr := NewWAVHeader(uint32(len(pcm)))
	var buf bytes.Buffer
	if err := hdr.Write(&buf); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	buf.Write(pcm)

	got := StripWAVHeader(buf.Bytes())
	if !bytes.Equal(got, pcm) {
		t.Errorf("StripWAVHeader result = %v, want %v", got, pcm)
	}
}

func TestStripWAVHeader_ShortData(t *testing.T) {
	short := []byte{0x01, 0x02, 0x03}
	got := StripWAVHeader(short)
	if !bytes.Equal(got, short) {
		t.Errorf("expected unchanged data for short input")
	}

	// Exactly 44 bytes — still "short" because there's no data after the header
	exact := make([]byte, WAVHeaderSize)
	got = StripWAVHeader(exact)
	if !bytes.Equal(got, exact) {
		t.Errorf("expected unchanged data for exact header-size input")
	}
}

func TestParseWAV_ReturnsSampleRate(t *testing.T) {
	pcm := make([]byte, 100)
	for i := range pcm {
		pcm[i] = byte(i)
	}

	// 24kHz WAV
	hdr24 := NewWAVHeaderWithRate(uint32(len(pcm)), 24000)
	var buf24 bytes.Buffer
	hdr24.Write(&buf24)
	buf24.Write(pcm)

	gotPCM, gotRate := ParseWAV(buf24.Bytes())
	if gotRate != 24000 {
		t.Errorf("ParseWAV sample rate = %d, want 24000", gotRate)
	}
	if !bytes.Equal(gotPCM, pcm) {
		t.Error("ParseWAV PCM data mismatch")
	}

	// 16kHz WAV
	hdr16 := NewWAVHeader(uint32(len(pcm)))
	var buf16 bytes.Buffer
	hdr16.Write(&buf16)
	buf16.Write(pcm)

	gotPCM, gotRate = ParseWAV(buf16.Bytes())
	if gotRate != 16000 {
		t.Errorf("ParseWAV sample rate = %d, want 16000", gotRate)
	}
	if !bytes.Equal(gotPCM, pcm) {
		t.Error("ParseWAV PCM data mismatch")
	}
}

func TestParseWAV_ShortData(t *testing.T) {
	short := []byte{0x01, 0x02, 0x03}
	gotPCM, gotRate := ParseWAV(short)
	if gotRate != 0 {
		t.Errorf("expected sampleRate=0 for short input, got %d", gotRate)
	}
	if !bytes.Equal(gotPCM, short) {
		t.Error("expected unchanged data for short input")
	}
}
