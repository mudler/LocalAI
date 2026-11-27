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
| `Version()` | `P2pmobile.version()` | Returns `p2pmobile/4`. Call it first to check that the native library loaded. |
| `Pair(identityPath, inviteLink, deviceName, allowOpenRelay)` | `P2pmobile.pair(...)` | Redeems an invite link. Returns JSON: `device_id`, `kind`, `phrase`, `server{peer_id,name,version}`, `relays`, `addrs`, `hints`, `client_key` when the server sends one, and, for a sign-in invite, `device_credential` (a secret: keep it out of logs and the WebView), `sign_in` and `owner_name`. Creates the identity file when it does not exist. Blocks for up to 2 minutes. |
| `PairJSON(identityPath, inviteLink, deviceName, allowOpenRelay)` | `P2pmobile.pairJSON(...)` | Like `Pair`, but never fails. Returns `{"ok":true,"result":{...}}` with the object of `Pair`, or `{"ok":false,"code":"...","detail":"..."}`. `code` is stable; `detail` is the error text, for logs. See the codes below. |
| `PeerID(identityPath)` | `P2pmobile.peerID(...)` | Returns the peer ID of an identity file. Does not create the file. |
| `StartForward(identityPath, peerID, listenAddr, relays, addrs, hints, cb)` | `P2pmobile.startForward(...)` | Listens on `listenAddr` (use a loopback address) and forwards each connection to the peer. `relays`, `addrs` and `hints` are JSON arrays of multiaddrs, or empty. `hints` are the public relays of the invite: they only help a hole punch and never carry data. |
| `(*Forwarder).Addr()` | `Forwarder.addr()` | The address the forwarder listens on. |
| `(*Forwarder).Stop()` | `Forwarder.stop()` | Closes the listener, the connections and the host. You can call it more than once. |
| `StateCallback.OnState(json)` | `StateCallback.onState(String)` | Gets one JSON line per state change: `{"state":"ready","addr":...}`, `{"state":"up","path":...}`, `{"state":"down","error":...}`, `{"state":"closed"}`. A `down` line has `"code":"needs_relay"` when the server was reached only through a hint and no hole punch worked. After each connection that opens, and every 10 minutes while the forward stays up, the forwarder asks the server for its routes and sends `{"state":"routes","addrs":[...],"relays":[...],"hints":[...]}`: the app stores these lists in place of the ones of the invite. A routes line is not a state change. |

`Pair`, `PairJSON` and `StartForward` block. Call them off the main thread.

The error codes of `PairJSON`:

| Code | Meaning |
|---|---|
| `invalid_link` | The text is not an invite link. |
| `expired` | The invite has expired. |
| `open_relay` | The link names an open relay, and `allowOpenRelay` is false. |
| `gone` | The server refused the code: it was used, cancelled or expired. |
| `too_many_tries` | The server saw too many wrong codes from this device or its relay. |
| `wrong_server` | Another peer answered than the one of the link. |
| `needs_relay` | The device reached the server only through a public relay of the link, and no hole punch opened a direct connection. The code was not sent: the invite stays pending. The server needs a relay that carries data. |
| `no_route` | The link names no address, relay or hint. |
| `unreachable` | The server could not be reached. |
| `failed` | Any other error. |

The device host listens on ports that the system picks, so that a hole
punch can reach it. It accepts no inbound connection, except from the
server that a dial of the device waits for during a hole punch.

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
