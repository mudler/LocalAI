// SPDX-License-Identifier: MIT
package motion

import (
	"fmt"
	motionpb "github.com/mudler/LocalAI/pkg/motion/proto"
	"math"
)

// ValidateFrame bounds allocation and rejects invalid subject boxes before a
// host forwards data to a native inference process.
func ValidateFrame(f *motionpb.Frame) error {
	if f == nil {
		return fmt.Errorf("frame or reset required")
	}
	n := uint64(f.Width) * uint64(f.Height)
	if f.Width < 8 || f.Height < 8 || f.Width > 32766 || f.Height > 32766 || n > 16000000 || uint64(len(f.Rgb)) != n*3 || f.SourceTimeUs < 0 {
		return fmt.Errorf("invalid RGB dimensions, byte length or timestamp")
	}
	b := f.SubjectBox
	if len(b) == 0 {
		return nil
	}
	if len(b) != 4 {
		return fmt.Errorf("subject_box requires four coordinates")
	}
	for _, v := range b {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("subject_box must be finite")
		}
	}
	if b[0] < 0 || b[1] < 0 || b[2] > float32(f.Width-1) || b[3] > float32(f.Height-1) || b[2] <= b[0] || b[3] <= b[1] {
		return fmt.Errorf("subject_box must be ordered XYXY within image pixel coordinates")
	}
	return nil
}
