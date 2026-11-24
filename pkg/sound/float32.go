package sound

import (
	"encoding/binary"
	"math"
)

func BytesFloat32(bytes []byte) float32 {
	bits := binary.LittleEndian.Uint32(bytes)
	float := math.Float32frombits(bits)
	return float
}

// Float32sToInt16LEBytes converts [-1,1] float PCM samples to int16
// little-endian bytes, clamping out-of-range values instead of wrapping.
func Float32sToInt16LEBytes(samples []float32) []byte {
	out := make([]byte, len(samples)*2)
	for i, f := range samples {
		v := int32(f * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}
