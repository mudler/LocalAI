# p2pmobile

The P2P library of the LocalAI Android client. It is a small Go facade over
`core/services/p2pnet`, `core/services/p2ppair` and `pairclient`, built into
an Android library (`.aar`) with `gomobile bind`.

This directory is a separate Go module. Its `go.mod` points at the main
module with `replace github.com/mudler/LocalAI => ../..`, so the main
`go.mod` does not need `golang.org/x/mobile`, and `go test ./...` at the
repository root does not build this module.

## API

Go package `p2pmobile`, Java package `io.localai.p2p.p2pmobile`:

| Go | Java | What it does |
|---|---|---|
| `Version()` | `P2pmobile.version()` | Returns `p2pmobile/1`. Call it first to check that the native library loaded. |
| `Pair(identityPath, inviteLink, deviceName, allowOpenRelay)` | `P2pmobile.pair(...)` | Redeems an invite link. Returns JSON: `device_id`, `kind`, `phrase`, `server{peer_id,name,version}`, `relays`, `addrs`, and `client_key` when the server sends one. Creates the identity file when it does not exist. Blocks for up to 2 minutes. |
| `PeerID(identityPath)` | `P2pmobile.peerID(...)` | Returns the peer ID of an identity file. Does not create the file. |
| `StartForward(identityPath, peerID, listenAddr, relays, addrs, cb)` | `P2pmobile.startForward(...)` | Listens on `listenAddr` (use a loopback address) and forwards each connection to the peer. `relays` and `addrs` are JSON arrays of multiaddrs, or empty. |
| `(*Forwarder).Addr()` | `Forwarder.addr()` | The address the forwarder listens on. |
| `(*Forwarder).Stop()` | `Forwarder.stop()` | Closes the listener, the connections and the host. You can call it more than once. |
| `StateCallback.OnState(json)` | `StateCallback.onState(String)` | Gets one JSON line per state change: `{"state":"ready","addr":...}`, `{"state":"up","path":...}`, `{"state":"down","error":...}`, `{"state":"closed"}`. |

`Pair` and `StartForward` block. Call them off the main thread.

## Build

You need Go (the version in `go.mod`), a JDK (17 or newer) and the Android
SDK with the NDK. Set `ANDROID_HOME`, or `ANDROID_NDK_HOME` for a specific
NDK.

```sh
bash build.sh               # arm64 only, for release builds
bash build.sh arm64,amd64   # also x86_64, for the emulator
```

The script writes `build/p2pmobile.aar`. The Android app uses it when the
file exists. The script builds `gomobile` and `gobind` at the version that
`go.mod` pins (the `tool` directive). It fails when a native library in the
AAR has LOAD segments that are not aligned to 16 KB.

The link step needs `-ldflags=-checklinkname=0`, because
`github.com/wlynxg/anet` uses `go:linkname`. libp2p's WebRTC transport
imports it.

## Test

```sh
go vet ./... && go test ./...
```
