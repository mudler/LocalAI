package utils

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	laudio "github.com/mudler/LocalAI/pkg/audio"
)

// generateTestWav creates a WAV file with a sine-ish tone at the given sample rate,
// channels, and bit depth (only 16-bit supported for simplicity).
func generateTestWav(t *testing.T, path string, sampleRate uint32, numChannels uint16, numSamples int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	bitsPerSample := uint16(16)
	blockAlign := numChannels * (bitsPerSample / 8)
	byteRate := sampleRate * uint32(blockAlign)
	totalSamples := numSamples * int(numChannels)
	dataSize := uint32(totalSamples) * uint32(bitsPerSample/8)

	hdr := laudio.WAVHeader{
		ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
		ChunkSize:     36 + dataSize,
		Format:        [4]byte{'W', 'A', 'V', 'E'},
		Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
		Subchunk1Size: 16,
		AudioFormat:   1,
		NumChannels:   numChannels,
		SampleRate:    sampleRate,
		ByteRate:      byteRate,
		BlockAlign:    blockAlign,
		BitsPerSample: bitsPerSample,
		Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
		Subchunk2Size: dataSize,
	}
	if err := binary.Write(f, binary.LittleEndian, &hdr); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < totalSamples; i++ {
		sample := int16(1000 * (i % 100))
		if err := binary.Write(f, binary.LittleEndian, sample); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAudioToWav_AlreadyCorrectFormat(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "input.wav")
	dst := filepath.Join(dir, "output.wav")

	generateTestWav(t, src, 16000, 1, 1600)

	if err := AudioToWav(src, dst); err != nil {
		t.Fatalf("AudioToWav failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("output not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}
}

func TestAudioToWav_ResampleFrom22050(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "input.wav")
	dst := filepath.Join(dir, "output.wav")

	generateTestWav(t, src, 22050, 1, 22050)

	if err := AudioToWav(src, dst); err != nil {
		t.Fatalf("AudioToWav failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("output not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}

	verifyWavFormat(t, dst, 16000, 1)
}

func TestAudioToWav_StereoDownmix(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "input.wav")
	dst := filepath.Join(dir, "output.wav")

	generateTestWav(t, src, 16000, 2, 1600)

	if err := AudioToWav(src, dst); err != nil {
		t.Fatalf("AudioToWav failed: %v", err)
	}

	verifyWavFormat(t, dst, 16000, 1)
}

func TestAudioToWav_StereoAndResample(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "input.wav")
	dst := filepath.Join(dir, "output.wav")

	generateTestWav(t, src, 44100, 2, 44100)

	if err := AudioToWav(src, dst); err != nil {
		t.Fatalf("AudioToWav failed: %v", err)
	}

	verifyWavFormat(t, dst, 16000, 1)
}

func verifyWavFormat(t *testing.T, path string, expectedRate uint32, expectedChannels uint16) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var hdr laudio.WAVHeader
	if err := binary.Read(f, binary.LittleEndian, &hdr); err != nil {
		t.Fatalf("read header: %v", err)
	}

	if hdr.SampleRate != expectedRate {
		t.Errorf("sample rate = %d, want %d", hdr.SampleRate, expectedRate)
	}
	if hdr.NumChannels != expectedChannels {
		t.Errorf("channels = %d, want %d", hdr.NumChannels, expectedChannels)
	}
	if hdr.BitsPerSample != 16 {
		t.Errorf("bit depth = %d, want 16", hdr.BitsPerSample)
	}
	if hdr.AudioFormat != 1 {
		t.Errorf("audio format = %d, want 1 (PCM)", hdr.AudioFormat)
	}
}
