package sound

import (
	"encoding/binary"
	"math"
)

func BytesToFloat32Array(aBytes []byte) []float32 {
	aArr := make([]float32, 3)
	for i := 0; i < 3; i++ {
		aArr[i] = BytesFloat32(aBytes[i*4:])
	}
	return aArr
}

func BytesFloat32(bytes []byte) float32 {
	bits := binary.LittleEndian.Uint32(bytes)
	float := math.Float32frombits(bits)
	return float
}
