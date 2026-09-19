// SPDX-License-Identifier: MIT
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ebitengine/purego"
	"github.com/mudler/LocalAI/pkg/grpc"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "gRPC listen address")
	flag.Parse()
	if err := loadNativeLibrary(os.Getenv("KIMODO_LIBRARY")); err != nil {
		panic(err)
	}
	if err := grpc.StartServer(*addr, &Kimodo{}); err != nil {
		panic(err)
	}
}

func loadNativeLibrary(path string) error {
	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	for _, binding := range []struct {
		function any
		name     string
	}{
		{&nativeABI, "kimodo_abi_version"},
		{&nativeLoad, "kimodo_model_load"},
		{&nativeFree, "kimodo_model_free"},
		{&nativeGenerate, "kimodo_generate"},
		{&nativeMotionFree, "kimodo_motion_free"},
		{&nativeFrames, "kimodo_motion_frames"},
		{&nativeJoints, "kimodo_motion_joints"},
		{&nativeRotations, "kimodo_motion_local_rotations_xyzw"},
		{&nativeRoots, "kimodo_motion_root_positions"},
		{&nativeConfigure, "localai_kimodo_configure"},
		{&nativeJointName, "localai_kimodo_joint_name"},
		{&nativeJointParent, "localai_kimodo_joint_parent"},
		{&nativeJointOffset, "localai_kimodo_joint_offset"},
	} {
		purego.RegisterLibFunc(binding.function, lib, binding.name)
	}
	if version := nativeABI(); version != 1 {
		return fmt.Errorf("kimodo ABI mismatch: expected 1, got %d", version)
	}
	return nil
}
