//go:build cgo && (!nosqlite || !purego_utp)

package downloader

// This file makes a missing build tag fail at compile time with a message you
// can read, instead of at link time with hundreds of "multiple definition of
// sqlite3_*" lines.
//
// The BitTorrent client has two optional C dependencies: the SQLite library of
// crawshaw, which would be a second copy next to mattn/go-sqlite3 in this
// binary and cannot link with it, and the libutp C++ library. The tags
// "nosqlite" and "purego_utp" switch them off and use the pure-Go code the
// client also has. With both tags the BitTorrent client adds no cgo to
// LocalAI.
//
// "make", the Dockerfile and the golangci-lint, gopls and VS Code settings in
// this repository set the tags for you. If you run go directly, add them:
// go build -tags nosqlite,purego_utp ./...
// See "Build tags" in CONTRIBUTING.md.
const _ uint8 = "LocalAI must be built with the build tags nosqlite and purego_utp when cgo is on. Run: go build -tags nosqlite,purego_utp ./..., or use make. See Build tags in CONTRIBUTING.md"
