//go:build ruleguard

// Package gorules holds the go-ruleguard rules gocritic runs inside
// `make lint`. It is never compiled into the binary: the build tag keeps it out
// of every normal build, and golangci-lint loads the file as data.
package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// backendWrapperMustBeUnwrappable fires on a struct that decorates a gRPC
// backend by embedding the raw interface.
//
// This exists because the same defect shipped twice. A wrapper that embeds
// grpc.Backend inherits exactly the methods Backend declares and nothing else.
// grpc.DialErrorReporter is deliberately NOT on Backend, so a wrapped client
// silently stops answering "did the transport fail, or did the backend die",
// and every guard built on that answer reads nil. The consequence is not
// subtle: core/services/nodes and pkg/model delete replica rows and stop
// backends on that answer, so a wrapper that swallows it turns a momentary loss
// of route into fleet-wide model reclamation.
//
// The rule is SYNTACTIC, and deliberately so. The obvious formulation, "embeds
// a backend and has no Unwrap", cannot be written: HasMethod rejects inline
// signatures outright ("inline func signatures are not supported yet"), its
// method-reference form needs a package ruleguard's own typechecker can import
// and it cannot import this module, and Implements tests the VALUE method set
// while every Unwrap here would be on a pointer receiver. So instead of
// checking for the method, this checks for the shape that CANNOT lack it:
// grpc.WrappedBackend provides the same pass-through method set plus Unwrap,
// with a value receiver, so anything embedding it is transparent by
// construction. Forgetting is then not expressible rather than merely
// discouraged, which is the same move loopbackService makes in the worker.
//
// Test doubles are excluded by path in .golangci.yml: they embed a NIL backend
// to inherit the interface's method set, decorate nothing, and have no
// transport answer to forward.
func backendWrapperMustBeUnwrappable(m dsl.Matcher) {
	m.Import("github.com/mudler/LocalAI/pkg/grpc")

	m.Match(
		`type $w struct { $*_; grpc.Backend; $*_ }`,
		`type $w struct { $*_; grpc.ControlBackend; $*_ }`,
		`type $w struct { $*_; grpc.InferenceBackend; $*_ }`,
	).
		Report(`$w decorates a gRPC backend by embedding the raw interface, so grpc.LastDialErrorOf cannot see through it and every transport-failure guard behind it reads nil, which deletes replica rows for workers that are merely unroutable. Embed grpc.WrappedBackend instead: it gives the same pass-through plus Unwrap. If $w decorates nothing, silence this with //nolint:gocritic and say so.`)
}
