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
