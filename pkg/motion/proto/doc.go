// SPDX-License-Identifier: MIT

// Package motionpb holds the Go code that protoc generates from motion.proto
// (make protogen-go). The generated files are not in the repository. This file
// keeps the package resolvable without them, so that go mod tidy works in tools
// that do not run the generator, such as Dependabot.
package motionpb
