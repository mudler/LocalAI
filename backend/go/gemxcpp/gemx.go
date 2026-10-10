// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	motionutil "github.com/mudler/LocalAI/pkg/motion"
	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type GemX struct {
	base.Base
	mu         sync.Mutex
	pipeline   uintptr
	active     bool
	definition map[string]json.RawMessage
}

type loadOptions struct {
	gem, pose, detector, module, device                   string
	threads, window, cadence, selection, precision, index uint32
	gap                                                   int64
}

func parseOptions(o *pb.ModelOptions) (loadOptions, error) {
	c := loadOptions{gem: o.ModelFile, module: os.Getenv("GEMX_MODULE"), device: "CPU", threads: uint32(min(max(int(o.Threads), 1), 8)), window: 30, cadence: 1, selection: 1, gap: 2000000}
	if os.Getenv("GEMX_DEVICE") != "" {
		c.device = os.Getenv("GEMX_DEVICE")
	}
	for _, v := range o.Options {
		k, v, ok := strings.Cut(v, ":")
		if !ok {
			return c, fmt.Errorf("invalid GEM-X option")
		}
		switch k {
		case "vitpose":
			c.pose = v
		case "yolox":
			c.detector = v
		case "device":
			switch v {
			case "cpu":
				c.device = "CPU"
			case "vulkan":
				c.device = "Vulkan"
			default:
				return c, fmt.Errorf("device must be cpu or vulkan")
			}
		case "selection":
			switch v {
			case "continuity":
				c.selection = 1
			case "parity":
				c.selection = 0
			default:
				return c, fmt.Errorf("selection must be continuity or parity")
			}
		case "precision":
			switch v {
			case "strict":
				c.precision = 0
			case "backend_default":
				c.precision = 1
			default:
				return c, fmt.Errorf("precision must be strict or backend_default")
			}
		case "window", "detector_interval", "device_index", "max_gap_us":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return c, fmt.Errorf("invalid %s", k)
			}
			switch k {
			case "window":
				c.window = uint32(n)
			case "detector_interval":
				c.cadence = uint32(n)
			case "device_index":
				c.index = uint32(n)
			case "max_gap_us":
				c.gap = int64(n)
			}
		default:
			return c, fmt.Errorf("unsupported GEM-X option %q", k)
		}
	}
	if c.window < 2 || c.window > 120 || c.cadence < 1 || c.cadence > 30 || c.gap <= 0 {
		return c, fmt.Errorf("window must be 2..120, detector_interval 1..30, max_gap_us positive")
	}
	for _, p := range []*string{&c.gem, &c.pose, &c.detector} {
		if *p == "" {
			return c, fmt.Errorf("model, vitpose and yolox are required")
		}
		if !filepath.IsAbs(*p) {
			*p = filepath.Join(o.ModelPath, *p)
		}
		info, err := os.Stat(*p)
		if err != nil || !info.Mode().IsRegular() {
			return c, fmt.Errorf("model asset is not a regular file: %s", *p)
		}
	}
	if c.module == "" {
		return c, fmt.Errorf("GEMX_MODULE is required")
	}
	return c, nil
}
func (g *GemX) Load(o *pb.ModelOptions) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active {
		return status.Error(codes.ResourceExhausted, "GEM-X session is active")
	}
	c, err := parseOptions(o)
	if err != nil {
		return err
	}
	if g.pipeline != 0 {
		return fmt.Errorf("GEM-X model already loaded")
	}
	buf := make([]byte, 2048)
	scalars := []uint32{c.index, c.threads, c.window, c.cadence, c.selection, c.precision}
	code := liveCreate(c.gem, c.pose, c.detector, c.module, c.device, &scalars[0], c.gap, &g.pipeline, &buf[0], uint64(len(buf)))
	if err := nativeError(code, buf); err != nil {
		return err
	}
	var n uint64
	if err := nativeError(liveDefinition(g.pipeline, nil, 0, &n, &buf[0], uint64(len(buf))), buf); err != nil {
		g.free()
		return err
	}
	if n < 2 || n > 1<<20 {
		g.free()
		return fmt.Errorf("invalid native definition size")
	}
	def := make([]byte, n)
	if err := nativeError(liveDefinition(g.pipeline, &def[0], n, &n, &buf[0], uint64(len(buf))), buf); err != nil {
		g.free()
		return err
	}
	if err := json.Unmarshal(def[:len(def)-1], &g.definition); err != nil {
		g.free()
		return err
	}
	xlog.Info("GEM-X loaded", "device", c.device, "window", c.window, "detector_interval", c.cadence)
	return nil
}
func (g *GemX) Busy() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.active }

func (g *GemX) free() {
	if g.pipeline != 0 {
		liveDestroy(g.pipeline)
		g.pipeline = 0
	}
}
func (g *GemX) Free() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active {
		return status.Error(codes.ResourceExhausted, "GEM-X session is active")
	}
	g.free()
	return nil
}

func supportsRootDisplacement(raw map[string]json.RawMessage, profile string) bool {
	var channels map[string]int
	return resultIntervalStart != nil && profile == "smpl24" && json.Unmarshal(raw["channels"], &channels) == nil && channels["root_displacement"] == 3
}

func publicDefinition(raw map[string]json.RawMessage, profile string) (*motion.Definition, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw[profile], &fields); err != nil {
		return nil, err
	}
	d := &motion.Definition{Profile: profile, Conventions: map[string]string{"time_unit": "microseconds", "time_origin": "caller-declared", "temporal_policy": "accepted-frame-index", "continuous_world_trajectory": "false"}}
	for key, dst := range map[string]any{"schema": &d.Schema, "joint_names": &d.JointNames, "parents": &d.Parents, "root": &d.Root, "rest_local_translations": &d.RestLocalTranslations, "rest_local_rotations": &d.RestLocalRotations} {
		if err := json.Unmarshal(fields[key], dst); err != nil {
			return nil, fmt.Errorf("native definition %s: %w", key, err)
		}
	}
	for _, k := range []string{"units", "handedness", "basis", "space", "quaternion_order", "quaternion_policy", "shape_policy", "reference_shape", "anchor_basis", "reconstruction", "rest_rotation_policy"} {
		if val := fields[k]; val != nil {
			var s string
			if err := json.Unmarshal(val, &s); err != nil {
				return nil, err
			}
			d.Conventions[k] = s
		}
	}
	d.Channels = []string{"positions", "root_translation", "box", "image_positions"}
	if supportsRootDisplacement(raw, profile) {
		d.Channels = append(d.Channels, "root_displacement")
		d.Conventions["root_displacement"] = "metres, end-anchor-local, closed source interval, no cross-epoch continuity"
	}
	d.Conventions["image_positions"] = "source-pixel xy, joint_names order, top-left origin, unmirrored"
	if profile == "soma77" {
		d.Channels = append(d.Channels, "local_rotations", "local_translations", "root_axis_angle")
	} else {
		d.Channels = append(d.Channels, "anchor")
	}
	if len(d.JointNames) != len(d.Parents) || len(d.RestLocalTranslations) != len(d.Parents)*3 || len(d.RestLocalRotations) != len(d.Parents)*4 {
		return nil, fmt.Errorf("invalid native topology")
	}
	return d, nil
}

func (g *GemX) MotionStream(ctx context.Context, recv func() (*pb.MotionRequest, error), send func(*pb.MotionResponse) error) error {
	first, err := recv()
	if err != nil {
		return err
	}
	if first.Profile != "soma77" && first.Profile != "smpl24" {
		return status.Error(codes.InvalidArgument, "profile must be soma77 or smpl24")
	}
	if len(first.Input) != 0 {
		return status.Error(codes.InvalidArgument, "configuration must precede frames")
	}
	g.mu.Lock()
	if g.active || g.pipeline == 0 {
		g.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "GEM-X is unavailable or already streaming")
	}
	g.active = true
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active = false; g.mu.Unlock() }()
	buf := make([]byte, 2048)
	if err := nativeError(liveReset(g.pipeline, &buf[0], uint64(len(buf))), buf); err != nil {
		return err
	}
	def, err := publicDefinition(g.definition, first.Profile)
	if err != nil {
		return err
	}
	projection, err := projectionForProfile(g.definition, first.Profile)
	if err != nil {
		return err
	}

	emit := func(o *motion.Output, metrics map[string]float64) error {
		b, err := proto.Marshal(o)
		if err != nil {
			return err
		}
		return send(&pb.MotionResponse{Output: b, StageMs: metrics})
	}
	if err := emit(&motion.Output{Payload: &motion.Output_Definition{Definition: def}}, nil); err != nil {
		return err
	}
	displacement := supportsRootDisplacement(g.definition, first.Profile)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if req.Profile != "" {
			return status.Error(codes.InvalidArgument, "configuration cannot change")
		}
		input := &motion.Input{}
		if err := proto.Unmarshal(req.Input, input); err != nil {
			return status.Error(codes.InvalidArgument, "invalid motion input")
		}
		if input.GetResetState() {
			if err := nativeError(liveReset(g.pipeline, &buf[0], uint64(len(buf))), buf); err != nil {
				return err
			}
			if err := emit(&motion.Output{Payload: &motion.Output_Event{Event: &motion.Event{Type: "reset"}}}, nil); err != nil {
				return err
			}
			continue
		}
		frame := input.GetFrame()
		if err := validateFrame(frame); err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		var box *float32
		if len(frame.SubjectBox) > 0 {
			box = &frame.SubjectBox[0]
		}
		var result uintptr
		code := liveSubmit(g.pipeline, &frame.Rgb[0], uint64(len(frame.Rgb)), frame.Width, frame.Height, uint64(frame.Width)*3, frame.Sequence, frame.SourceTimeUs, box, uint64(len(frame.SubjectBox)), frame.SubjectId, &result, &buf[0], uint64(len(buf)))
		runtime.KeepAlive(frame)
		if err := nativeError(code, buf); err != nil {
			return err
		}
		projection.width, projection.height = frame.Width, frame.Height
		out, metrics, err := readResult(result, first.Profile, projection, displacement)
		resultDestroy(result)
		if err != nil {
			return err
		}
		if err := emit(out, metrics); err != nil {
			return err
		}
	}
}
func validateFrame(f *motion.Frame) error { return motionutil.ValidateFrame(f) }

func readResult(r uintptr, profile string, projection poseProjection, displacement bool) (*motion.Output, map[string]float64, error) {
	buf := make([]byte, 2048)
	p := &motion.Pose{}
	var outcome uint32
	if err := nativeError(resultInfo(r, &p.Sequence, &p.SourceTimeUs, &p.Epoch, &p.TrackEpoch, &outcome, &p.Flags, &buf[0], uint64(len(buf))), buf); err != nil {
		return nil, nil, err
	}
	copyChannel := func(channel uint32, max uint64) ([]float32, error) {
		var n uint64
		if err := nativeError(resultCopy(r, channel, nil, 0, &n, &buf[0], uint64(len(buf))), buf); err != nil {
			return nil, err
		}
		if n > max {
			return nil, fmt.Errorf("native channel exceeds expected size")
		}
		if n == 0 {
			return nil, nil
		}
		values := make([]float32, n)
		if err := nativeError(resultCopy(r, channel, &values[0], n, &n, &buf[0], uint64(len(buf))), buf); err != nil {
			return nil, err
		}
		for _, v := range values {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, fmt.Errorf("nonfinite native output")
			}
		}
		return values, nil
	}
	metrics := map[string]float64{}
	times, err := copyChannel(11, 9)
	if err != nil {
		return nil, nil, err
	}
	if len(times) >= 5 {
		for i, k := range []string{"detector", "vitpose", "gem", "world", "camera"} {
			metrics[k] = float64(times[i])
		}
	}
	if outcome != 1 {
		names := map[uint32]string{0: "warmup", 2: "lost", 3: "ambiguous"}
		name, ok := names[outcome]
		if !ok {
			return nil, nil, fmt.Errorf("unknown native outcome")
		}
		return &motion.Output{Payload: &motion.Output_Event{Event: &motion.Event{Type: name, Sequence: p.Sequence, SourceTimeUs: p.SourceTimeUs, Epoch: p.Epoch, TrackEpoch: p.TrackEpoch, Flags: p.Flags}}}, metrics, nil
	}
	type channel struct {
		id  uint32
		n   uint64
		dst *[]float32
	}
	channels := []channel{{6, 3, &p.RootTranslation}, {10, 4, &p.Box}}
	if profile == "soma77" {
		channels = append(channels,
			channel{0, 231, &p.Positions},
			channel{1, 308, &p.LocalRotations},
			channel{2, 231, &p.LocalTranslations},
			channel{5, 3, &p.RootAxisAngle})
	} else {
		channels = append(channels,
			channel{3, 72, &p.Positions},
			channel{4, 4, &p.Anchor})
	}
	for _, c := range channels {
		v, err := copyChannel(c.id, c.n)
		if err != nil {
			return nil, nil, err
		}
		if uint64(len(v)) != c.n {
			return nil, nil, fmt.Errorf("incomplete native pose")
		}
		*c.dst = v
	}
	if displacement && p.Flags&1 == 0 {
		if err := nativeError(resultIntervalStart(r, &p.DisplacementStartTimeUs, &buf[0], uint64(len(buf))), buf); err != nil {
			return nil, nil, err
		}
		if p.DisplacementStartTimeUs < 0 || p.DisplacementStartTimeUs >= p.SourceTimeUs {
			return nil, nil, fmt.Errorf("invalid native displacement interval")
		}
		delta, err := copyChannel(14, 3)
		if err != nil {
			return nil, nil, err
		}
		if len(delta) != 3 {
			return nil, nil, fmt.Errorf("incomplete native displacement")
		}
		p.RootDisplacement = delta
	}
	camera, err := copyChannel(7, 231)
	if err != nil {
		return nil, nil, err
	}
	translation, err := copyChannel(8, 3)
	if err != nil {
		return nil, nil, err
	}
	p.ImagePositions = projection.project(camera, translation)
	return &motion.Output{Payload: &motion.Output_Pose{Pose: p}}, metrics, nil
}
