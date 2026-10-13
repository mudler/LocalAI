+++
title = "Focused addon testing"
toc = true
description = "Prepare and verify the toolchain for focused addon tests."
+++

The addon verification scripts separate explicit dependency setup from offline checks.
Run all commands from the repository root. Only the `prerequisites` suite is currently available.
Unknown suites, empty selections, skipped required tests, and failed commands return a nonzero status.

## Required tools

Provide these tools before running setup:

- Go 1.26.x, with `GOTOOLCHAIN=local`. Other minor versions are rejected.
- Node 22.12 or later within Node 22, with npm. The validated setup uses Node 22.22.0.
- `protoc` 26.1. The general Makefile downloader uses a different version.
- A native C compiler (`cc`, or the executable specified by `CC`) with `CGO_ENABLED=1`.
- Python 3, POSIX shell tools, and the runtime libraries required by Chromium.

Setup and integration preparation require at least 20 GiB of free disk space before each major step.
They do not install system packages or fetch Go, Node, or `protoc` automatically.
Place locally provisioned executables in `.cache/addons/bin`, or put them on `PATH`.

For Linux x86-64, the validated tool archives have these SHA-256 checksums:

| Archive | Source | SHA-256 |
| --- | --- | --- |
| `node-v22.22.0-linux-x64.tar.xz` | [Node 22.22.0 distribution](https://nodejs.org/dist/v22.22.0/) | `9aa8e9d2298ab68c600bd6fb86a6c13bce11a4eca1ba9b39d79fa021755d7c37` |
| `protoc-26.1-linux-x86_64.zip` | [Protocol Buffers 26.1 release](https://github.com/protocolbuffers/protobuf/releases/tag/v26.1) | `a7be2928c0454f132c599e25b79b7ad1b57663f2337d7f7e468a1d59b98ec1b0` |

Validate archive checksums before extraction. These archives do not apply to other architectures.

## Explicit setup

Setup downloads Go dependencies, installs the pinned protobuf plugins, and runs `npm ci` against the React lockfile.
It then installs Chromium through the local Playwright CLI, unless you supply an existing browser.

```sh
sh scripts/addons/prepare.sh --prepare
```

The protobuf plugin pins match the Makefile:

- `google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2`
- `google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af`

Go resolves the second pin to module version `v1.4.0`.
The check validates each plugin's embedded module version and checksum, not its version label alone.
Go downloads use module checksum authentication. npm uses the lockfile's package integrity records.

The lockfile currently selects Playwright 1.58.2 and Chromium revision 1208 (Chrome 145.0.7632.6).
The installed Playwright package selects the browser revision; the scripts never invoke `npx`.
To provision only the browser after npm setup, run:

```sh
export PATH="$PWD/.cache/addons/bin:$PATH"
export PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/addons/browsers"
core/http/react-ui/node_modules/.bin/playwright install chromium
```

Keep `PLAYWRIGHT_BROWSERS_PATH` set to this worktree-local path for manual installation.
The preparation and verification scripts set it automatically.
A browser installed in Playwright's default user cache does not satisfy this setup.

Alternatively, set `PLAYWRIGHT_CHROMIUM_PATH` to an absolute executable path before setup and verification.
Setup then skips the browser download. Verification still requires a successful browser launch and rendered UI.
Neither option installs OS libraries. If launch fails, inspect the reported library names and the browser executable with `ldd` on Linux.
Provide a compatible runtime environment or report the missing libraries as a blocker; do not skip the browser probe.

## Prepare generated inputs

After explicit setup, generate only the inputs needed by the focused tests:

```sh
sh scripts/addons/prepare.sh --integration
```

This command generates `pkg/grpc/proto`, builds the React distribution, and builds two auth-enabled test binaries:

- `tests/e2e/mock-backend/mock-backend`
- `tests/e2e-ui/ui-test-server`

It does not build inference backends or run a full repository build.
It disables Go dependency downloads and requires the populated setup cache.
Repeat this step when the proto, UI, or test-server sources change.

## Check and verify

Inspect the toolchain, installed dependencies, and generated inputs without downloading or rebuilding them:

```sh
sh scripts/addons/prepare.sh --check
```

This read-only dependency check is not a browser or SQLite runtime test.
Run the complete readiness gate separately:

```sh
sh scripts/addons/verify.sh prerequisites
```

Verification performs these checks:

1. Run isolated shell fixtures for missing tools, incompatible versions, failed children, invalid reports, and empty or skipped selections.
2. Inspect the real toolchain and generated inputs.
3. Compile and execute a SQLite transaction with CGO, Ginkgo v2, and Gomega.
4. Start the real UI test server with the mock backend on an isolated loopback port.
5. Wait for server health, launch Chromium, and require a rendered React root.

The SQLite probe uses `go test -count=1 -tags auth` and a machine-readable Ginkgo report.
Verification rejects missing, empty, failed, skipped, or unexpected test cases.
It records child commands, output, and status in `.cache/addons/logs/verify-*.jsonl`.
Ginkgo reports use separate unique files in the same directory.
Server processes stop after the probe, including when the browser check fails.

Checks and verification never install dependencies or browsers.
They set `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, and `GOFLAGS=-mod=readonly` for verification builds.
The npm dependency check uses `--offline`.
Missing cached modules or browser files are failures, not permission to download them.

## Cache layout and test inventory

The scripts keep caches and transient results under `.cache/addons`:

| Path | Purpose |
| --- | --- |
| `bin` | Local tools and protobuf plugins; prepended to `PATH` |
| `go`, `go/pkg/mod` | `GOPATH` and `GOMODCACHE` |
| `go-build` | `GOCACHE` |
| `npm` | npm cache |
| `browsers` | Playwright browser installations |
| `tmp` | Setup, fixture, and verification scratch data |
| `logs` | Command results and Ginkgo reports |
| `downloads` | Optional archives from explicit local provisioning |

Do not commit caches, generated distributions, or test binaries.
Keep the command and required-case inventory in `scripts/addons/suites.json` synchronized with the runner.
Add future suites only when their actual tests and commands exist.
A successful prerequisite gate demonstrates readiness, not acceptance of addon functionality that has no tests yet.

## Gateway verification

Run `sh scripts/addons/verify.sh gateway` for the owner carrier and real-auth HTTP suites.
The inventory lists each report separately and requires their exact combined case count.
Missing reports, skipped cases, empty reports, and unsuccessful child commands fail the gate.
The tests use local certificate authorities, pinned mTLS peers, two receiving frontends,
and a real SQLite identity database. No external identity service or addon port is exposed.

The disconnect fixture consumes the POST envelope before waiting for cancellation.
Leaving that body unread prevents the HTTP/1 server from observing disconnect while
its handler waits, which can strand test-server cleanup. Production owner handlers decode
the bounded envelope before starting a relay.
