package sound

import (
	"math"
	"testing"
)

func TestBytesToInt16sLE_and_Int16toBytesLE_Roundtrip(t *testing.T) {
	values := []int16{0, 1, -1, 32767, -32768}
	b := Int16toBytesLE(values)
	got := BytesToInt16sLE(b)

	if len(got) != len(values) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(values))
	}
	for i, v := range values {
		if got[i] != v {
			t.Errorf("index %d: got %d, want %d", i, got[i], v)
		}
	}
}

func TestBytesToInt16sLE_PanicsOnOddLength(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on odd-length input, got none")
		}
	}()
	BytesToInt16sLE([]byte{0x01, 0x02, 0x03})
}

func TestBytesToInt16sLE_EmptyInput(t *testing.T) {
	got := BytesToInt16sLE([]byte{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got length %d", len(got))
	}
}

func TestInt16toBytesLE_EmptyInput(t *testing.T) {
	got := Int16toBytesLE([]int16{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got length %d", len(got))
	}
}

func TestResampleInt16_Identity(t *testing.T) {
	src := generateSineWave(440, 16000, 320)
	dst := ResampleInt16(src, 16000, 16000)

	if len(dst) != len(src) {
		t.Fatalf("length mismatch: got %d, want %d", len(dst), len(src))
	}
	for i := range src {
		if src[i] != dst[i] {
			t.Errorf("sample %d differs: got %d, want %d", i, dst[i], src[i])
		}
	}
}

func TestResampleInt16_Downsample_48k_to_16k(t *testing.T) {
	// 960 samples at 48kHz = 20ms
	src := generateSineWave(440, 48000, 960)
	dst := ResampleInt16(src, 48000, 16000)

	expectedLen := 320
	if len(dst) != expectedLen {
		t.Fatalf("expected %d samples, got %d", expectedLen, len(dst))
	}

	// Verify the output still contains a reasonable sine wave
	freq := estimateFrequency(dst, 16000)
	if math.Abs(freq-440) > 50 {
		t.Errorf("estimated frequency %.1f Hz, expected ~440 Hz", freq)
	}
}

func TestResampleInt16_Upsample_16k_to_48k(t *testing.T) {
	// 320 samples at 16kHz = 20ms
	src := generateSineWave(440, 16000, 320)
	dst := ResampleInt16(src, 16000, 48000)

	expectedLen := 960
	if len(dst) != expectedLen {
		t.Fatalf("expected %d samples, got %d", expectedLen, len(dst))
	}

	freq := estimateFrequency(dst, 48000)
	if math.Abs(freq-440) > 50 {
		t.Errorf("estimated frequency %.1f Hz, expected ~440 Hz", freq)
	}
}

func TestResampleInt16_DoubleResamplingQuality(t *testing.T) {
	// Compare 48k->24k->16k vs direct 48k->16k
	src := generateSineWave(440, 48000, 4800) // 100ms

	direct := ResampleInt16(src, 48000, 16000)

	step1 := ResampleInt16(src, 48000, 24000)
	double := ResampleInt16(step1, 24000, 16000)

	// Lengths should be the same
	minLen := len(direct)
	if len(double) < minLen {
		minLen = len(double)
	}

	corr := computeCorrelation(direct[:minLen], double[:minLen])
	if corr < 0.95 {
		t.Errorf("double resampling correlation %.4f < 0.95 (quality loss too high)", corr)
	}
}

func TestResampleInt16_SingleSample(t *testing.T) {
	src := []int16{1000}
	got := ResampleInt16(src, 48000, 16000)
	if len(got) == 0 {
		t.Fatal("expected non-empty output for single-sample input")
	}
	if got[0] != 1000 {
		t.Errorf("expected sample value 1000, got %d", got[0])
	}
}

func TestResampleInt16_EmptyInput(t *testing.T) {
	got := ResampleInt16(nil, 48000, 16000)
	if got != nil {
		t.Errorf("expected nil for empty input, got length %d", len(got))
	}
}

func TestCalculateRMS16_ConstantSignal(t *testing.T) {
	buf := make([]int16, 1000)
	for i := range buf {
		buf[i] = 1000
	}
	rms := CalculateRMS16(buf)
	if math.Abs(rms-1000) > 0.01 {
		t.Errorf("expected RMS=1000, got %.4f", rms)
	}
}

func TestCalculateRMS16_Silence(t *testing.T) {
	buf := make([]int16, 1000)
	rms := CalculateRMS16(buf)
	if rms != 0 {
		t.Errorf("expected RMS=0, got %.4f", rms)
	}
}

func TestCalculateRMS16_KnownSineWave(t *testing.T) {
	// RMS of a sine wave with amplitude A is A/sqrt(2)
	amplitude := float64(math.MaxInt16 / 2)
	buf := generateSineWave(440, 16000, 16000) // 1 second
	rms := CalculateRMS16(buf)
	expectedRMS := amplitude / math.Sqrt(2)

	tolerance := expectedRMS * 0.02
	if math.Abs(rms-expectedRMS) > tolerance {
		t.Errorf("expected RMS≈%.1f, got %.1f (tolerance %.1f)", expectedRMS, rms, tolerance)
	}
}
