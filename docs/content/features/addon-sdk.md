+++
disableToc = false
title = "Build an add-on in your own repository"
description = "Write an add-on in its own Go module, import the LocalAI SDK packages, pack it as a zip file and install it from the file."
weight = 6
keywords = ["add-on", "sdk", "go module", "addonpkg", "zip"]
devbuild = "addon"
+++

An add-on does not need to live in the LocalAI repository. You can keep it in your own repository, with its own Go module. You import the helper packages from `github.com/mudler/LocalAI`, build the executable, and pack a zip file with the LocalAI packaging tool. A user installs the zip with **Install from file**.

This page shows the whole path with one small add-on. A complete copy of it is in the LocalAI repository, in `examples/addon-hello`. For the format of the manifest and the protocol, see the [contract reference]({{% relref "/addons/contract-reference" %}}). For a tutorial inside a checkout of the repository, see [Build your own add-on]({{% relref "/addons/build-your-own" %}}).

{{% callout style="note" %}}
An add-on in another repository cannot have a UI yet. The UI build reads the UI tree of the LocalAI repository. A tools-only add-on is fully supported.
{{% /callout %}}

## What you need

- Go 1.26 or newer.
- A LocalAI version or commit to build against. The add-on API is part of that version.
- The packaging tool `addonpkg`. You can install it without a checkout:

```bash
go install github.com/mudler/LocalAI/scripts/addons/cmd/addonpkg@<version>
```

`<version>` is a release tag or a commit. Use the same one as in your `go.mod`.

## 1. Set up the module

```bash
mkdir addon-hello && cd addon-hello
go mod init example.com/addon-hello
go get github.com/mudler/LocalAI@<version>
```

The `go.mod` file now requires LocalAI. Do not add a `replace` line for a released add-on. During development you can point it at a local checkout:

```bash
go mod edit -replace github.com/mudler/LocalAI=../LocalAI
```

The layout of the repository is:

```text
addon-hello/
  go.mod
  manifest.json
  package/platforms.yaml
  skills/hello/SKILL.md
  cmd/hello/main.go
  internal/tools/tools.go
```

## 2. Write the tool

The runtime has five methods. A read-only add-on answers the last four with an error.

```go
// internal/tools/tools.go
package tools

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

var errNoTool = errors.New("this tool is not served by the hello add-on")

type Runtime struct{}

var _ contract.Runtime = Runtime{}

func (Runtime) Read(_ context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if tool != "greet" {
		return contract.Result{}, protocol.ErrNotFound
	}
	var a struct {
		Tool string `json:"tool"`
		Name string `json:"name"`
	}
	if err := protocol.DecodeArgs(env.Request.Arguments, &a); err != nil {
		return contract.Result{}, err
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return contract.Result{Payload: protocol.Payload(map[string]any{"ok": false, "error": "name is empty"})}, nil
	}
	return contract.Result{Payload: protocol.Payload(map[string]any{"ok": true, "greeting": "Hello, " + name + "!"})}, nil
}

func (Runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (Runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (Runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (Runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}
```

## 3. Write `main.go`

The host starts the program with the argument `--localai-addon-v1`. `protocol.Run` reads the startup data and serves the tools on a local port.

```go
// cmd/hello/main.go
package main

import (
	"os"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"

	"example.com/addon-hello/internal/tools"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime {
		return tools.Runtime{}
	}))
}
```

## 4. Write the manifest

The manifest says who the add-on is, which host versions it needs and which tools it offers. Save it as `manifest.json` in the root of your repository.

```json
{
  "schemaVersion": 1,
  "id": "hello",
  "version": "0.1.0",
  "executable": "hello",
  "os": "linux",
  "arch": "amd64",
  "host": {"min": 1, "max": 1},
  "api": {"min": 2, "max": 2},
  "protocol": {"min": 1, "max": 1},
  "dataFormat": {"min": 1, "max": 1},
  "capabilities": {
    "tools": ["greet"],
    "skillDefinitions": [{"name": "hello"}],
    "toolDefinitions": [
      {
        "name": "greet",
        "description": "Greet a person by name. Use it when the user asks you to say hello to someone.",
        "kind": "read",
        "inputSchema": {
          "type": "object",
          "properties": {
            "name": {"type": "string", "description": "The name to greet.", "maxLength": 80}
          },
          "required": ["name"],
          "additionalProperties": false
        }
      }
    ]
  }
}
```

The `api` range must match the API version of the LocalAI version you build against. The packaging tool refuses a manifest with another range. `os` and `arch` are filled in for each platform when the package is built.

Add a skill in `skills/hello/SKILL.md` that tells the model when to call the tool. A manifest with `skillDefinitions` needs a skill folder with the same name.

## 5. Write the platform file

`package/platforms.yaml` lists the platforms of the add-on. The paths in this file are relative to the root of your repository.

```yaml
schema: 1
addon: hello
executable: hello
manifest: manifest.json
skills: skills
platforms:
  - platform: linux/amd64
    enabled: true
    runner: ubuntu-24.04
    native_check: executable-start
```

A platform is built only when `enabled` is `true`. For a test build of a platform that is not enabled, use `--local-check`.

## 6. Build the zip

Build the executable for your platform, then run the three steps of `addonpkg`: `build` makes a package layout, `verify` checks it, and `zip` writes the file.

```bash
go build -trimpath -o hello ./cmd/hello

addonpkg build  --config package/platforms.yaml --root . --layout hello-layout \
  --version 0.1.0 --platform linux/amd64 --executable ./hello
addonpkg verify --config package/platforms.yaml --layout hello-layout \
  --version 0.1.0 --platform linux/amd64
addonpkg zip    --config package/platforms.yaml --layout hello-layout \
  --version 0.1.0 --platform linux/amd64 --out dist
```

`--layout` must not exist, or must be empty. The result is two files:

```text
dist/hello-0.1.0-linux-amd64.zip
dist/hello-0.1.0-linux-amd64.zip.sha256
```

The `.sha256` file is in `sha256sum` format. Build a platform natively: an executable of another platform is not accepted by the host that runs there.

If you have a checkout of the LocalAI repository, one command does the same steps. It builds the executable from `cmd/hello`, so it needs the Go module of your add-on to be resolvable:

```bash
make addons-zip ADDON_ROOT=/path/to/addon-hello
# or, without make
sh scripts/addons/zip.sh --root /path/to/addon-hello
```

With a root, the files go to `<root>/dist/addons`. Use `ADDON_EXECUTABLE` (`--executable`) to pack a binary that you built yourself.

## 7. Install it

Check the file first:

```bash
local-ai addons inspect dist/hello-0.1.0-linux-amd64.zip
```

Then upload it with `local-ai addons install <file>`, or choose **Install from file** in the Add-ons page. You review the permissions in the browser.

{{% callout style="warning" %}}
Nothing signs a local file, so LocalAI cannot tell who made it. An installed add-on runs as the operating system user that runs LocalAI and is not sandboxed. Install a file only if you trust whoever made it. The checksum in the `.sha256` file tells you that the file did not change. It does not tell you who made it.
{{% /callout %}}

## What the SDK promises

The packages under `pkg/addons/sdk` are the public helpers of an add-on:

| Package | Use |
|---|---|
| `protocol` | Reads the startup data, serves the protocol, exits with the parent process. |
| `hostclient` | Calls the host operations that the manifest asks for. |
| `idle` | A runtime with no tools, for an add-on that only adds a UI. |
| `atomicfile` | Writes a file so that a reader never sees half of it. |
| `argutil` | Reads typed arguments of a tool call. |

Together with `pkg/addons/contract`, they follow the manifest `api` range. As long as your manifest declares the `api` version that you built against, a new LocalAI version with the same `api` version keeps your add-on working. A change that breaks a caller of these packages needs a new `api` version. Adding a function or a type is not a break. A test in the LocalAI repository records the exported names of these packages, so a break cannot land by accident.

These packages never import `core/` or an `internal` path, so they work from another module. Other helper packages under `addons/internal`, such as the connector and the UI build tools, are not part of the SDK. You cannot import them.
