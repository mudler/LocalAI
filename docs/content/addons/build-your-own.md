+++
disableToc = false
title = "Build your own add-on"
description = "A tutorial that builds a small add-on with one tool, a skill and a test."
weight = 20
+++

This tutorial builds an add-on called `hello`. It has one tool, `greet`, and one skill. You test it, pack it as a zip file and install it from the file. It does not need a UI, a database or a network.

{{% callout style="note" %}}
The Go helper package that the first-party add-ons use is `internal` to the LocalAI repository. This tutorial puts the add-on inside a checkout of the repository, like the first-party add-ons. An add-on made outside the repository must speak the same wire protocol itself. The [contract reference]({{% relref "/addons/contract-reference#managed-runtime" %}}) describes it.
{{% /callout %}}

## What you need

- A checkout of the LocalAI repository.
- Go 1.26 or newer. The `go.mod` file of the repository names the exact version.
- Linux on amd64. The manifest below names that platform.

## 1. Make the folders

```bash
mkdir -p addons/hello/cmd/hello addons/hello/internal/tools addons/hello/skills/hello addons/hello/package
```

## 2. Write the manifest

The manifest says who the add-on is, which host it needs and which tools it offers. Save it as `addons/hello/manifest.json`. A manifest is one JSON object of at most 64 KiB. Unknown fields are refused.

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

- `kind: "read"` means the tool changes nothing, so it runs without a confirmation.
- `skillDefinitions` names the skill folder under `skills/`. A package with skills and no definition is refused.
- The four `min` and `max` pairs are the versions of the host contract. Copy them from a first-party manifest, such as `addons/calendar/manifest.json`.

## 3. Write the tool

Save this as `addons/hello/internal/tools/tools.go`. The runtime has five methods. A read-only add-on answers the last four with an error.

```go
// SPDX-License-Identifier: MIT

// Package tools serves the tools of the Hello add-on.
package tools

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ToolGreet is the one tool. It is an entry of capabilities.tools in the manifest.
const ToolGreet = "greet"

var errNoTool = errors.New("this tool is not served by the hello add-on")

// Runtime is the add-on runtime. It keeps no state.
type Runtime struct{}

var _ contract.Runtime = Runtime{}

// Read serves the read tools.
func (Runtime) Read(_ context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if tool != ToolGreet {
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
		// The model can correct this, so answer with a message, not an error.
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

The host puts the tool name in the arguments. `protocol.DecodeArgs` refuses unknown fields. The user comes from the host in `env.Authority.Actor`, so an add-on never decides who is calling.

## 4. Write the program

Save this as `addons/hello/cmd/hello/main.go`. The host starts the program with the argument `--localai-addon-v1`. `protocol.Run` reads the startup data and serves the tools on a local port.

```go
// SPDX-License-Identifier: MIT

// Command hello is the executable of the Hello add-on.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/hello/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime {
		return tools.Runtime{}
	}))
}
```

## 5. Write the skill

A skill tells the model when and how to use the tool. Save it as `addons/hello/skills/hello/SKILL.md`.

```markdown
---
name: hello
description: Use when the user asks you to greet someone or to say hello to a person by name.
---

# Hello

Call `greet` with the name the user gave. Say the greeting from the answer back to the user. If the user gave no name, ask for one. Do not guess a name.
```

## 6. Write the package file

Save this as `addons/hello/package/platforms.yaml`. It says which platforms the packager builds.

```yaml
schema: 1
addon: hello
executable: hello
manifest: addons/hello/manifest.json
skills: addons/hello/skills
platforms:
  - platform: linux/amd64
    enabled: true
    runner: ubuntu-24.04
    native_check: executable-start
```

## 7. Test it

Save this as `addons/hello/internal/tools/tools_test.go`. It calls the tool and checks that the manifest passes the same parser that the host uses.

```go
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/hello/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func envelope(args string) contract.Envelope {
	return contract.Envelope{
		Request: contract.Request{AddonID: "hello", OperationID: "op1", Generation: 1, Action: "read", Arguments: json.RawMessage(args)},
		Authority: contract.Authority{
			Actor: contract.Actor{UserID: "alice", CredentialID: "k", Mode: "named-user"},
			Scope: "read", ExpiresAt: time.Now().Add(time.Minute),
		},
	}
}

func TestGreet(t *testing.T) {
	res, err := tools.Runtime{}.Read(context.Background(), envelope(`{"tool":"greet","name":"Ada"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Payload) != `{"greeting":"Hello, Ada!","ok":true}` {
		t.Fatalf("unexpected payload %s", res.Payload)
	}
}

func TestGreetRefusesUnknownField(t *testing.T) {
	if _, err := (tools.Runtime{}).Read(context.Background(), envelope(`{"tool":"greet","name":"Ada","x":1}`)); err == nil {
		t.Fatal("an unknown argument must be refused")
	}
}

// The manifest must parse and pass the same checks as an installed package.
func TestManifest(t *testing.T) {
	data, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ParseManifest(data); err != nil {
		t.Fatal(err)
	}
}
```

Run it from the root of the repository.

```bash
go vet ./addons/hello/...
go test ./addons/hello/...
```

Both commands must pass. For the larger suites that check the contract, see [Testing add-ons]({{% relref "/addons/testing" %}}).

## 8. Pack it

```bash
sh scripts/addons/zip.sh --addon hello --local-check --out dist/addons
```

`--local-check` lets you pack a platform that is not in the official release matrix. The output is a zip and a `.sha256` file, named `hello-0.1.0-linux-amd64.zip`. The packager says `publishable=false`, because a local package is never published.

## 9. Install it

1. Check the file: `local-ai addons inspect dist/addons/hello-0.1.0-linux-amd64.zip`.
2. On the **Add-ons** page, choose **Install from file** and pick the zip.
3. Read the dialog. It shows "Local file, unsigned". Confirm.
4. Open a chat, switch `hello` on in the **Tools** menu and ask: "Say hello to Ada."

The install needs sign-in to be on, or `--addon-host-ui-grants-without-auth`. See [Install from a file]({{% relref "/addons/contract-reference#install-from-a-file" %}}).

## Next steps

- A tool that changes something uses another `kind`. See [Action tools]({{% relref "/addons/contract-reference#action-tools" %}}) and [Proposals and confirmation]({{% relref "/addons/contract-reference#proposals-and-confirmation" %}}).
- To show a page, add a UI bundle. See [Add-on pages]({{% relref "/addons/contract-reference#add-on-pages" %}}).
- To keep data, use the data directory that the host gives in the startup data.
