// SPDX-License-Identifier: MIT
package main

import (
	"encoding/json"
	"math"
	"unsafe"

	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("Root displacement capability", func() {
	BeforeEach(func() { old := resultIntervalStart; DeferCleanup(func() { resultIntervalStart = old }) })
	for _, tc := range []struct {
		name, profile, channels string
		symbol, want            bool
	}{
		{"supported", "smpl24", `{"root_displacement":3}`, true, true},
		{"old library", "smpl24", `{"root_displacement":3}`, false, false},
		{"old definition", "smpl24", `{}`, true, false},
		{"wrong shape", "smpl24", `{"root_displacement":2}`, true, false},
		{"malformed", "smpl24", `null`, true, false},
		{"SOMA basis", "soma77", `{"root_displacement":3}`, true, false},
	} {
		It(tc.name, func() {
			resultIntervalStart = nil
			if tc.symbol {
				resultIntervalStart = func(uintptr, *int64, *byte, uint64) int32 { return 0 }
			}
			raw := map[string]json.RawMessage{"channels": json.RawMessage(tc.channels), tc.profile: json.RawMessage(`{"schema":"test","joint_names":["root"],"parents":[-1],"root":0,"rest_local_translations":[0,0,0],"rest_local_rotations":[1,0,0,0]}`)}
			d, err := publicDefinition(raw, tc.profile)
			Expect(err).NotTo(HaveOccurred())
			found := false
			for _, c := range d.Channels {
				found = found || c == "root_displacement"
			}
			Expect(found).To(Equal(tc.want))
		})
	}
})

var _ = Describe("Root displacement results", func() {
	BeforeEach(func() {
		oldInfo, oldCopy, oldStart := resultInfo, resultCopy, resultIntervalStart
		DeferCleanup(func() { resultInfo, resultCopy, resultIntervalStart = oldInfo, oldCopy, oldStart })
	})
	for _, tc := range []struct {
		name           string
		start          int64
		delta          []float32
		enabled        bool
		flags, outcome uint32
		wantErr        bool
	}{
		{"zero timestamp", 0, []float32{1, 2, 3}, true, 0, 1, false},
		{"irregular interval", 123, []float32{-.1, 0, .2}, true, 0, 1, false},
		{"negative timestamp", -1, []float32{1, 2, 3}, true, 0, 1, true},
		{"empty interval", 1000, []float32{1, 2, 3}, true, 0, 1, true},
		{"future timestamp", 1001, []float32{1, 2, 3}, true, 0, 1, true},
		{"missing channel", 0, nil, true, 0, 1, true},
		{"short channel", 0, []float32{1, 2}, true, 0, 1, true},
		{"oversize channel", 0, []float32{1, 2, 3, 4}, true, 0, 1, true},
		{"nonfinite channel", 0, []float32{float32(math.NaN()), 0, 0}, true, 0, 1, true},
		{"legacy", 0, nil, false, 0, 1, false},
		{"reset", 0, nil, true, 1, 1, false},
		{"warmup", 0, nil, true, 1, 0, false},
		{"lost", 0, nil, true, 1, 2, false},
	} {
		It(tc.name, func() {
			touched := false
			resultInfo = func(_ uintptr, s *uint64, ts *int64, e, tr *uint64, o, f *uint32, _ *byte, _ uint64) int32 {
				*s = 2
				*ts = 1000
				*e = 3
				*tr = 4
				*o = tc.outcome
				*f = tc.flags
				return 0
			}
			resultIntervalStart = func(_ uintptr, start *int64, _ *byte, _ uint64) int32 { touched = true; *start = tc.start; return 0 }
			resultCopy = func(_ uintptr, ch uint32, out *float32, cap uint64, n *uint64, _ *byte, _ uint64) int32 {
				data := map[uint32][]float32{6: make([]float32, 3), 10: make([]float32, 4), 3: make([]float32, 72), 4: {1, 0, 0, 0}}[ch]
				if ch == 14 {
					touched = true
					data = tc.delta
				}
				*n = uint64(len(data))
				if out != nil {
					copy(unsafe.Slice(out, int(cap)), data)
				}
				return 0
			}
			out, _, err := readResult(1, "smpl24", poseProjection{}, tc.enabled)
			Expect(err != nil).To(Equal(tc.wantErr), "%v", err)
			if tc.wantErr {
				return
			}
			want := tc.enabled && tc.flags&1 == 0 && tc.outcome == 1
			Expect(touched).To(Equal(want))
			b, err := proto.Marshal(out)
			Expect(err).NotTo(HaveOccurred())
			decoded := new(motion.Output)
			Expect(proto.Unmarshal(b, decoded)).To(Succeed())
			if want {
				p := decoded.GetPose()
				Expect(p.DisplacementStartTimeUs).To(Equal(tc.start))
				Expect(p.RootDisplacement).To(Equal(tc.delta))
			}
		})
	}
})
